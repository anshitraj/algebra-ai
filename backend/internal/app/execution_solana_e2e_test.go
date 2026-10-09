package app_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/receipt"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/platform/safehttp"
	"github.com/project-algebra/algebra/internal/platform/solana"
	"github.com/project-algebra/algebra/internal/platform/solana/solanatest"
	"github.com/project-algebra/algebra/providers/solanax402"
	"github.com/project-algebra/algebra/providers/x402client"
)

// These tests run the whole stack against a fake Solana cluster: the executor,
// the x402 runner, the real Solana rail, a provider whose facilitator checks
// every payment against the spec's rules, the chain the transaction lands on,
// and the coordinator that decides from what the chain proves. The only thing
// not real is the cluster itself.

type solWorld struct {
	svc     *app.EconomicService
	exec    *app.ExecutionService
	store   app.ExecutionStore
	signer  *receipt.Signer
	chain   *solanatest.Chain
	rail    *solanax402.Rail
	payer   *solana.Keypair
	mint    solana.PublicKey
	src     solana.PublicKey
	servers map[string]*solanatest.X402Server
	payees  map[string]solana.PublicKey
	srv     *httptest.Server
}

func newSolWorld(t *testing.T, version int, providers ...string) *solWorld {
	t.Helper()
	svc, signer := app.NewTestCoordinator(t, 1, 100_000_000)
	w := &solWorld{svc: svc, signer: signer, chain: solanatest.New(solana.MainnetGenesisHash), servers: map[string]*solanatest.X402Server{}, payees: map[string]solana.PublicKey{}}
	w.payer, _ = solana.NewKeypair()
	mintStr, _ := chain.AssetAddress(chain.Solana, "USDC")
	w.mint = solana.MustPublicKey(mintStr)
	w.src, _ = solana.AssociatedTokenAddress(w.payer.PublicKey(), w.mint, solana.TokenProgram)
	w.chain.SetTokenAccount(w.src, 10_000_000)

	rpc := solana.NewRPC(w.chain.Serve(t), nil)
	rail, err := solanax402.New(solanax402.Config{Cluster: "mainnet", RPC: rpc, Signer: w.payer})
	if err != nil {
		t.Fatal(err)
	}
	w.rail = rail
	svc.RegisterRail(rail)

	mux := http.NewServeMux()
	w.srv = httptest.NewServer(mux)
	t.Cleanup(w.srv.Close)
	network := chain.Solana
	if version >= 2 {
		network = "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp"
	}
	for _, name := range providers {
		payee, _ := solana.NewKeypair()
		dst, _ := solana.AssociatedTokenAddress(payee.PublicKey(), w.mint, solana.TokenProgram)
		w.chain.SetTokenAccount(dst, 0)
		s := &solanatest.X402Server{Chain: w.chain, Sponsor: w.chain.Sponsor(), PayTo: payee.PublicKey(), Mint: w.mint, Network: network, Version: version, Amount: 5_000}
		mux.Handle("/"+name, s.Handler())
		w.servers[name], w.payees[name] = s, payee.PublicKey()
	}

	catalog, err := app.NewStaticCatalog(app.DefaultCapabilities()...)
	if err != nil {
		t.Fatal(err)
	}
	w.store = app.NewMemExecStore()
	w.exec = app.NewExecutionService(svc, w.store)
	w.exec.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	w.exec.SetCapabilities(catalog)
	w.exec.RegisterRunner(x402client.New(x402client.Config{
		HTTP: safehttp.New(safehttp.Options{AllowLoopback: true, Timeout: 5 * time.Second}), Networks: map[string]string{chain.Solana: solanax402.RailName},
	}))
	return w
}

