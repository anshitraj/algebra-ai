// Package solanatest is an in-memory Solana node for tests: it answers the
// JSON-RPC calls Algebra makes, holds token balances, and can "land" a
// partially signed transaction the way an x402 sponsor submits one. It is a
// test double, not a validator: it decodes what it is given and applies the
// token transfers in it, nothing more.
package solanatest

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/platform/solana"
)

// Chain is a fake cluster.
type Chain struct {
	mu sync.Mutex

	GenesisHash string
	Height      uint64
	// Slot advances with Height; blocks are 1:1 with slots here.
	blockhash [32]byte

	lamports map[solana.PublicKey]uint64
	tokens   map[solana.PublicKey]uint64 // token account -> amount
	txs      map[string]*landed
	history  map[solana.PublicKey][]string // address -> signatures, newest first
	calls    map[string]int

	// FailMethod makes a method return this error (rate limits, outages).
	FailMethod map[string]error
	// FinalizedLag is how many blocks behind the tip "finalized" is.
	FinalizedLag uint64
	// Clock stamps the block time of every transaction that lands.
	Clock func() time.Time
	// HideFromFinalized: signatures the node reports only at confirmed level.
	hidden map[string]bool
}

type landed struct {
	sigs      []string
	slot      uint64
	blockTime int64
	err       any
	instrs    []map[string]any
}

// New starts a chain on the given genesis hash.
func New(genesis string) *Chain {
	c := &Chain{
		GenesisHash: genesis, Height: 1_000, FinalizedLag: 32,
		lamports: map[solana.PublicKey]uint64{}, tokens: map[solana.PublicKey]uint64{},
		txs: map[string]*landed{}, history: map[solana.PublicKey][]string{}, calls: map[string]int{},
		FailMethod: map[string]error{}, hidden: map[string]bool{}, Clock: time.Now,
	}
	_, _ = rand.Read(c.blockhash[:])
	return c
}

// Serve starts an HTTP server for the chain and returns its URL.
func (c *Chain) Serve(t testing.TB) string {
	srv := httptest.NewServer(c)
	t.Cleanup(srv.Close)
	return srv.URL
}

// Fund gives an account lamports.
func (c *Chain) Fund(pk solana.PublicKey, lamports uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lamports[pk] += lamports
}

// SetTokenAccount creates a token account holding amount.
func (c *Chain) SetTokenAccount(pk solana.PublicKey, amount uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tokens[pk] = amount
}

// TokenBalance reads a token account.
func (c *Chain) TokenBalance(pk solana.PublicKey) (uint64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.tokens[pk]
	return v, ok
}

// Advance moves the chain forward by n blocks.
func (c *Chain) Advance(n uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Height += n
	_, _ = rand.Read(c.blockhash[:])
}

// Calls is how many times a method was called.
func (c *Chain) Calls(method string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[method]
}

// Land does what an x402 sponsor does: it adds its signature as fee payer to a
// partially signed transaction and submits it. The transfers in it take
// effect, and the transaction appears in the history of every account it
// touches. It returns the transaction's signature, which is the fee payer's.
func (c *Chain) Land(txBase64 string, sponsor *solana.Keypair) (string, error) {
	return c.land(txBase64, sponsor, false)
}

// LandFailed lands the transaction but makes it fail on chain: no transfer,
// the failure recorded.
func (c *Chain) LandFailed(txBase64 string, sponsor *solana.Keypair) (string, error) {
	return c.land(txBase64, sponsor, true)
}

func (c *Chain) land(txBase64 string, sponsor *solana.Keypair, fail bool) (string, error) {
	tx, err := solana.DecodeTransactionBase64(txBase64)
	if err != nil {
		return "", err
	}
	if tx.Message.AccountKeys[0] != sponsor.PublicKey() {
		return "", errors.New("solanatest: the sponsor isn't the fee payer")
	}
	for i := 1; i < int(tx.Message.NumRequiredSignatures); i++ {
		if !tx.VerifySignature(tx.Message.AccountKeys[i]) {
			return "", errors.New("solanatest: a required signature is missing or wrong")
		}
	}
	if err := tx.PartialSign(sponsor); err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	rec := &landed{slot: c.Height, blockTime: c.Clock().Unix()}
	for _, s := range tx.Signatures {
		rec.sigs = append(rec.sigs, solana.EncodeBase58(s[:]))
	}
	for _, in := range tx.Message.Instructions {
		prog := tx.Message.ProgramOf(in)
		accts := tx.Message.InstructionAccounts(in)
		switch {
		case prog == solana.TokenProgram && len(in.Data) == 10 && in.Data[0] == 12:
			amount := uint64(0)
			for i := 8; i >= 1; i-- {
				amount = amount<<8 | uint64(in.Data[i])
			}
			decimals := in.Data[9]
			rec.instrs = append(rec.instrs, map[string]any{
				"program": "spl-token", "programId": prog.String(),
				"parsed": map[string]any{"type": "transferChecked", "info": map[string]any{
					"source": accts[0].String(), "mint": accts[1].String(), "destination": accts[2].String(), "authority": accts[3].String(),
					"tokenAmount": map[string]any{"amount": strconv.FormatUint(amount, 10), "decimals": decimals},
				}},
			})
			if !fail {
				if c.tokens[accts[0]] < amount {
					return "", errors.New("solanatest: insufficient token balance")
				}
				c.tokens[accts[0]] -= amount
				c.tokens[accts[2]] += amount
			}
		case prog == solana.MemoProgram:
			rec.instrs = append(rec.instrs, map[string]any{"program": "spl-memo", "programId": prog.String(), "parsed": string(in.Data)})
		default:
			rec.instrs = append(rec.instrs, map[string]any{
				"programId": prog.String(), "accounts": []string{}, "data": solana.EncodeBase58(in.Data),
			})
		}
	}
	if fail {
		rec.err = map[string]any{"InstructionError": []any{2, map[string]any{"Custom": 1}}}
	}
	sig := rec.sigs[0]
	c.txs[sig] = rec
	for _, k := range tx.Message.AccountKeys {
		c.history[k] = append([]string{sig}, c.history[k]...)
	}
	return sig, nil
}

