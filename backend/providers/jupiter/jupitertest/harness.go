// Package jupitertest is a Jupiter and a Solana node that agree with each other,
// for testing a swap end to end without a network: Jupiter quotes and builds
// transactions, the node simulates them against a wallet's accounts and lands
// them when asked, and both read and write one chain state.
//
// It can also misbehave the way a hostile or broken Jupiter could, so tests can
// show that the rail refuses what it should.
package jupitertest

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/project-algebra/algebra/internal/platform/solana"
)

// Behavior is what the transactions Jupiter builds actually do.
type Behavior int

const (
	// Honest: spends the input, delivers the output.
	Honest Behavior = iota
	// Drain: spends three times the input.
	Drain
	// Delegate: spends the input and also approves a stranger to spend the rest.
	Delegate
	// Underdeliver: spends the input and delivers 90% of the quote.
	Underdeliver
	// Fail: the transaction would fail on chain.
	Fail
)

// USDC is Circle's mainnet USDC, the mint a swap spends.
var USDC = solana.MustPublicKey("EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v")

// Stranger is somebody else's address.
var Stranger = solana.MustPublicKey("9xQeWvG816bUx9EPjHmaT23yvVM2ZWbrrpZb9PusVFin")

// Harness is the pair of servers and the chain they share.
type Harness struct {
	// RPCURL and APIURL are where the node and Jupiter listen.
	RPCURL, APIURL string
	Wallet, Bought solana.PublicKey
	// Mode is what the next transactions do. Set it before asking for an order.
	Mode Behavior

	mu       sync.Mutex
	accounts map[solana.PublicKey]*solana.AccountInfo
	orders   map[string]*order // by request id
	byHash   map[[32]byte]*order
	status   map[string]*solana.SignatureStatus
	balances map[string][]balance
	height   uint64
	// Orders, Simulations and Landed count what happened.
	Orders, Simulations, Landed int
}

type order struct {
	id       string
	in, out  uint64
	mode     Behavior
	lastBlk  uint64
	blockhsh [32]byte
}

type balance struct {
	account, mint, owner string
	pre, post            uint64
}

// New starts the pair for a wallet holding usdc micro-USDC and 1 SOL, and a
// token that can be bought.
func New(t testing.TB, wallet, bought solana.PublicKey, usdc uint64) *Harness {
	h := &Harness{
		Wallet: wallet, Bought: bought, accounts: map[solana.PublicKey]*solana.AccountInfo{}, orders: map[string]*order{},
		byHash: map[[32]byte]*order{}, status: map[string]*solana.SignatureStatus{}, balances: map[string][]balance{}, height: 1_000,
	}
	h.accounts[wallet] = &solana.AccountInfo{Lamports: 1_000_000_000, Owner: solana.SystemProgram}
	h.accounts[h.USDCAccount()] = tokenAccount(USDC, wallet, usdc)
	h.accounts[bought] = &solana.AccountInfo{Lamports: 1_461_600, Owner: solana.TokenProgram, Data: make([]byte, 82)}
	rpc := httptest.NewServer(http.HandlerFunc(h.serveRPC))
	api := httptest.NewServer(http.HandlerFunc(h.serveAPI))
	t.Cleanup(rpc.Close)
	t.Cleanup(api.Close)
	h.RPCURL, h.APIURL = rpc.URL, api.URL
	return h
}

// USDCAccount is the wallet's USDC token account.
func (h *Harness) USDCAccount() solana.PublicKey {
	a, _ := solana.AssociatedTokenAddress(h.Wallet, USDC, solana.TokenProgram)
	return a
}

// BoughtAccount is the wallet's account for the token that can be bought.
func (h *Harness) BoughtAccount() solana.PublicKey {
	a, _ := solana.AssociatedTokenAddress(h.Wallet, h.Bought, solana.TokenProgram)
	return a
}

// USDCBalance is what the wallet holds, in micro-USDC.
func (h *Harness) USDCBalance() uint64 { return h.tokenBalance(h.USDCAccount()) }

// BoughtBalance is what the wallet holds of the bought token.
func (h *Harness) BoughtBalance() uint64 { return h.tokenBalance(h.BoughtAccount()) }

func (h *Harness) tokenBalance(pk solana.PublicKey) uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if a := h.accounts[pk]; a != nil {
		if ta, err := solana.ParseTokenAccount(a.Data); err == nil {
			return ta.Amount
		}
	}
	return 0
}

// Counts returns how many orders were built, transactions simulated and swaps landed.
func (h *Harness) Counts() (orders, simulations, landed int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.Orders, h.Simulations, h.Landed
}

func tokenAccount(mint, owner solana.PublicKey, amount uint64) *solana.AccountInfo {
	ta := &solana.TokenAccount{Mint: mint, Owner: owner, Amount: amount, State: solana.TokenAccountInitialized}
	return &solana.AccountInfo{Lamports: 2_039_280, Owner: solana.TokenProgram, Data: ta.Encode()}
}

