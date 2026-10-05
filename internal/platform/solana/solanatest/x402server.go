package solanatest

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/project-algebra/algebra/internal/platform/solana"
	"github.com/project-algebra/algebra/providers/x402"
)

// X402Server is a paid API and the facilitator behind it, for tests. Its
// verification follows the x402 "exact" scheme for Solana as the spec states
// it, written separately from the code that builds payments, so a payment is
// checked by something that can disagree with the builder:
//
//   - a versioned transaction, 3 to 7 instructions, in the order compute
//     limit, compute price, TransferChecked, then only memo (or Lighthouse);
//   - the compute price at most 5 lamports per unit;
//   - the sponsor as fee payer, in no instruction's accounts, and neither the
//     source nor the authority of the transfer;
//   - the destination the associated token account of (payTo, mint), the
//     mint the asset, the amount exact;
//   - exactly one memo, equal to the seller's memo when one is set, otherwise
//     a hex nonce of at least 16 bytes;
//   - no signature required beyond the payer and the sponsor.
//
// It then settles the way a sponsor does, by adding its signature and
// submitting, which on the fake chain is Chain.Land.
type X402Server struct {
	Chain   *Chain
	Sponsor *solana.Keypair
	PayTo   solana.PublicKey
	Mint    solana.PublicKey
	// Network is what the provider calls the network in its requirements.
	Network string
	Version int
	Amount  uint64
	// Memo, when set, is the seller's extra.memo.
	Memo string
	// Body is the paid response.
	Body string

	// Faults.
	// DropResponse settles and then answers 504, as a provider whose response
	// got lost does.
	DropResponse bool
	// RejectPayment answers 402 without settling, as a facilitator that
	// fails verification does.
	RejectPayment bool
	// NoReplay turns off idempotent replay.
	NoReplay bool

	mu       sync.Mutex
	stored   map[string]stored
	settling map[string]bool
	// Settled lists the signatures of the transactions it submitted.
	Settled []string
	// Verified counts payments that passed verification.
	Verified int
	// Requests counts every call; PaidRequests those that carried a payment.
	Requests     int
	PaidRequests int
}

type stored struct {
	sig  string
	body string
}

// Handler returns the server as an http.Handler.
func (p *X402Server) Handler() http.Handler { return http.HandlerFunc(p.serve) }

func (p *X402Server) challenge() map[string]any {
	key := "maxAmountRequired"
	if p.Version >= 2 {
		key = "amount"
	}
	extra := map[string]any{"feePayer": p.Sponsor.PublicKey().String()}
	if p.Memo != "" {
		extra["memo"] = p.Memo
	}
	opt := map[string]any{
		"scheme": "exact", "network": p.Network, key: fmt.Sprint(p.Amount), "asset": p.Mint.String(), "payTo": p.PayTo.String(),
		"maxTimeoutSeconds": 60, "extra": extra,
	}
	ch := map[string]any{"x402Version": p.Version, "error": "payment required", "accepts": []any{opt}}
	if p.Version >= 2 {
		ch["resource"] = map[string]any{"url": "https://api.example.com/risk", "mimeType": "application/json"}
	}
	return ch
}

func (p *X402Server) serve(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	p.mu.Lock()
	p.Requests++
	if p.stored == nil {
		p.stored, p.settling = map[string]stored{}, map[string]bool{}
	}
	key := r.Header.Get("Idempotency-Key")
	prior, replay := p.stored[key]
	p.mu.Unlock()

	// A provider that remembers results replays one for a repeated key,
	// without a payment, however it was asked.
	if replay && key != "" && !p.NoReplay {
		p.respondPaid(w, prior.sig, prior.body)
		return
	}
	header := r.Header.Get(x402.HeaderPaymentV2)
	if header == "" {
		header = r.Header.Get(x402.HeaderPayment)
	}
	if header == "" {
		p.respond402(w, "payment required")
		return
	}
	p.mu.Lock()
	p.PaidRequests++
	p.mu.Unlock()
	if p.RejectPayment {
		p.respond402(w, "payment could not be verified")
		return
	}
	sig, err := p.verifyAndSettle(header)
	if err != nil {
		p.respond402(w, err.Error())
		return
	}
	body := p.Body
	if body == "" {
		body = `{"mint":"SOL","risk_score":12}`
	}
	p.mu.Lock()
	if key != "" {
		p.stored[key] = stored{sig: sig, body: body}
	}
	p.mu.Unlock()
	if p.DropResponse {
		http.Error(w, "upstream timed out", http.StatusGatewayTimeout)
		return
	}
	p.respondPaid(w, sig, body)
}

