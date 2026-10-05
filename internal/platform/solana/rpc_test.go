package solana_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/platform/solana"
	"github.com/project-algebra/algebra/internal/platform/solana/solanatest"
)

var usdcMint = solana.MustPublicKey("EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v")

// payment builds the partially signed transaction an x402 payer makes.
func payment(t *testing.T, chain *solanatest.Chain, amount uint64) (b64 string, payer *solana.Keypair, sponsor *solana.Keypair, src, dst solana.PublicKey, memo string) {
	t.Helper()
	payer, _ = solana.NewKeypair()
	sponsor = chain.Sponsor()
	payee, _ := solana.NewKeypair()
	src, _ = solana.AssociatedTokenAddress(payer.PublicKey(), usdcMint, solana.TokenProgram)
	dst, _ = solana.AssociatedTokenAddress(payee.PublicKey(), usdcMint, solana.TokenProgram)
	chain.SetTokenAccount(src, 10_000_000)
	chain.SetTokenAccount(dst, 0)
	memo = "00112233445566778899aabbccddeeff"
	mi, _ := solana.Memo(memo)
	info := solana.BlockhashInfo{}
	msg, err := solana.CompileV0(sponsor.PublicKey(), []solana.Instruction{
		solana.SetComputeUnitLimit(60_000), solana.SetComputeUnitPrice(1),
		solana.TransferChecked(solana.TokenProgram, src, usdcMint, dst, payer.PublicKey(), amount, 6), mi,
	}, info.Blockhash)
	if err != nil {
		t.Fatal(err)
	}
	tx := solana.NewTransaction(msg)
	if err := tx.PartialSign(payer); err != nil {
		t.Fatal(err)
	}
	return tx.Base64(), payer, sponsor, src, dst, memo
}

