package jupiter

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/project-algebra/algebra/internal/platform/solana"
)

var (
	boughtMint = solana.MustPublicKey("DezXAZ8z7PnrnRJjz3wXBoRgixCa6xjnB7YaB1pPB263") // a token, as it might be
	usdcPK     = solana.MustPublicKey("EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v")
	otherMint  = solana.MustPublicKey("JUPyiwrYJFskUPiHa7hkeR8VUtAeFoSYbKedZNsDvCN")
	stranger   = solana.MustPublicKey("9xQeWvG816bUx9EPjHmaT23yvVM2ZWbrrpZb9PusVFin")
)

// swapTx is a v0 transaction like an aggregator's: signed by nobody yet, with an
// address table lookup, the wallet as its first signer and fee payer.
func swapTx(t *testing.T, payer solana.PublicKey, extraSigners ...solana.PublicKey) string {
	t.Helper()
	signers := append([]solana.PublicKey{payer}, extraSigners...)
	keys := append([]solana.PublicKey{}, signers...)
	keys = append(keys, solana.TokenProgram, solana.MustPublicKey("JUP6LkbZbjS1jKKwapdHNy74zcZ3tLUZoi5QNyVTaV4"))
	raw := []byte{byte(len(signers))}
	raw = append(raw, make([]byte, 64*len(signers))...)
	raw = append(raw, 0x80, byte(len(signers)), 0, 2, byte(len(keys)))
	for _, k := range keys {
		raw = append(raw, k[:]...)
	}
	raw = append(raw, bytes.Repeat([]byte{7}, 32)...)
	raw = append(raw, 1, byte(len(signers)+1), 1, 0, 3, 1, 2, 3)
	raw = append(raw, 1)
	raw = append(raw, bytes.Repeat([]byte{9}, 32)...)
	raw = append(raw, 1, 5, 0)
	return base64.StdEncoding.EncodeToString(raw)
}

func acct(lamports uint64, owner solana.PublicKey, data []byte) *solana.AccountInfo {
	return &solana.AccountInfo{Lamports: lamports, Owner: owner, Data: data}
}

func tokenAcct(mint, owner solana.PublicKey, amount uint64) *solana.AccountInfo {
	return acct(2_039_280, solana.TokenProgram, (&solana.TokenAccount{Mint: mint, Owner: owner, Amount: amount, State: solana.TokenAccountInitialized}).Encode())
}

// world is a Solana node scripted for one swap: the wallet's accounts as they
// are, what simulating the swap would leave them as, and what the chain knows
// of a signature.
type world struct {
	mu       sync.Mutex
	wallet   solana.PublicKey
	accounts map[solana.PublicKey]*solana.AccountInfo
	// after overrides the state a simulation returns; absent keys stay as they are,
	// and a nil value is an account that no longer exists.
	after     map[solana.PublicKey]*solana.AccountInfo
	simErr    any
	statuses  map[string]*solana.SignatureStatus
	balances  map[string][]balance // by signature: pre/post token balances
	txFailed  map[string]bool
	height    uint64
	simulated int
	calls     map[string]int
}

type balance struct {
	account, mint, owner string
	pre, post            uint64
}

func newWorld(wallet solana.PublicKey) *world {
	w := &world{
		wallet: wallet, accounts: map[solana.PublicKey]*solana.AccountInfo{}, after: map[solana.PublicKey]*solana.AccountInfo{},
		statuses: map[string]*solana.SignatureStatus{}, balances: map[string][]balance{}, txFailed: map[string]bool{}, height: 1_000, calls: map[string]int{},
	}
	usdcATA, _ := solana.AssociatedTokenAddress(wallet, usdcPK, solana.TokenProgram)
	w.accounts[wallet] = acct(50_000_000, solana.SystemProgram, nil)
	w.accounts[usdcATA] = tokenAcct(usdcPK, wallet, 10_000_000)
	w.accounts[boughtMint] = acct(1_461_600, solana.TokenProgram, make([]byte, 82)) // the mint itself
	return w
}

func (w *world) usdcATA() solana.PublicKey {
	a, _ := solana.AssociatedTokenAddress(w.wallet, usdcPK, solana.TokenProgram)
	return a
}

