package solana_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/project-algebra/algebra/internal/platform/solana"
)

// withLookupTable is a v0 transaction like an aggregator's: signed by nobody
// yet, carrying an address table lookup that DecodeTransaction refuses.
func withLookupTable(t *testing.T, signers ...*solana.Keypair) []byte {
	t.Helper()
	keys := make([]solana.PublicKey, 0, len(signers)+2)
	for _, s := range signers {
		keys = append(keys, s.PublicKey())
	}
	keys = append(keys, solana.MustPublicKey("TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"), solana.MustPublicKey("JUP6LkbZbjS1jKKwapdHNy74zcZ3tLUZoi5QNyVTaV4"))
	raw := []byte{byte(len(signers))}
	raw = append(raw, make([]byte, 64*len(signers))...)
	raw = append(raw, 0x80, byte(len(signers)), 0, 2, byte(len(keys)))
	for _, k := range keys {
		raw = append(raw, k[:]...)
	}
	raw = append(raw, bytes.Repeat([]byte{7}, 32)...)            // blockhash
	raw = append(raw, 1, byte(len(signers)+1), 1, 0, 3, 1, 2, 3) // one instruction: program, one account, three bytes
	// one address table lookup: table key, one writable index, no readonly
	table := bytes.Repeat([]byte{9}, 32)
	raw = append(raw, 1)
	raw = append(raw, table...)
	raw = append(raw, 1, 5, 0)
	return raw
}

