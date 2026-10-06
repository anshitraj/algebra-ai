package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/project-algebra/algebra/internal/platform/solana"
	"github.com/project-algebra/algebra/providers/paychan"
	"github.com/project-algebra/algebra/providers/x402"
)

const devnetCAIP = "solana:EtWTRABZaYq6iMfeYKouRu166VU2xqa1"

// persona is one demo provider.
type persona struct {
	Name, Title, Class string
	// ListedMinor is what the provider's listing says it charges; AskMinor
	// what its 402 actually asks. For upto, AskMinor is the ceiling.
	ListedMinor, AskMinor int64
	Scheme                string // "exact" or "upto"
	Method                string
	Delay                 time.Duration
	Down                  bool
}

var personas = []persona{
	{Name: "alpha", Title: "Alpha Prices (demo)", Class: "token.price", ListedMinor: 2_000, AskMinor: 2_000, Scheme: "exact", Method: "GET"},
	{Name: "beta", Title: "Beta Prices (demo, slow)", Class: "token.price", ListedMinor: 1_000, AskMinor: 1_000, Scheme: "exact", Method: "GET", Delay: 1200 * time.Millisecond},
	{Name: "flaky", Title: "Flaky Prices (demo, down)", Class: "token.price", ListedMinor: 500, AskMinor: 500, Scheme: "exact", Method: "GET", Down: true},
	{Name: "greedy", Title: "Greedy Prices (demo, overcharges)", Class: "token.price", ListedMinor: 1_000, AskMinor: 4_000, Scheme: "exact", Method: "GET"},
	{Name: "trap", Title: "Trap Prices (demo, honeypot)", Class: "token.price", ListedMinor: 25_000_000, AskMinor: 25_000_000, Scheme: "exact", Method: "GET"},
	{Name: "meter", Title: "Metered LLM (demo, x402 upto)", Class: "llm.chat", ListedMinor: 50_000, AskMinor: 50_000, Scheme: "upto", Method: "POST"},
}

func (p persona) endpoint() string {
	if p.Class == "llm.chat" {
		return "/" + p.Name + "/chat"
	}
	return "/" + p.Name + "/price"
}

func (p persona) path() string { return p.endpoint() }

// configured is the ECONOMIC_PROVIDERS entry for each persona.
func configured(base string) []map[string]any {
	out := []map[string]any{}
	for _, p := range personas {
		out = append(out, map[string]any{
			"capability": p.Class, "provider": "demo:" + p.Name, "name": p.Title, "endpoint": base + p.endpoint(),
			"method": p.Method, "network": "solana-devnet", "price_minor": p.ListedMinor,
		})
	}
	return out
}

type cached struct {
	body   []byte
	settle string
	at     time.Time
}

type server struct {
	rpc  *solana.RPC
	key  *solana.Keypair
	mint solana.PublicKey
	base string

	mu      sync.Mutex
	results map[string]cached // by Idempotency-Key
	busy    map[string]bool   // payment payloads being settled
}

func (s *server) listPersonas(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"providers": configured(s.base), "pay_to": s.key.PublicKey().String(), "network": "solana-devnet"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// requirements is the persona's one payment option, in x402 v2 form.
func (s *server) requirements(p persona) map[string]any {
	me := s.key.PublicKey().String()
	req := map[string]any{
		"scheme": p.Scheme, "network": devnetCAIP, "amount": strconv.FormatInt(p.AskMinor, 10), "asset": s.mint.String(),
		"payTo": me, "maxTimeoutSeconds": 120, "extra": map[string]any{"feePayer": me},
	}
	if p.Scheme == paychan.Scheme {
		req["maxTimeoutSeconds"] = 300
		req["extra"] = map[string]any{
			"paymentFlow": "escrow", "feePayer": me, "receiverAuthorizer": me, "withdrawDelay": 900,
			"tokenProgram": solana.TokenProgram.String(),
		}
	}
	return req
}

func (s *server) challenge(w http.ResponseWriter, p persona, reason string) {
	c := map[string]any{
		"x402Version": 2,
		"resource":    map[string]any{"url": s.base + p.endpoint(), "description": p.Title, "mimeType": "application/json"},
		"accepts":     []any{s.requirements(p)},
	}
	if reason != "" {
		c["error"] = reason
	}
	b, _ := json.Marshal(c)
	w.Header().Set(x402.HeaderPaymentRequiredV2, base64.StdEncoding.EncodeToString(b))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusPaymentRequired)
	_, _ = w.Write(b)
}