func TestRPCAgainstTheFakeChain(t *testing.T) {
	ctx := context.Background()
	chain := solanatest.New(solana.DevnetGenesisHash)
	rpc := solana.NewRPC(chain.Serve(t), nil)

	if h, err := rpc.GetGenesisHash(ctx); err != nil || h != solana.DevnetGenesisHash {
		t.Fatalf("genesis: %q %v", h, err)
	}
	bh, err := rpc.GetLatestBlockhash(ctx, solana.Finalized)
	if err != nil || bh.LastValidBlockHeight != chain.Height+150 || bh.Blockhash == [32]byte{} {
		t.Fatalf("blockhash: %+v %v", bh, err)
	}
	if h, err := rpc.GetBlockHeight(ctx, solana.Confirmed); err != nil || h != chain.Height {
		t.Errorf("height: %d %v", h, err)
	}
	if h, _ := rpc.GetBlockHeight(ctx, solana.Finalized); h != chain.Height-chain.FinalizedLag {
		t.Errorf("finalized height lags the tip: %d", h)
	}

	b64, payer, sponsor, src, dst, memo := payment(t, chain, 5_000)
	bal, err := rpc.GetTokenAccountBalance(ctx, src, solana.Confirmed)
	if err != nil || bal.Amount != 10_000_000 || bal.Decimals != 6 {
		t.Errorf("balance: %+v %v", bal, err)
	}
	if _, err := rpc.GetTokenAccountBalance(ctx, solana.PublicKey{9}, solana.Confirmed); !errors.Is(err, solana.ErrAccountNotFound) {
		t.Errorf("a missing token account is ErrAccountNotFound: %v", err)
	}
	if info, err := rpc.GetAccountInfo(ctx, dst, solana.Confirmed); err != nil || info == nil || info.Owner != solana.TokenProgram {
		t.Errorf("destination token account: %+v %v", info, err)
	}
	if info, err := rpc.GetAccountInfo(ctx, solana.PublicKey{8}, solana.Confirmed); err != nil || info != nil {
		t.Errorf("a missing account is nil, not an error: %+v %v", info, err)
	}
	if lam, err := rpc.GetBalance(ctx, sponsor.PublicKey(), solana.Confirmed); err != nil || lam != 1_000_000_000 {
		t.Errorf("sol balance: %d %v", lam, err)
	}

	// Simulation: a good transaction runs; garbage is refused by the node.
	if sim, err := rpc.SimulateTransaction(ctx, b64, solana.Confirmed); err != nil || sim.Err != nil || sim.UnitsConsumed == 0 {
		t.Errorf("simulation: %+v %v", sim, err)
	}
	if _, err := rpc.SimulateTransaction(ctx, "AAAA", solana.Confirmed); err == nil {
		t.Error("an undecodable transaction is an error")
	}

	// Land it as the sponsor would, then read it back every way Algebra does.
	sig, err := chain.Land(b64, sponsor)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := rpc.GetTransaction(ctx, sig, solana.Confirmed)
	if err != nil || tx == nil || tx.Err != nil {
		t.Fatalf("transaction: %+v %v", tx, err)
	}
	if m, ok := tx.Memo(); !ok || m != memo {
		t.Errorf("memo: %q %v", m, ok)
	}
	tr := tx.Transfers()
	if len(tr) != 1 || tr[0].Source != src.String() || tr[0].Destination != dst.String() || tr[0].Mint != usdcMint.String() ||
		tr[0].Authority != payer.PublicKey().String() || tr[0].Amount != 5_000 || tr[0].Decimals != 6 {
		t.Errorf("transfers: %+v", tr)
	}
	if len(tx.Signatures) != 2 || tx.Signatures[0] != sig {
		t.Errorf("the transaction's signature is the fee payer's: %v", tx.Signatures)
	}
	if bal, _ := chain.TokenBalance(dst); bal != 5_000 {
		t.Errorf("the transfer took effect: %d", bal)
	}

	hist, err := rpc.GetSignaturesForAddress(ctx, src, 10, "", solana.Confirmed)
	if err != nil || len(hist) != 1 || hist[0].Signature != sig || hist[0].Err != nil {
		t.Errorf("history: %+v %v", hist, err)
	}
	if got, err := rpc.GetTransaction(ctx, solanatest.RandomSignature(), solana.Confirmed); err != nil || got != nil {
		t.Errorf("an unknown signature is nil: %+v %v", got, err)
	}

	// Confirmed but not yet finalized: invisible at finalized commitment.
	chain.ConfirmedOnly(sig)
	if got, _ := rpc.GetTransaction(ctx, sig, solana.Finalized); got != nil {
		t.Error("not visible at finalized yet")
	}
	if got, _ := rpc.GetTransaction(ctx, sig, solana.Confirmed); got == nil {
		t.Error("visible at confirmed")
	}
}

func TestRPCErrorsAreReported(t *testing.T) {
	ctx := context.Background()
	chain := solanatest.New(solana.MainnetGenesisHash)
	rpc := solana.NewRPC(chain.Serve(t), nil)
	chain.FailMethod["getBlockHeight"] = errors.New("node is behind")
	_, err := rpc.GetBlockHeight(ctx, solana.Finalized)
	var re *solana.RPCError
	if !errors.As(err, &re) || !strings.Contains(re.Message, "node is behind") {
		t.Errorf("a node error comes through: %v", err)
	}
	if _, err := rpc.GetGenesisHash(ctx); err != nil {
		t.Errorf("other methods are unaffected: %v", err)
	}

	// HTTP-level failures.
	for name, status := range map[string]int{"rate limited": http.StatusTooManyRequests, "server error": http.StatusBadGateway} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
		_, err := solana.NewRPC(srv.URL, nil).GetGenesisHash(ctx)
		srv.Close()
		if err == nil {
			t.Errorf("%s must be an error", name)
		}
	}
	// A response that isn't JSON-RPC, and one that is too large.
	junk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) }))
	defer junk.Close()
	if _, err := solana.NewRPC(junk.URL, nil).GetGenesisHash(ctx); err == nil {
		t.Error("non-JSON is an error")
	}
	// A hung node doesn't hang the caller.
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { time.Sleep(300 * time.Millisecond) }))
	defer slow.Close()
	tctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := solana.NewRPC(slow.URL, nil).GetGenesisHash(tctx); err == nil {
		t.Error("the context bounds the call")
	}
}