func TestRawTransactionSignsWhatDecodeTransactionRefuses(t *testing.T) {
	wallet, _ := solana.NewKeypair()
	raw := withLookupTable(t, wallet)
	if _, err := solana.DecodeTransaction(raw); err == nil {
		t.Fatal("the full decoder refuses lookup tables, which is why this exists")
	}

	tx, err := solana.ParseRawTransaction(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !tx.Versioned || tx.NumRequiredSignatures != 1 || tx.FeePayer() != wallet.PublicKey() || len(tx.AccountKeys) != 3 {
		t.Fatalf("parsed: %+v", tx)
	}
	signed, sig, err := tx.Sign(wallet)
	if err != nil {
		t.Fatal(err)
	}
	pub := wallet.PublicKey()
	if !ed25519.Verify(pub[:], tx.Message(), sig[:]) {
		t.Error("the signature is over the message, untouched")
	}
	if !bytes.Equal(signed[65:], raw[65:]) || bytes.Equal(signed[1:65], raw[1:65]) || !bytes.Equal(signed[1:65], sig[:]) {
		t.Error("only the signature slot changed")
	}
	if !bytes.Equal(tx.Message(), raw[65:]) {
		t.Error("the receiver is not modified")
	}
}

func TestRawTransactionSignsItsOwnSlotAndLeavesOthers(t *testing.T) {
	wallet, _ := solana.NewKeypair()
	other, _ := solana.NewKeypair()
	raw := withLookupTable(t, other, wallet) // the other signer comes first, as a market maker might
	tx, err := solana.ParseRawTransaction(raw)
	if err != nil {
		t.Fatal(err)
	}
	signed, sig, err := tx.Sign(wallet)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(signed[1:65], raw[1:65]) || !bytes.Equal(signed[65:129], sig[:]) {
		t.Error("the wallet's slot is the second one; the first is as it was")
	}
	if i, ok := tx.SignerIndex(wallet.PublicKey()); !ok || i != 1 {
		t.Errorf("index %d %v", i, ok)
	}
	stranger, _ := solana.NewKeypair()
	if _, _, err := tx.Sign(stranger); err == nil {
		t.Error("a key that isn't a signer can't sign")
	}
}

func TestRawTransactionRefusesWhatItCantBeSureOf(t *testing.T) {
	wallet, _ := solana.NewKeypair()
	good := withLookupTable(t, wallet)
	v1 := append([]byte(nil), good...)
	v1[65] = 0x81
	mismatched := append([]byte(nil), good...)
	mismatched[66] = 2 // header says two signers, one slot
	for name, raw := range map[string][]byte{
		"empty":            nil,
		"no signatures":    {0},
		"truncated":        good[:70],
		"version 1":        v1,
		"header mismatch":  mismatched,
		"signatures only":  append([]byte{1}, make([]byte, 64)...),
		"too many signers": append([]byte{0xff, 0x7f}, make([]byte, 64)...),
	} {
		if _, err := solana.ParseRawTransaction(raw); err == nil {
			t.Errorf("%s: must be refused", name)
		}
	}
}

func TestTokenAccountRoundTripAndWhatCountsAsChanged(t *testing.T) {
	mint, owner, delegate, closer := solana.PublicKey{1}, solana.PublicKey{2}, solana.PublicKey{3}, solana.PublicKey{4}
	native := uint64(2_039_280)
	a := &solana.TokenAccount{Mint: mint, Owner: owner, Amount: 500, State: solana.TokenAccountInitialized}
	got, err := solana.ParseTokenAccount(a.Encode())
	if err != nil || got.Mint != mint || got.Owner != owner || got.Amount != 500 || got.Delegate != nil || got.CloseAuthority != nil || got.IsNative != nil {
		t.Fatalf("plain: %+v %v", got, err)
	}
	full := &solana.TokenAccount{Mint: mint, Owner: owner, Amount: 9, Delegate: &delegate, State: solana.TokenAccountFrozen, IsNative: &native, DelegatedAmount: 4, CloseAuthority: &closer}
	got, err = solana.ParseTokenAccount(append(full.Encode(), 1, 2, 3)) // Token-2022 extensions follow
	if err != nil || got.Delegate == nil || *got.Delegate != delegate || got.State != solana.TokenAccountFrozen || *got.IsNative != native || got.DelegatedAmount != 4 || *got.CloseAuthority != closer {
		t.Fatalf("full: %+v %v", got, err)
	}
	if _, err := solana.ParseTokenAccount(make([]byte, 100)); err == nil {
		t.Error("too short")
	}

	// Only the amount may differ.
	moved := *a
	moved.Amount = 1
	if !a.SameExceptAmount(&moved) {
		t.Error("a different amount is the point of a swap")
	}
	for name, mod := range map[string]func(x *solana.TokenAccount){
		"a delegate appears":        func(x *solana.TokenAccount) { x.Delegate = &delegate },
		"the owner changes":         func(x *solana.TokenAccount) { x.Owner = delegate },
		"it is frozen":              func(x *solana.TokenAccount) { x.State = solana.TokenAccountFrozen },
		"a close authority appears": func(x *solana.TokenAccount) { x.CloseAuthority = &closer },
		"the mint changes":          func(x *solana.TokenAccount) { x.Mint = delegate },
		"a delegated amount":        func(x *solana.TokenAccount) { x.DelegatedAmount = 1 },
		"it becomes native":         func(x *solana.TokenAccount) { x.IsNative = &native },
	} {
		c := *a
		mod(&c)
		if a.SameExceptAmount(&c) {
			t.Errorf("%s must count as a change", name)
		}
	}
}

// node answers JSON-RPC with canned results by method, and records the options
// (second parameter) of every call.
func node(t *testing.T, results map[string]any) (*solana.RPC, *[]json.RawMessage) {
	t.Helper()
	var params []json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int64             `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if len(req.Params) > 1 {
			params = append(params, req.Params[1])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": results[req.Method]})
	}))
	t.Cleanup(srv.Close)
	return solana.NewRPC(srv.URL, nil), &params
}

func account(lamports uint64, owner solana.PublicKey, data []byte) map[string]any {
	return map[string]any{"lamports": lamports, "owner": owner.String(), "data": []string{base64.StdEncoding.EncodeToString(data), "base64"}, "executable": false}
}

func TestMultipleAccountsLineUpWithTheKeysAndMissingOnesAreNil(t *testing.T) {
	a, b := solana.PublicKey{1}, solana.PublicKey{2}
	rpc, _ := node(t, map[string]any{"getMultipleAccounts": map[string]any{"value": []any{account(5, solana.TokenProgram, []byte{1, 2}), nil}}})
	got, err := rpc.GetMultipleAccounts(context.Background(), []solana.PublicKey{a, b}, solana.Confirmed)
	if err != nil || len(got) != 2 || got[0] == nil || got[0].Lamports != 5 || got[0].Owner != solana.TokenProgram || !bytes.Equal(got[0].Data, []byte{1, 2}) || got[1] != nil {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := rpc.GetMultipleAccounts(context.Background(), make([]solana.PublicKey, 101), solana.Confirmed); err == nil {
		t.Error("more than 100 can't be read at once")
	}
	short, _ := node(t, map[string]any{"getMultipleAccounts": map[string]any{"value": []any{nil}}})
	if _, err := short.GetMultipleAccounts(context.Background(), []solana.PublicKey{a, b}, solana.Confirmed); err == nil {
		t.Error("a node that answers for fewer accounts than were asked is not believed")
	}
}

func TestTokenAccountsByOwner(t *testing.T) {
	acct := solana.PublicKey{7}
	data := (&solana.TokenAccount{Mint: solana.PublicKey{1}, Owner: solana.PublicKey{2}, Amount: 42, State: 1}).Encode()
	rpc, params := node(t, map[string]any{"getTokenAccountsByOwner": map[string]any{"value": []any{map[string]any{"pubkey": acct.String(), "account": account(2_039_280, solana.TokenProgram, data)}}}})
	got, err := rpc.GetTokenAccountsByOwner(context.Background(), solana.PublicKey{2}, solana.TokenProgram, solana.Confirmed)
	if err != nil || len(got) != 1 || got[0].Address != acct {
		t.Fatalf("%+v %v", got, err)
	}
	if ta, err := solana.ParseTokenAccount(got[0].Info.Data); err != nil || ta.Amount != 42 {
		t.Errorf("%+v %v", ta, err)
	}
	if len(*params) == 0 || !bytes.Contains((*params)[0], []byte(solana.TokenProgram.String())) {
		t.Errorf("asked under the token program: %s", *params)
	}
}

func TestSimulateWithAccountsReturnsTheStateAfter(t *testing.T) {
	a, b := solana.PublicKey{1}, solana.PublicKey{2}
	rpc, params := node(t, map[string]any{"simulateTransaction": map[string]any{"value": map[string]any{
		"err": nil, "logs": []string{"Program log: swap"}, "unitsConsumed": 123_456,
		"accounts": []any{account(1_000_000, solana.SystemProgram, nil), nil},
	}}})
	sim, err := rpc.SimulateWithAccounts(context.Background(), "dHg=", []solana.PublicKey{a, b}, solana.Processed)
	if err != nil || sim.Err != nil || sim.UnitsConsumed != 123_456 || len(sim.Accounts) != 2 || sim.Accounts[0].Lamports != 1_000_000 || sim.Accounts[1] != nil {
		t.Fatalf("%+v %v", sim, err)
	}
	var opts map[string]any
	_ = json.Unmarshal((*params)[0], &opts)
	if opts["sigVerify"] != false || opts["replaceRecentBlockhash"] != true || opts["accounts"] == nil {
		t.Errorf("an unsigned transaction can be simulated, with a fresh blockhash and the accounts back: %v", opts)
	}

	failing, _ := node(t, map[string]any{"simulateTransaction": map[string]any{"value": map[string]any{"err": map[string]any{"InstructionError": []any{0, "Custom"}}, "logs": []string{"boom"}}}})
	if sim, err := failing.SimulateWithAccounts(context.Background(), "dHg=", []solana.PublicKey{a}, solana.Processed); err != nil || sim.Err == nil || sim.Accounts != nil {
		t.Errorf("a failing transaction has an error and no state: %+v %v", sim, err)
	}
	if _, err := rpc.SimulateWithAccounts(context.Background(), "dHg=", nil, solana.Processed); err == nil {
		t.Error("it needs accounts to return")
	}
}