func (s *server) handle(p persona) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if p.Down {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "demo: this provider is down on purpose"})
			return
		}
		key := r.Header.Get("Idempotency-Key")
		header := r.Header.Get(x402.HeaderPaymentV2)
		if header == "" {
			header = r.Header.Get(x402.HeaderPayment)
		}
		if header == "" {
			if c, ok := s.cachedFor(key); ok {
				s.respond(w, c.body, c.settle, 0, "")
				return
			}
			s.challenge(w, p, "")
			return
		}
		input := readInput(r)
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		var settle string
		var charged int64
		var payer string
		var err error
		switch p.Scheme {
		case paychan.Scheme:
			settle, charged, payer, err = s.payUpto(ctx, p, header, input)
		default:
			settle, payer, err = s.payExact(ctx, p, header)
			charged = p.AskMinor
		}
		if err != nil {
			log.Printf("%s: payment refused: %v", p.Name, err)
			s.challenge(w, p, err.Error())
			return
		}
		time.Sleep(p.Delay)
		body := p.answer(input, charged)
		s.remember(key, cached{body: body, settle: settle, at: time.Now()})
		log.Printf("%s: paid %d by %s, settlement %s", p.Name, charged, payer, settle)
		s.respond(w, body, settle, charged, payer)
	}
}