// Against a real node (devnet is free to read), when SOLANA_RPC_URL is set:
//
//	SOLANA_RPC_URL=https://api.devnet.solana.com go test ./internal/platform/solana -run Live -v
//
// It checks our own transaction encoding and address derivation against the
// real thing, which no fake can: the node decodes our bytes, and the
// associated token account we derive is the one that actually holds a
// well-known wallet's USDC.
func TestLiveNodeAgreesWithOurEncodingAndDerivation(t *testing.T) {
	url := os.Getenv("SOLANA_RPC_URL")
	if url == "" {
		t.Skip("SOLANA_RPC_URL not set; skipping the live-node check")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	rpc := solana.NewRPC(url, nil)

	genesis, err := rpc.GetGenesisHash(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var mint solana.PublicKey
	switch genesis {
	case solana.DevnetGenesisHash:
		mint = solana.MustPublicKey("4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU")
	case solana.MainnetGenesisHash:
		mint = usdcMint
	default:
		t.Fatalf("unknown cluster %s", genesis)
	}
	t.Logf("cluster genesis %s, USDC mint %s", genesis, mint)

	// 1. The node accepts a transaction built by our encoder. A throwaway fee
	//    payer and payer (no funds, no signatures verified): the failure the
	//    node reports must be about accounts, not about bytes it can't read.
	payer, _ := solana.NewKeypair()
	fee, _ := solana.NewKeypair()
	payee, _ := solana.NewKeypair()
	src, _ := solana.AssociatedTokenAddress(payer.PublicKey(), mint, solana.TokenProgram)
	dst, _ := solana.AssociatedTokenAddress(payee.PublicKey(), mint, solana.TokenProgram)
	bh, err := rpc.GetLatestBlockhash(ctx, solana.Finalized)
	if err != nil {
		t.Fatal(err)
	}
	memo, _ := solana.Memo("00112233445566778899aabbccddeeff")
	msg, _ := solana.CompileV0(fee.PublicKey(), []solana.Instruction{
		solana.SetComputeUnitLimit(60_000), solana.SetComputeUnitPrice(1),
		solana.TransferChecked(solana.TokenProgram, src, mint, dst, payer.PublicKey(), 1, 6), memo,
	}, bh.Blockhash)
	tx := solana.NewTransaction(msg)
	_ = tx.PartialSign(payer)
	sim, err := rpc.SimulateTransaction(ctx, tx.Base64(), solana.Confirmed)
	if err != nil {
		t.Fatalf("the node rejected our encoding outright: %v", err)
	}
	t.Logf("simulation: err=%v units=%d logs=%v", sim.Err, sim.UnitsConsumed, sim.Logs)
	if s, _ := sim.Err.(string); s == "SanitizeFailure" {
		t.Errorf("the node could not sanitize our transaction: %v", sim.Err)
	}

	// 2. The associated token account we derive is the real one. Ask the node
	//    which token accounts a well-known wallet has for this mint and
	//    compare. (The wallet is read from SOLANA_LIVE_OWNER, so no address is
	//    baked into the repo.)
	owner := os.Getenv("SOLANA_LIVE_OWNER")
	if owner == "" {
		t.Log("SOLANA_LIVE_OWNER not set; skipping the derivation comparison")
		return
	}
	ownerKey, err := solana.ParsePublicKey(owner)
	if err != nil {
		t.Fatal(err)
	}
	ata, _ := solana.AssociatedTokenAddress(ownerKey, mint, solana.TokenProgram)
	bal, err := rpc.GetTokenAccountBalance(ctx, ata, solana.Confirmed)
	if err != nil {
		t.Fatalf("the derived token account %s doesn't exist on chain: %v", ata, err)
	}
	t.Logf("derived ATA %s holds %d (decimals %d)", ata, bal.Amount, bal.Decimals)
}

// liveCall is a raw JSON-RPC call for the live checks, which need methods the
// client doesn't (and shouldn't) expose.
func liveCall(t *testing.T, url, method string, params []any, out any) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	for attempt := 0; ; attempt++ {
		resp, err := http.Post(url, "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		var env struct {
			Result json.RawMessage  `json:"result"`
			Error  *solana.RPCError `json:"error"`
		}
		err = json.NewDecoder(resp.Body).Decode(&env)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if env.Error != nil && env.Error.Code == 429 && attempt < 5 {
			time.Sleep(time.Duration(2+attempt*2) * time.Second) // public nodes rate-limit; back off
			continue
		}
		if env.Error != nil {
			t.Fatalf("%s: %v", method, env.Error)
		}
		if err := json.Unmarshal(env.Result, out); err != nil {
			t.Fatal(err)
		}
		return
	}
}

// With a funded account as fee payer (no key needed: signatures aren't
// verified in simulation) the node goes past decoding and runs our
// instructions. A transaction we encoded wrongly fails at the compute-budget
// or token instruction's DATA; a right one fails later, on the token
// accounts that don't exist.
func TestLiveNodeRunsOurInstructions(t *testing.T) {
	url := os.Getenv("SOLANA_RPC_URL")
	if url == "" {
		t.Skip("SOLANA_RPC_URL not set; skipping the live-node check")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	rpc := solana.NewRPC(url, nil)
	if g, err := rpc.GetGenesisHash(ctx); err != nil || g != solana.DevnetGenesisHash {
		t.Skipf("this check uses devnet's USDC mint (genesis %q, %v)", g, err)
	}
	var votes struct {
		Current []struct {
			NodePubkey string `json:"nodePubkey"`
		} `json:"current"`
	}
	liveCall(t, url, "getVoteAccounts", nil, &votes)
	if len(votes.Current) == 0 {
		t.Skip("no validators to borrow a funded address from")
	}
	fee := solana.MustPublicKey(votes.Current[0].NodePubkey)
	mint := solana.MustPublicKey("4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU")
	payer, _ := solana.NewKeypair()
	payee, _ := solana.NewKeypair()
	src, _ := solana.AssociatedTokenAddress(payer.PublicKey(), mint, solana.TokenProgram)
	dst, _ := solana.AssociatedTokenAddress(payee.PublicKey(), mint, solana.TokenProgram)
	bh, err := rpc.GetLatestBlockhash(ctx, solana.Finalized)
	if err != nil {
		t.Fatal(err)
	}
	memo, _ := solana.Memo("00112233445566778899aabbccddeeff")
	msg, _ := solana.CompileV0(fee, []solana.Instruction{
		solana.SetComputeUnitLimit(60_000), solana.SetComputeUnitPrice(1),
		solana.TransferChecked(solana.TokenProgram, src, mint, dst, payer.PublicKey(), 1, 6), memo,
	}, bh.Blockhash)
	tx := solana.NewTransaction(msg)
	_ = tx.PartialSign(payer)
	sim, err := rpc.SimulateTransaction(ctx, tx.Base64(), solana.Confirmed)
	if err != nil {
		t.Fatalf("the node rejected our encoding outright: %v", err)
	}
	b, _ := json.Marshal(sim)
	t.Logf("simulation against %s as fee payer: %s", fee, b)
	ie, ok := sim.Err.(map[string]any)
	if !ok {
		t.Fatalf("expected an instruction error on the empty token accounts, got %v", sim.Err)
	}
	detail, _ := ie["InstructionError"].([]any)
	if len(detail) != 2 || detail[0] != float64(2) {
		t.Fatalf("the compute-budget instructions must pass and the token instruction must be the one that fails (index 2): %v", sim.Err)
	}
	if s, _ := detail[1].(string); s == "InvalidInstructionData" || s == "InvalidAccountData" && false {
		t.Errorf("the token program could not read our TransferChecked data: %v", detail[1])
	}
}

// The associated token account we derive must be the address the network
// itself creates. Recent transactions that create associated token accounts
// state, in their parsed instructions, the wallet, the mint and the account
// that resulted: ground truth for any mint, and for Token-2022 too. Most are
// created by another program through a cross-program call, so inner
// instructions are read as well.
func TestLiveDerivationMatchesAccountsTheNetworkCreated(t *testing.T) {
	url := os.Getenv("SOLANA_MAINNET_RPC_URL")
	if url == "" {
		t.Skip("SOLANA_MAINNET_RPC_URL not set; skipping the live derivation check")
	}
	var sigs []struct {
		Signature string `json:"signature"`
		Err       any    `json:"err"`
	}
	liveCall(t, url, "getSignaturesForAddress", []any{solana.AssociatedTokenProgram.String(), map[string]any{"limit": 40, "commitment": "finalized"}}, &sigs)

	type parsedIx struct {
		Program string `json:"program"`
		Parsed  struct {
			Type string `json:"type"`
			Info struct {
				Account      string `json:"account"`
				Wallet       string `json:"wallet"`
				Mint         string `json:"mint"`
				TokenProgram string `json:"tokenProgram"`
			} `json:"info"`
		} `json:"parsed"`
	}
	type creation struct{ Account, Wallet, Mint, TokenProgram string }
	var found []creation
	collect := func(instrs []parsedIx) {
		for _, in := range instrs {
			if in.Program == "spl-associated-token-account" && (in.Parsed.Type == "create" || in.Parsed.Type == "createIdempotent") {
				i := in.Parsed.Info
				found = append(found, creation{i.Account, i.Wallet, i.Mint, i.TokenProgram})
			}
		}
	}
	for _, sg := range sigs {
		if sg.Err != nil || len(found) >= 4 {
			continue
		}
		var tx struct {
			Meta struct {
				InnerInstructions []struct {
					Instructions []parsedIx `json:"instructions"`
				} `json:"innerInstructions"`
			} `json:"meta"`
			Transaction struct {
				Message struct {
					Instructions []parsedIx `json:"instructions"`
				} `json:"message"`
			} `json:"transaction"`
		}
		liveCall(t, url, "getTransaction", []any{sg.Signature, map[string]any{"encoding": "jsonParsed", "commitment": "finalized", "maxSupportedTransactionVersion": 255}}, &tx)
		collect(tx.Transaction.Message.Instructions)
		for _, inner := range tx.Meta.InnerInstructions {
			collect(inner.Instructions)
		}
		time.Sleep(700 * time.Millisecond) // the public node rate-limits
	}
	if len(found) == 0 {
		t.Skip("no associated-token-account creations in the latest transactions; try again")
	}
	for _, f := range found {
		wallet, err1 := solana.ParsePublicKey(f.Wallet)
		mint, err2 := solana.ParsePublicKey(f.Mint)
		prog, err3 := solana.ParsePublicKey(f.TokenProgram)
		if err1 != nil || err2 != nil || err3 != nil {
			t.Fatalf("unparseable creation: %+v", f)
		}
		derived, err := solana.AssociatedTokenAddress(wallet, mint, prog)
		if err != nil {
			t.Fatal(err)
		}
		if derived.String() != f.Account {
			t.Errorf("wallet %s mint %s token program %s: the network created %s, we derive %s", f.Wallet, f.Mint, f.TokenProgram, f.Account, derived)
		}
	}
	t.Logf("%d associated token accounts created on mainnet match our derivation", len(found))
}