// ConfirmedOnly makes a landed transaction invisible at finalized commitment,
// as it is in the seconds after it lands.
func (c *Chain) ConfirmedOnly(sig string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hidden[sig] = true
}

// Finalize lifts ConfirmedOnly.
func (c *Chain) Finalize(sig string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.hidden, sig)
}

type request struct {
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
	ID     any               `json:"id"`
}

func (c *Chain) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	c.mu.Lock()
	c.calls[req.Method]++
	failure := c.FailMethod[req.Method]
	c.mu.Unlock()

	reply := func(result any, rpcErr map[string]any) {
		body := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		if rpcErr != nil {
			body["error"] = rpcErr
		} else {
			body["result"] = result
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}
	if failure != nil {
		reply(nil, map[string]any{"code": -32005, "message": failure.Error()})
		return
	}
	opt := func(i int) map[string]any {
		var m map[string]any
		if i < len(req.Params) {
			_ = json.Unmarshal(req.Params[i], &m)
		}
		return m
	}
	str := func(i int) string {
		var s string
		if i < len(req.Params) {
			_ = json.Unmarshal(req.Params[i], &s)
		}
		return s
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ctxVal := map[string]any{"slot": c.Height}
	finalized := func(o map[string]any) bool { return o != nil && o["commitment"] == "finalized" }

	switch req.Method {
	case "getGenesisHash":
		reply(c.GenesisHash, nil)
	case "getLatestBlockhash":
		reply(map[string]any{"context": ctxVal, "value": map[string]any{
			"blockhash": solana.EncodeBase58(c.blockhash[:]), "lastValidBlockHeight": c.Height + 150,
		}}, nil)
	case "getBlockHeight":
		h := c.Height
		if finalized(opt(0)) && h > c.FinalizedLag {
			h -= c.FinalizedLag
		}
		reply(h, nil)
	case "getBalance":
		pk, _ := solana.ParsePublicKey(str(0))
		reply(map[string]any{"context": ctxVal, "value": c.lamports[pk]}, nil)
	case "getAccountInfo":
		pk, _ := solana.ParsePublicKey(str(0))
		_, isToken := c.tokens[pk]
		if !isToken && c.lamports[pk] == 0 {
			reply(map[string]any{"context": ctxVal, "value": nil}, nil)
			return
		}
		owner := solana.SystemProgram
		if isToken {
			owner = solana.TokenProgram
		}
		reply(map[string]any{"context": ctxVal, "value": map[string]any{
			"lamports": c.lamports[pk], "owner": owner.String(), "executable": false, "data": []string{"", "base64"},
		}}, nil)
	case "getTokenAccountBalance":
		pk, _ := solana.ParsePublicKey(str(0))
		amount, ok := c.tokens[pk]
		if !ok {
			reply(nil, map[string]any{"code": -32602, "message": "Invalid param: could not find account"})
			return
		}
		reply(map[string]any{"context": ctxVal, "value": map[string]any{"amount": strconv.FormatUint(amount, 10), "decimals": 6}}, nil)
	case "getSignaturesForAddress":
		pk, _ := solana.ParsePublicKey(str(0))
		o := opt(1)
		limit := 1000
		if l, ok := o["limit"].(float64); ok {
			limit = int(l)
		}
		out := []map[string]any{}
		before, _ := o["before"].(string)
		skipping := before != ""
		for _, sig := range c.history[pk] {
			if skipping {
				// The cursor: start after the signature given.
				skipping = sig != before
				continue
			}
			if finalized(o) && c.hidden[sig] {
				continue
			}
			t := c.txs[sig]
			out = append(out, map[string]any{"signature": sig, "slot": t.slot, "err": t.err, "blockTime": t.blockTime})
			if len(out) >= limit {
				break
			}
		}
		reply(out, nil)
	case "getTransaction":
		sig := str(0)
		t, ok := c.txs[sig]
		if !ok || (finalized(opt(1)) && c.hidden[sig]) {
			reply(nil, nil)
			return
		}
		reply(map[string]any{
			"slot": t.slot, "blockTime": t.blockTime, "meta": map[string]any{"err": t.err},
			"transaction": map[string]any{"signatures": t.sigs, "message": map[string]any{"instructions": t.instrs}},
		}, nil)
	case "simulateTransaction":
		raw := str(0)
		if _, err := solana.DecodeTransactionBase64(raw); err != nil {
			reply(nil, map[string]any{"code": -32602, "message": "invalid transaction: " + err.Error()})
			return
		}
		reply(map[string]any{"context": ctxVal, "value": map[string]any{"err": nil, "logs": []string{"Program log: simulated"}, "unitsConsumed": 12345}}, nil)
	default:
		reply(nil, map[string]any{"code": -32601, "message": fmt.Sprintf("method %s not found", req.Method)})
	}
}

// Sponsor makes a funded fee-payer keypair, standing in for a facilitator.
func (c *Chain) Sponsor() *solana.Keypair {
	k, _ := solana.NewKeypair()
	c.Fund(k.PublicKey(), 1_000_000_000)
	return k
}

// RandomSignature is a syntactically valid signature that is on no chain.
func RandomSignature() string {
	b := make([]byte, ed25519.SignatureSize)
	_, _ = rand.Read(b)
	return solana.EncodeBase58(b)
}