func (s *server) respond(w http.ResponseWriter, body []byte, settle string, amount int64, payer string) {
	resp := map[string]any{"success": true, "transaction": settle, "network": devnetCAIP}
	if amount > 0 {
		resp["amount"] = strconv.FormatInt(amount, 10)
	}
	if payer != "" {
		resp["payer"] = payer
	}
	b, _ := json.Marshal(resp)
	w.Header().Set(x402.HeaderResponseV2, base64.StdEncoding.EncodeToString(b))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (s *server) cachedFor(key string) (cached, bool) {
	if key == "" {
		return cached{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.results[key]
	return c, ok
}

func (s *server) remember(key string, c cached) {
	if key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results[key] = c
}

// claim makes sure one payment is settled once, however often it is sent.
func (s *server) claim(payload string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy == nil {
		s.busy = map[string]bool{}
	}
	if s.busy[payload] {
		return false
	}
	s.busy[payload] = true
	return true
}

func readInput(r *http.Request) map[string]any {
	in := map[string]any{}
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			in[k] = v[0]
		}
	}
	if r.Body != nil {
		b, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		if len(bytes.TrimSpace(b)) > 0 {
			_ = json.Unmarshal(b, &in)
		}
	}
	return in
}

// answer is the persona's (demo) data.
func (p persona) answer(in map[string]any, charged int64) []byte {
	if p.Class == "llm.chat" {
		prompt, _ := in["prompt"].(string)
		return mustBytes(map[string]any{
			"demo": true, "provider": "demo:" + p.Name, "model": "demo-echo-1",
			"text":          "demo answer to: " + truncate(prompt, 200),
			"charged_minor": charged, "ceiling_minor": p.AskMinor,
		})
	}
	mint, _ := in["mint"].(string)
	price := "1.0000"
	if mint != "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v" && mint != "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU" {
		price = fmt.Sprintf("%d.%02d", 100+len(mint)%50, len(mint)%100)
	}
	return mustBytes(map[string]any{"demo": true, "provider": "demo:" + p.Name, "mint": mint, "price_usd": price, "as_of": time.Now().UTC().Format(time.RFC3339)})
}

func mustBytes(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// payExact checks an x402 exact payment, co-signs it as fee payer and
// submits it to devnet.
func (s *server) payExact(ctx context.Context, p persona, header string) (string, string, error) {
	pay, err := x402.DecodePayment(header)
	if err != nil {
		return "", "", err
	}
	var inner struct {
		Transaction string `json:"transaction"`
	}
	if err := json.Unmarshal(pay.Payload, &inner); err != nil || inner.Transaction == "" {
		return "", "", errors.New("the payload carries no transaction")
	}
	tx, err := solana.DecodeTransactionBase64(inner.Transaction)
	if err != nil {
		return "", "", err
	}
	payer, err := s.verifyExact(tx, uint64(p.AskMinor))
	if err != nil {
		return "", "", err
	}
	if !s.claim(inner.Transaction) {
		return "", "", errors.New("this payment is already being settled")
	}
	sig, err := s.cosignAndSend(ctx, tx)
	return sig, payer.String(), err
}

// verifyExact is the facilitator's check of an exact payment: a transfer of
// exactly the price, in devnet USDC, to this provider's USDC account, with this
// provider as fee payer and in no instruction's accounts.
func (s *server) verifyExact(tx *solana.Transaction, amount uint64) (solana.PublicKey, error) {
	m := tx.Message
	me := s.key.PublicKey()
	if len(m.AccountKeys) == 0 || m.AccountKeys[0] != me {
		return solana.PublicKey{}, errors.New("the fee payer is not this provider")
	}
	if m.NumRequiredSignatures != 2 {
		return solana.PublicKey{}, errors.New("the payment must need exactly the payer's and the sponsor's signatures")
	}
	want, err := solana.AssociatedTokenAddress(me, s.mint, solana.TokenProgram)
	if err != nil {
		return solana.PublicKey{}, err
	}
	var payer solana.PublicKey
	transfers := 0
	for _, ci := range m.Instructions {
		prog := m.ProgramOf(ci)
		accts := m.InstructionAccounts(ci)
		for _, a := range accts {
			if a == me {
				return solana.PublicKey{}, errors.New("the fee payer appears in an instruction")
			}
		}
		switch prog {
		case solana.ComputeBudgetProgram, solana.MemoProgram:
		case solana.TokenProgram:
			if len(ci.Data) != 10 || ci.Data[0] != 12 || len(accts) != 4 {
				return solana.PublicKey{}, errors.New("not a TransferChecked")
			}
			var got uint64
			for i := 7; i >= 0; i-- {
				got = got<<8 | uint64(ci.Data[1+i])
			}
			if accts[1] != s.mint || accts[2] != want || got != amount {
				return solana.PublicKey{}, fmt.Errorf("the transfer must be exactly %d devnet USDC to %s", amount, want)
			}
			payer = accts[3]
			transfers++
		default:
			return solana.PublicKey{}, fmt.Errorf("unexpected program %s", prog)
		}
	}
	if transfers != 1 || !tx.VerifySignature(payer) {
		return solana.PublicKey{}, errors.New("the payment must hold one transfer signed by its payer")
	}
	return payer, nil
}

func (s *server) cosignAndSend(ctx context.Context, tx *solana.Transaction) (string, error) {
	if err := tx.PartialSign(s.key); err != nil {
		return "", err
	}
	sig, err := s.rpc.SendTransaction(ctx, tx.Base64(), solana.SendOptions{})
	if err != nil {
		return "", fmt.Errorf("submitting: %w", err)
	}
	if _, err := s.rpc.ConfirmSignature(ctx, sig, solana.Confirmed, 0); err != nil {
		return "", err
	}
	return sig, nil
}

// payUpto takes an x402 upto authorization: it checks and submits the
// client's channel open, serves the call, then settles what it actually cost
// from its own voucher and refunds the rest in the same transaction.
func (s *server) payUpto(ctx context.Context, p persona, header string, in map[string]any) (string, int64, string, error) {
	pay, err := x402.DecodePayment(header)
	if err != nil {
		return "", 0, "", err
	}
	var payload paychan.UptoPayload
	if err := json.Unmarshal(pay.Payload, &payload); err != nil {
		return "", 0, "", errors.New("the payload isn't an upto authorization")
	}
	reqBytes, _ := json.Marshal(s.requirements(p))
	var reqs x402.Requirements
	_ = json.Unmarshal(reqBytes, &reqs)
	terms, err := paychan.ParseUptoTerms(reqs)
	if err != nil {
		return "", 0, "", err
	}
	tx, ch, err := paychan.VerifyUptoOpen(terms, payload)
	if err != nil {
		return "", 0, "", err
	}
	if info, err := s.rpc.GetAccountInfo(ctx, ch, solana.Confirmed); err != nil {
		return "", 0, "", err
	} else if info != nil {
		return "", 0, "", errors.New("that channel is already open; an upto authorization is single-use")
	}
	if !s.claim(payload.OpenTransaction) {
		return "", 0, "", errors.New("this authorization is already being settled")
	}
	openSig, err := s.cosignAndSend(ctx, tx)
	if err != nil {
		return "", 0, "", fmt.Errorf("opening the channel: %w", err)
	}
	log.Printf("%s: channel %s opened (%s), ceiling %d", p.Name, ch, openSig, terms.MaxAmount)

	// The work's real cost: a base fee plus a little per byte of prompt.
	prompt, _ := in["prompt"].(string)
	actual := min(uint64(2_000+40*len(prompt)), terms.MaxAmount)
	from, _ := solana.ParsePublicKey(payload.From)
	v := paychan.SignVoucher(s.key, ch, actual, time.Unix(payload.ExpiresAt, 0))
	bh, err := s.rpc.GetLatestBlockhash(ctx, solana.Confirmed)
	if err != nil {
		return "", 0, "", err
	}
	msg, err := paychan.SettleUptoTx("solana-devnet", terms, from, ch, &v, bh.Blockhash)
	if err != nil {
		return "", 0, "", err
	}
	stx := solana.NewTransaction(msg)
	if err := stx.PartialSign(s.key); err != nil {
		return "", 0, "", err
	}
	sig, err := s.rpc.SendTransaction(ctx, stx.Base64(), solana.SendOptions{})
	if err != nil {
		return "", 0, "", fmt.Errorf("settling the channel: %w", err)
	}
	if _, err := s.rpc.ConfirmSignature(ctx, sig, solana.Confirmed, 0); err != nil {
		return "", 0, "", err
	}

	return sig, int64(actual), payload.From, nil
}