func (w *world) boughtATA() solana.PublicKey {
	a, _ := solana.AssociatedTokenAddress(w.wallet, boughtMint, solana.TokenProgram)
	return a
}

func (w *world) state(pk solana.PublicKey, simulated bool) *solana.AccountInfo {
	if simulated {
		if a, ok := w.after[pk]; ok {
			return a
		}
	}
	return w.accounts[pk]
}

func jsonAccount(a *solana.AccountInfo) any {
	if a == nil {
		return nil
	}
	return map[string]any{"lamports": a.Lamports, "owner": a.Owner.String(), "data": []string{base64.StdEncoding.EncodeToString(a.Data), "base64"}, "executable": a.Executable}
}

// serve answers the JSON-RPC methods the rail uses.
func (w *world) serve(t *testing.T) *solana.RPC {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int64             `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.mu.Lock()
		defer w.mu.Unlock()
		w.calls[req.Method]++
		var result any
		switch req.Method {
		case "getAccountInfo":
			var key string
			_ = json.Unmarshal(req.Params[0], &key)
			pk, _ := solana.ParsePublicKey(key)
			result = map[string]any{"value": jsonAccount(w.state(pk, false))}
		case "getMultipleAccounts":
			var keys []string
			_ = json.Unmarshal(req.Params[0], &keys)
			out := make([]any, len(keys))
			for i, k := range keys {
				pk, _ := solana.ParsePublicKey(k)
				out[i] = jsonAccount(w.state(pk, false))
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
			for pk, a := range w.accounts {
				if a.Owner.String() != filter.ProgramID || len(a.Data) < solana.TokenAccountSize {
					continue
				}
				if ta, err := solana.ParseTokenAccount(a.Data); err == nil && ta.Owner.String() == owner {
					list = append(list, map[string]any{"pubkey": pk.String(), "account": jsonAccount(a)})
				}
			}
			result = map[string]any{"value": list}
		case "simulateTransaction":
			w.simulated++
			var opts struct {
				Accounts struct {
					Addresses []string `json:"addresses"`
				} `json:"accounts"`
			}
			_ = json.Unmarshal(req.Params[1], &opts)
			value := map[string]any{"err": w.simErr, "logs": []string{}, "unitsConsumed": 200_000}
			if w.simErr == nil {
				out := make([]any, len(opts.Accounts.Addresses))
				for i, k := range opts.Accounts.Addresses {
					pk, _ := solana.ParsePublicKey(k)
					out[i] = jsonAccount(w.state(pk, true))
				}
				value["accounts"] = out
			}
			result = map[string]any{"value": value}
		case "getSignatureStatuses":
			var sigs []string
			_ = json.Unmarshal(req.Params[0], &sigs)
			out := make([]any, len(sigs))
			for i, s := range sigs {
				if st := w.statuses[s]; st != nil {
					out[i] = st
				}
			}
			result = map[string]any{"value": out}
		case "getBlockHeight":
			result = w.height
		case "getTransaction":
			var sig string
			_ = json.Unmarshal(req.Params[0], &sig)
			bs, ok := w.balances[sig]
			if !ok {
				result = nil
				break
			}
			keys := []any{}
			var pre, post []any
			for i, b := range bs {
				keys = append(keys, map[string]any{"pubkey": b.account})
				tok := func(amount uint64) any {
					return map[string]any{"accountIndex": i, "mint": b.mint, "owner": b.owner, "uiTokenAmount": map[string]any{"amount": uintStr(amount)}}
				}
				pre, post = append(pre, tok(b.pre)), append(post, tok(b.post))
			}
			var txErr any
			if w.txFailed[sig] {
				txErr = map[string]any{"InstructionError": []any{0, "Custom"}}
			}
			result = map[string]any{"slot": 1, "meta": map[string]any{"err": txErr, "preTokenBalances": pre, "postTokenBalances": post},
				"transaction": map[string]any{"signatures": []string{sig}, "message": map[string]any{"accountKeys": keys}}}
		}
		_ = json.NewEncoder(rw).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	t.Cleanup(srv.Close)
	return solana.NewRPC(srv.URL, nil)
}

func uintStr(n uint64) string { b, _ := json.Marshal(n); return string(b) }