func (w *solWorld) intent(t *testing.T, input string, budget int64) *app.IntentView {
	t.Helper()
	v, _, err := w.svc.CreateIntent(context.Background(), "agent_1", econ.Spec{
		Capability: "solana.token-risk", Input: json.RawMessage(input), Window: "sol", Currency: "USDC", BudgetMaxMinor: budget,
	})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (w *solWorld) run(t *testing.T, intentID string, names ...string) (*app.PlanReport, error) {
	t.Helper()
	var cs []routing.Candidate
	for _, n := range names {
		cs = append(cs, routing.Candidate{
			Capability: "solana.token-risk", Provider: n, ExecutionType: routing.ExecX402, Endpoint: w.srv.URL + "/" + n, Method: "POST",
			Network: chain.Solana, Sources: []routing.DiscoverySource{routing.SourceConfigured},
		})
	}
	return w.exec.ExecuteCandidates(context.Background(), app.CandidatesRequest{AgentID: "agent_1", IntentID: intentID, Candidates: cs})
}

func (w *solWorld) usdc(pk solana.PublicKey) uint64 {
	b, _ := w.chain.TokenBalance(pk)
	return b
}

func (w *solWorld) payeeATA(name string) solana.PublicKey {
	a, _ := solana.AssociatedTokenAddress(w.payees[name], w.mint, solana.TokenProgram)
	return a
}

func TestSolanaE2E_PaysThroughTheSpecAndProvesSettlementFromTheChain(t *testing.T) {
	for _, version := range []int{1, 2} {
		w := newSolWorld(t, version, "alpha")
		in := w.intent(t, `{"mint":"SOL"}`, 50_000)

		rep, err := w.run(t, in.ID, "alpha")
		if err != nil {
			t.Fatalf("v%d: %v", version, err)
		}
		if !rep.Delivered || rep.Stopped != "committed" {
			t.Fatalf("v%d: delivered=%v stopped=%s attempts=%+v", version, rep.Delivered, rep.Stopped, rep.Attempts)
		}
		srv := w.servers["alpha"]
		if srv.Verified != 1 || len(srv.Settled) != 1 {
			t.Fatalf("v%d: the facilitator verified and settled exactly one payment: %d/%d", version, srv.Verified, len(srv.Settled))
		}
		a := rep.Attempts[0]
		r := a.Result
		if r.Payment != routing.PaymentSettled || r.Delivery != econ.FulfillmentFulfilled || r.ActualCostMinor != 5_000 || r.Test {
			t.Errorf("v%d: result: %+v", version, r)
		}
		if r.Transaction != srv.Settled[0] || r.Network != "solana" || r.Asset != w.mint.String() {
			t.Errorf("v%d: the recorded transaction is the sponsor's, on solana: %q %q %q", version, r.Transaction, r.Network, r.Asset)
		}
		// The money really moved, once, from the payer to the payee's associated token account.
		if w.usdc(w.src) != 10_000_000-5_000 || w.usdc(w.payeeATA("alpha")) != 5_000 {
			t.Errorf("v%d: balances: payer %d, payee %d", version, w.usdc(w.src), w.usdc(w.payeeATA("alpha")))
		}
		// The rail confirms it from chain state alone, whatever anyone reports.
		ev := a.Intent.Reservations[0].Evidence
		st, err := w.rail.Settlement(context.Background(), ev)
		if err != nil || st.Status != app.SettlementSettled || st.Transaction != srv.Settled[0] || st.AmountMinor != 5_000 {
			t.Errorf("v%d: rail: %+v %v", version, st, err)
		}
		if a.Quality == nil || a.Quality.FinalQuality == nil || *a.Quality.FinalQuality != 100 {
			t.Errorf("v%d: quality: %+v", version, a.Quality)
		}
		// The receipt carries Solana evidence and does not say "test".
		c, err := receipt.VerifyIntent(a.Intent.Receipt, w.signer.JWKS())
		if err != nil {
			t.Fatal(err)
		}
		if c.Test || c.Settlement.Network != "solana" || c.Settlement.Transaction != srv.Settled[0] || c.Settlement.Asset != w.mint.String() ||
			c.Settlement.Payer != w.payer.PublicKey().String() || c.Settlement.Rail != solanax402.RailName || c.Settlement.PaymentID == "" {
			t.Errorf("v%d: receipt settlement: %+v test=%v", version, c.Settlement, c.Test)
		}
	}
}

// The provider settled the payment and its answer was lost. A provider that
// remembers the result replays it for the same idempotency key, for free.
func TestSolanaE2E_LostResponseIsReplayedNotRepaid(t *testing.T) {
	w := newSolWorld(t, 2, "alpha")
	w.servers["alpha"].DropResponse = true
	in := w.intent(t, `{"mint":"SOL"}`, 50_000)

	rep, err := w.run(t, in.ID, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Delivered || len(w.servers["alpha"].Settled) != 1 {
		t.Fatalf("delivered=%v settled=%d", rep.Delivered, len(w.servers["alpha"].Settled))
	}
	if w.usdc(w.src) != 10_000_000-5_000 {
		t.Errorf("one payment, not two: payer holds %d", w.usdc(w.src))
	}
}

// The provider settled, lost its answer and keeps no record. The payment is
// still found on chain by the payer's own signature, so the intent commits
// honestly: paid, result unknown, and never paid again.
func TestSolanaE2E_LostResponseWithoutReplayIsFoundOnChain(t *testing.T) {
	w := newSolWorld(t, 2, "alpha", "beta")
	w.servers["alpha"].DropResponse, w.servers["alpha"].NoReplay = true, true
	in := w.intent(t, `{"mint":"SOL"}`, 50_000)

	rep, err := w.run(t, in.ID, "alpha", "beta")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Delivered || rep.Stopped != "committed" || len(rep.Attempts) != 1 {
		t.Fatalf("committed without a result, and the fallback must not run: delivered=%v stopped=%s attempts=%d", rep.Delivered, rep.Stopped, len(rep.Attempts))
	}
	a := rep.Attempts[0]
	if a.Intent.State != econ.StateCommitted || a.Intent.Fulfillment != econ.FulfillmentUnknown || a.Intent.CommittedMinor != 5_000 {
		t.Errorf("paid, result unknown: %s/%s/%d", a.Intent.State, a.Intent.Fulfillment, a.Intent.CommittedMinor)
	}
	if a.Result.Payment != routing.PaymentSettled || a.Result.Transaction != w.servers["alpha"].Settled[0] {
		t.Errorf("found on chain by our signature: %+v", a.Result)
	}
	if w.servers["beta"].PaidRequests != 0 {
		t.Error("the fallback must not be paid once money has moved")
	}
	if w.usdc(w.src) != 10_000_000-5_000 {
		t.Errorf("paid exactly once: %d", w.usdc(w.src))
	}
}

// The facilitator refuses the payment, so nothing is submitted. Until the
// signed payment's blockhash expires at a finalized height nobody can say it
// won't land, so the executor waits; once it can be proven absent the intent
// reopens and the fallback is paid instead. One payment, in the end.
func TestSolanaE2E_RefusedPaymentIsProvenAbsentThenFallsBack(t *testing.T) {
	w := newSolWorld(t, 2, "alpha", "beta")
	w.servers["alpha"].RejectPayment = true
	in := w.intent(t, `{"mint":"SOL"}`, 50_000)

	rep, err := w.run(t, in.ID, "alpha", "beta")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Pending || rep.Delivered || w.servers["beta"].PaidRequests != 0 {
		t.Fatalf("while the signed payment could still land there is no fallback: pending=%v beta paid requests=%d", rep.Pending, w.servers["beta"].PaidRequests)
	}
	if w.usdc(w.src) != 10_000_000 {
		t.Errorf("nothing moved: %d", w.usdc(w.src))
	}

	// Time passes: the blockhash expires and the chain finalizes past it.
	w.chain.Advance(200)
	if _, err := w.svc.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	v, _ := w.svc.View(context.Background(), in.ID, false)
	if v.State != econ.StateOpen || v.CommittedMinor != 0 {
		t.Fatalf("proven never to have landed: the intent reopens: %s %d", v.State, v.CommittedMinor)
	}
	rep2, err := w.run(t, in.ID, "beta")
	if err != nil || !rep2.Delivered {
		t.Fatalf("the fallback delivers: %v", err)
	}
	if w.usdc(w.src) != 10_000_000-5_000 || w.usdc(w.payeeATA("beta")) != 5_000 || w.usdc(w.payeeATA("alpha")) != 0 {
		t.Errorf("only the fallback was paid: payer %d beta %d alpha %d", w.usdc(w.src), w.usdc(w.payeeATA("beta")), w.usdc(w.payeeATA("alpha")))
	}
	hist, _ := w.exec.Executions(context.Background(), "user_1", in.ID)
	if len(hist) != 2 || hist[0].Result.Payment != routing.PaymentNotSettled || hist[1].Result.Payment != routing.PaymentSettled {
		t.Errorf("history: %+v", hist)
	}
}

func TestSolanaE2E_TheRailsHardCeilingStopsAnOverpricedProvider(t *testing.T) {
	w := newSolWorld(t, 2, "greedy", "beta")
	w.servers["greedy"].Amount = 2_000_000 // 2 USDC: over the rail's 1 USDC ceiling, though the intent allows it
	in := w.intent(t, `{"mint":"SOL"}`, 5_000_000)

	rep, err := w.run(t, in.ID, "greedy", "beta")
	if err != nil || !rep.Delivered {
		t.Fatalf("the cheap provider delivers: %v", err)
	}
	if rep.Attempts[0].Result.Provider != "beta" || len(rep.Attempts) != 1 {
		// The ceiling is enforced when signing, after the quote; the first
		// attempt is refused before any signature exists and falls through.
		var providers []string
		for _, a := range rep.Attempts {
			providers = append(providers, a.Result.Provider+":"+string(a.Result.Payment))
		}
		if len(rep.Attempts) != 2 || rep.Attempts[0].Result.Payment != routing.PaymentNotAttempted {
			t.Fatalf("the greedy provider must be refused without a payment: %v", providers)
		}
	}
	if w.usdc(w.payeeATA("greedy")) != 0 || w.servers["greedy"].Verified != 0 {
		t.Error("nothing was signed for the greedy provider")
	}
	if w.usdc(w.src) != 10_000_000-5_000 {
		t.Errorf("only beta was paid: %d", w.usdc(w.src))
	}
}

func TestSolanaE2E_AnUnderfundedWalletIsRefusedBeforeSigning(t *testing.T) {
	w := newSolWorld(t, 2, "alpha")
	w.chain.SetTokenAccount(w.src, 1_000)
	in := w.intent(t, `{"mint":"SOL"}`, 50_000)

	rep, err := w.run(t, in.ID, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Delivered || len(rep.Attempts) != 1 {
		t.Fatalf("delivered=%v attempts=%d", rep.Delivered, len(rep.Attempts))
	}
	r := rep.Attempts[0].Result
	if r.Payment != routing.PaymentNotAttempted || r.Failure == nil || r.Failure.Class != routing.FailPayment || !strings.Contains(r.Failure.Message, "insufficient USDC") {
		t.Errorf("result: %+v failure=%+v", r, r.Failure)
	}
	if rep.Attempts[0].Intent.State != econ.StateOpen || w.servers["alpha"].Verified != 0 {
		t.Error("nothing was signed or submitted, and the intent is open again")
	}
}