// rate is how much of the bought token a micro-USDC buys: 0.4, a little worse
// above $3, as a pool's would be.
func rate(in uint64) uint64 {
	if in > 3_000_000 {
		return in * 36 / 100
	}
	return in * 4 / 10
}

// --- Jupiter ---

func (h *Harness) serveAPI(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/order":
		q := r.URL.Query()
		in, _ := strconv.ParseUint(q.Get("amount"), 10, 64)
		out := rate(in)
		o := map[string]any{"inAmount": q.Get("amount"), "outAmount": fmt.Sprint(out), "router": "metis", "transactionVersion": 0, "transaction": nil}
		if q.Get("taker") != "" {
			h.Orders++
			ord := &order{id: fmt.Sprintf("req-%d", h.Orders), in: in, out: out, mode: h.Mode, lastBlk: h.height + 150}
			ord.blockhsh = sha256.Sum256([]byte(ord.id))
			h.orders[ord.id] = ord
			h.byHash[ord.blockhsh] = ord
			o["requestId"], o["lastValidBlockHeight"] = ord.id, ord.lastBlk
			o["transaction"] = h.buildTx(ord)
		}
		_ = json.NewEncoder(w).Encode(o)
	case "/execute":
		var in struct {
			SignedTransaction string `json:"signedTransaction"`
			RequestID         string `json:"requestId"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		ord := h.orders[in.RequestID]
		raw, err := base64.StdEncoding.DecodeString(in.SignedTransaction)
		if ord == nil || err != nil || len(raw) < 65 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "Failed", "code": -2, "error": "invalid signed transaction"})
			return
		}
		sig := solana.EncodeBase58(raw[1:65])
		h.apply(ord, sig)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "Success", "signature": sig, "code": 0, "totalInputAmount": fmt.Sprint(ord.in), "inputAmountResult": fmt.Sprint(ord.in),
			"outputAmountResult": fmt.Sprint(ord.out), "totalOutputAmount": fmt.Sprint(ord.out),
		})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// buildTx is a v0 transaction with the wallet as its only signer and fee payer,
// an address table lookup, and the order's id in its blockhash so the node can
// tell which swap it is.
func (h *Harness) buildTx(o *order) string {
	keys := []solana.PublicKey{h.Wallet, solana.TokenProgram, solana.MustPublicKey("JUP6LkbZbjS1jKKwapdHNy74zcZ3tLUZoi5QNyVTaV4")}
	raw := []byte{1}
	raw = append(raw, make([]byte, 64)...)
	raw = append(raw, 0x80, 1, 0, 2, byte(len(keys)))
	for _, k := range keys {
		raw = append(raw, k[:]...)
	}
	raw = append(raw, o.blockhsh[:]...)
	raw = append(raw, 1, 2, 1, 0, 3, 1, 2, 3)
	raw = append(raw, 1)
	raw = append(raw, bytes.Repeat([]byte{9}, 32)...)
	raw = append(raw, 1, 5, 0)
	return base64.StdEncoding.EncodeToString(raw)
}

// outcome is the chain after a swap, without changing it.
func (h *Harness) outcome(o *order) (map[solana.PublicKey]*solana.AccountInfo, bool) {
	after := map[solana.PublicKey]*solana.AccountInfo{}
	for k, v := range h.accounts {
		c := *v
		after[k] = &c
	}
	usdcAcct, boughtAcct := h.USDCAccount(), h.BoughtAccount()
	spend := o.in
	deliver := o.out
	switch o.mode {
	case Drain:
		spend = o.in * 3
	case Underdeliver:
		deliver = o.out * 9 / 10
	case Fail:
		return nil, false
	}
	ua, _ := solana.ParseTokenAccount(after[usdcAcct].Data)
	if ua.Amount < spend {
		return nil, false
	}
	ua.Amount -= spend
	if o.mode == Delegate {
		d := Stranger
		ua.Delegate, ua.DelegatedAmount = &d, 1<<62
	}
	after[usdcAcct] = &solana.AccountInfo{Lamports: after[usdcAcct].Lamports, Owner: solana.TokenProgram, Data: ua.Encode()}
	if b := after[boughtAcct]; b != nil {
		ba, _ := solana.ParseTokenAccount(b.Data)
		ba.Amount += deliver
		after[boughtAcct] = &solana.AccountInfo{Lamports: b.Lamports, Owner: solana.TokenProgram, Data: ba.Encode()}
	} else {
		after[boughtAcct] = tokenAccount(h.Bought, h.Wallet, deliver)
	}
	w := *after[h.Wallet]
	w.Lamports -= 2_100_000 // the fee and the new token account's rent
	after[h.Wallet] = &w
	return after, true
}

// apply lands a swap: the chain changes, and the signature is on record.
func (h *Harness) apply(o *order, sig string) {
	pre := map[solana.PublicKey]uint64{h.USDCAccount(): h.amount(h.USDCAccount()), h.BoughtAccount(): h.amount(h.BoughtAccount())}
	after, ok := h.outcome(o)
	if !ok {
		return
	}
	h.accounts = after
	h.Landed++
	h.status[sig] = &solana.SignatureStatus{ConfirmationStatus: "finalized", Slot: h.height}
	h.balances[sig] = []balance{
		{h.USDCAccount().String(), USDC.String(), h.Wallet.String(), pre[h.USDCAccount()], h.amount(h.USDCAccount())},
		{h.BoughtAccount().String(), h.Bought.String(), h.Wallet.String(), pre[h.BoughtAccount()], h.amount(h.BoughtAccount())},
	}
}

func (h *Harness) amount(pk solana.PublicKey) uint64 {
	if a := h.accounts[pk]; a != nil {
		if ta, err := solana.ParseTokenAccount(a.Data); err == nil {
			return ta.Amount
		}
	}
	return 0
}

// --- the node ---

func jsonAccount(a *solana.AccountInfo) any {
	if a == nil {
		return nil
	}
	return map[string]any{"lamports": a.Lamports, "owner": a.Owner.String(), "data": []string{base64.StdEncoding.EncodeToString(a.Data), "base64"}, "executable": a.Executable}
}

func (h *Harness) serveRPC(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     int64             `json:"id"`
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	h.mu.Lock()
	defer h.mu.Unlock()
	var result any
	switch req.Method {
	case "getAccountInfo":
		var key string
		_ = json.Unmarshal(req.Params[0], &key)
		pk, _ := solana.ParsePublicKey(key)
		result = map[string]any{"value": jsonAccount(h.accounts[pk])}
	case "getMultipleAccounts":
		var keys []string
		_ = json.Unmarshal(req.Params[0], &keys)
		out := make([]any, len(keys))
		for i, k := range keys {
			pk, _ := solana.ParsePublicKey(k)
			out[i] = jsonAccount(h.accounts[pk])
		}
		result = map[string]any{"value": out}
	case "getTokenAccountsByOwner":
		var owner string
		var filter struct {
			ProgramID string `json:"programId"`
		}
		_ = json.Unmarshal(req.Params[0], &owner)
		_ = json.Unmarshal(req.Params[1], &filter)
		var list []any
		for pk, a := range h.accounts {
			if a.Owner.String() != filter.ProgramID || len(a.Data) < solana.TokenAccountSize {
				continue
			}
			if ta, err := solana.ParseTokenAccount(a.Data); err == nil && ta.Owner.String() == owner {
				list = append(list, map[string]any{"pubkey": pk.String(), "account": jsonAccount(a)})
			}
		}
		result = map[string]any{"value": list}
	case "simulateTransaction":
		h.Simulations++
		var tx string
		var opts struct {
			Accounts struct {
				Addresses []string `json:"addresses"`
			} `json:"accounts"`
		}
		_ = json.Unmarshal(req.Params[0], &tx)
		_ = json.Unmarshal(req.Params[1], &opts)
		value := map[string]any{"err": nil, "logs": []string{}, "unitsConsumed": 200_000}
		raw, _ := base64.StdEncoding.DecodeString(tx)
		var ord *order
		for hash, o := range h.byHash {
			if bytes.Contains(raw, hash[:]) {
				ord = o
			}
		}
		var after map[solana.PublicKey]*solana.AccountInfo
		ok := false
		if ord != nil {
			after, ok = h.outcome(ord)
		}
		if !ok {
			value["err"] = map[string]any{"InstructionError": []any{2, "Custom"}}
		} else {
			out := make([]any, len(opts.Accounts.Addresses))
			for i, k := range opts.Accounts.Addresses {
				pk, _ := solana.ParsePublicKey(k)
				out[i] = jsonAccount(after[pk])
			}
			value["accounts"] = out
		}
		result = map[string]any{"value": value}
	case "getSignatureStatuses":
		var sigs []string
		_ = json.Unmarshal(req.Params[0], &sigs)
		out := make([]any, len(sigs))
		for i, s := range sigs {
			if st := h.status[s]; st != nil {
				out[i] = st
			}
		}
		result = map[string]any{"value": out}
	case "getBlockHeight":
		result = h.height
	case "getTransaction":
		var sig string
		_ = json.Unmarshal(req.Params[0], &sig)
		bs, ok := h.balances[sig]
		if !ok {
			break
		}
		keys := []any{}
		var pre, post []any
		for i, b := range bs {
			keys = append(keys, map[string]any{"pubkey": b.account})
			tok := func(amount uint64) any {
				return map[string]any{"accountIndex": i, "mint": b.mint, "owner": b.owner, "uiTokenAmount": map[string]any{"amount": fmt.Sprint(amount)}}
			}
			pre, post = append(pre, tok(b.pre)), append(post, tok(b.post))
		}
		result = map[string]any{"slot": h.height, "meta": map[string]any{"err": nil, "preTokenBalances": pre, "postTokenBalances": post},
			"transaction": map[string]any{"signatures": []string{sig}, "message": map[string]any{"accountKeys": keys}}}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
}