func (p *X402Server) respond402(w http.ResponseWriter, reason string) {
	ch := p.challenge()
	ch["error"] = reason
	b, _ := json.Marshal(ch)
	if p.Version >= 2 {
		w.Header().Set(x402.HeaderPaymentRequiredV2, base64.StdEncoding.EncodeToString(b))
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusPaymentRequired)
	_, _ = w.Write(b)
}

func (p *X402Server) respondPaid(w http.ResponseWriter, sig, body string) {
	settle := x402.SettleResponse{Success: true, Transaction: sig, Network: p.Network, Payer: p.Sponsor.PublicKey().String()}
	h := x402.HeaderPaymentResponse
	if p.Version >= 2 {
		h = x402.HeaderResponseV2
	}
	w.Header().Set(h, settle.Encode())
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

// verifyAndSettle checks a payment against the spec and, if it passes,
// submits it.
func (p *X402Server) verifyAndSettle(header string) (string, error) {
	pay, err := x402.DecodePayment(header)
	if err != nil {
		return "", err
	}
	if pay.Version != p.Version {
		return "", fmt.Errorf("payment is x402 version %d, this resource speaks %d", pay.Version, p.Version)
	}
	if p.Version >= 2 && len(pay.Accepted) == 0 {
		return "", errors.New("a v2 payment must echo the option it accepted")
	}
	var inner struct {
		Transaction string `json:"transaction"`
	}
	if err := json.Unmarshal(pay.Payload, &inner); err != nil || inner.Transaction == "" {
		return "", errors.New("the payload carries no transaction")
	}
	raw, err := base64.StdEncoding.DecodeString(inner.Transaction)
	if err != nil {
		return "", errors.New("the transaction isn't base64")
	}
	tx, err := solana.DecodeTransaction(raw)
	if err != nil {
		return "", err
	}
	if err := p.verify(tx, raw); err != nil {
		return "", err
	}
	// Duplicate settlement mitigation: the same transaction is submitted once.
	p.mu.Lock()
	if p.settling[inner.Transaction] {
		p.mu.Unlock()
		return "", errors.New("this transaction is already being settled")
	}
	p.settling[inner.Transaction] = true
	p.Verified++
	p.mu.Unlock()

	sig, err := p.Chain.Land(inner.Transaction, p.Sponsor)
	if err != nil {
		return "", fmt.Errorf("settlement failed: %w", err)
	}
	p.mu.Lock()
	p.Settled = append(p.Settled, sig)
	p.mu.Unlock()
	return sig, nil
}

func (p *X402Server) verify(tx *solana.Transaction, raw []byte) error {
	// A versioned transaction: the byte after the signatures has its high bit set.
	if nsig := len(tx.Signatures); 1+64*nsig >= len(raw) || raw[1+64*nsig]&0x80 == 0 {
		return errors.New("the transaction must be versioned")
	}
	m := tx.Message
	n := len(m.Instructions)
	if n < 3 || n > 7 {
		return fmt.Errorf("expected 3 to 7 instructions, found %d", n)
	}
	// 1. compute unit limit, 2. compute unit price
	for i, disc := range []byte{2, 3} {
		in := m.Instructions[i]
		if m.ProgramOf(in) != solana.ComputeBudgetProgram || len(in.Data) == 0 || in.Data[0] != disc {
			return fmt.Errorf("instruction %d must be a compute budget instruction with discriminator %d", i, disc)
		}
	}
	price := uint64(0)
	for i, b := range m.Instructions[1].Data[1:] {
		price |= uint64(b) << (8 * i)
	}
	if price > 5_000_000 { // 5 lamports per compute unit, in micro-lamports
		return fmt.Errorf("compute unit price %d micro-lamports is above the cap", price)
	}
	// 3. TransferChecked
	tc := m.Instructions[2]
	if prog := m.ProgramOf(tc); prog != solana.TokenProgram && prog != solana.Token2022Program {
		return errors.New("instruction 2 must be a token TransferChecked")
	}
	if len(tc.Data) != 10 || tc.Data[0] != 12 || len(tc.AccountIndexes) != 4 {
		return errors.New("instruction 2 is not a well-formed TransferChecked")
	}
	acct := m.InstructionAccounts(tc)
	source, mint, dest, authority := acct[0], acct[1], acct[2], acct[3]
	amount := uint64(0)
	for i := 7; i >= 0; i-- {
		amount = amount<<8 | uint64(tc.Data[1+i])
	}
	want, err := solana.AssociatedTokenAddress(p.PayTo, p.Mint, solana.TokenProgram)
	if err != nil {
		return err
	}
	switch {
	case mint != p.Mint:
		return errors.New("the transfer is not in the required asset")
	case dest != want:
		return errors.New("the destination is not the associated token account of payTo for the mint")
	case amount != p.Amount:
		return fmt.Errorf("the amount is %d, the requirement is %d", amount, p.Amount)
	}
	// Optional instructions after the transfer: memo or Lighthouse only.
	memos := []string{}
	lighthouse := solana.MustPublicKey("L2TExMFKdjpN9kozasaurPirfHy9P8sbXoAN1qA3S95")
	for _, in := range m.Instructions[3:] {
		switch m.ProgramOf(in) {
		case solana.MemoProgram:
			memos = append(memos, string(in.Data))
		case lighthouse:
		default:
			return errors.New("only memo and Lighthouse instructions may follow the transfer")
		}
	}
	if len(memos) != 1 {
		return fmt.Errorf("exactly one memo instruction is required, found %d", len(memos))
	}
	if p.Memo != "" {
		if memos[0] != p.Memo {
			return errors.New("the memo does not match the seller's memo")
		}
	} else if b, err := hex.DecodeString(memos[0]); err != nil || len(b) < 16 {
		return errors.New("without a seller memo the memo must be a hex nonce of at least 16 bytes")
	}
	// Fee payer safety.
	fee := m.AccountKeys[0]
	if fee != p.Sponsor.PublicKey() {
		return errors.New("the fee payer is not this sponsor")
	}
	if fee == source || fee == authority {
		return errors.New("the fee payer must not be the source or the authority")
	}
	for _, in := range m.Instructions {
		if m.ProgramOf(in) == fee {
			return errors.New("the fee payer must not be invoked as a program")
		}
		for _, a := range m.InstructionAccounts(in) {
			if a == fee {
				return errors.New("the fee payer must not appear in any instruction's accounts")
			}
		}
	}
	// Signatures: the payer's must be valid, and nothing beyond payer and sponsor is required.
	if m.NumRequiredSignatures != 2 || !tx.VerifySignature(authority) {
		return errors.New("the transaction must need exactly the payer's and the sponsor's signatures, and the payer's must verify")
	}
	// What the sponsor's simulation would catch.
	if bal, ok := p.Chain.TokenBalance(source); !ok || bal < amount {
		return errors.New("simulation: the payer cannot cover the transfer")
	}
	if _, ok := p.Chain.TokenBalance(dest); !ok {
		return errors.New("simulation: the destination token account does not exist")
	}
	return nil
}

// Mux mounts the server at a path, so one httptest server can host several.
func (p *X402Server) Mux(mux *http.ServeMux, path string) {
	mux.Handle(path, p.Handler())
}
