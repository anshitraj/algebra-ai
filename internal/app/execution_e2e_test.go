package app_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/receipt"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/platform/safehttp"
	"github.com/project-algebra/algebra/providers/sandboxpay"
	"github.com/project-algebra/algebra/providers/x402"
	"github.com/project-algebra/algebra/providers/x402client"
)

// These tests run the whole path with nothing mocked but the money itself:
// real HTTP to real x402 providers (the sandbox ones), the real coordinator,
// the real executor and x402 runner, and the sandbox rail, whose payments
// have deadlines like a chain's blockhash does.

// faulty adds the sandbox provider's fault-injection header to paid requests.
type faulty struct {
	inner *safehttp.Client
	mu    sync.Mutex
	fault map[string]string // URL path -> fault
}

func (f *faulty) set(path, fault string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fault[path] = fault
}

func (f *faulty) Do(req *http.Request) (*safehttp.Response, error) {
	if req.Header.Get(x402.HeaderPayment) != "" {
		f.mu.Lock()
		fault := f.fault[req.URL.Path]
		f.mu.Unlock()
		if fault != "" {
			req.Header.Set("X-Sandbox-Fault", fault)
		}
	}
	return f.inner.Do(req)
}

type world struct {
	svc    *app.EconomicService
	exec   *app.ExecutionService
	store  app.ExecutionStore
	rail   *sandboxpay.Rail
	faults *faulty
	signer *receipt.Signer
	srv    *httptest.Server
}

const tokenRiskSchema = `{"type":"object","required":["mint","risk_score"],"properties":{"mint":{"type":"string"},"risk_score":{"type":"number"},"sandbox":{"type":"boolean"}}}`

func newWorld(t *testing.T, railTTL time.Duration, passBudget int64) *world {
	t.Helper()
	svc, signer := app.NewTestCoordinator(t, 1, passBudget)
	rail := sandboxpay.NewRail(railTTL)
	svc.RegisterRail(rail)

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	for _, name := range []string{"alpha", "beta"} {
		p := sandboxpay.NewProvider(rail, srv.URL+"/"+name, 3_000)
		mux.HandleFunc("POST /"+name, p.Serve)
		mux.HandleFunc("GET /"+name+"/operations/{key}", p.ServeOperation)
		svc.RegisterRecovery(name, p)
	}

	faults := &faulty{inner: safehttp.New(safehttp.Options{AllowLoopback: true, Timeout: 5 * time.Second}), fault: map[string]string{}}
	catalog, err := app.NewStaticCatalog(routing.Capability{ID: "solana.token-risk", OutputSchema: json.RawMessage(tokenRiskSchema)})
	if err != nil {
		t.Fatal(err)
	}
	store := app.NewMemExecStore()
	exec := app.NewExecutionService(svc, store)
	exec.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	exec.SetCapabilities(catalog)
	exec.RegisterRunner(x402client.New(x402client.Config{HTTP: faults, Networks: map[string]string{"sandbox": "sandbox"}}))
	return &world{svc: svc, exec: exec, store: store, rail: rail, faults: faults, signer: signer, srv: srv}
}

func (w *world) intent(t *testing.T, budget int64) *app.IntentView {
	t.Helper()
	v, _, err := w.svc.CreateIntent(context.Background(), "agent_1", econ.Spec{
		Capability: "solana.token-risk", Input: json.RawMessage(`{"mint":"SOL"}`), Window: "e2e", Currency: "USDC", BudgetMaxMinor: budget,
	})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (w *world) candidate(name string) routing.Candidate {
	return routing.Candidate{
		Capability: "solana.token-risk", Provider: name, ExecutionType: routing.ExecX402, Endpoint: w.srv.URL + "/" + name,
		Method: "POST", Network: "sandbox", Sources: []routing.DiscoverySource{routing.SourceConfigured},
	}
}

func (w *world) run(t *testing.T, intentID string, names ...string) (*app.PlanReport, error) {
	t.Helper()
	var cs []routing.Candidate
	for _, n := range names {
		cs = append(cs, w.candidate(n))
	}
	return w.exec.ExecuteCandidates(context.Background(), app.CandidatesRequest{AgentID: "agent_1", IntentID: intentID, Candidates: cs})
}

func TestE2E_RealPaidCallEndToEnd(t *testing.T) {
	w := newWorld(t, time.Minute, 10_000_000)
	in := w.intent(t, 50_000)

	rep, err := w.run(t, in.ID, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Delivered || rep.Stopped != "committed" {
		t.Fatalf("delivered=%v stopped=%s", rep.Delivered, rep.Stopped)
	}
	a := rep.Attempts[0]
	r := a.Result
	if r.Payment != routing.PaymentSettled || r.Delivery != econ.FulfillmentFulfilled || r.ActualCostMinor != 3_000 || !r.Test {
		t.Errorf("result: %+v", r)
	}
	if !strings.HasPrefix(r.Transaction, "sbxtx_") || r.Network != "sandbox" {
		t.Errorf("the transaction is the sandbox rail's, from its own ledger: %q on %q", r.Transaction, r.Network)
	}
	var body map[string]any
	if err := json.Unmarshal(a.Body, &body); err != nil || body["mint"] != "SOL" || body["sandbox"] != true {
		t.Errorf("the caller gets the provider's real response: %s", a.Body)
	}
	if a.Quality == nil || a.Quality.FinalQuality == nil || *a.Quality.FinalQuality != 100 || a.Quality.SchemaValid == nil || !*a.Quality.SchemaValid {
		t.Errorf("a response that satisfies the capability's schema scores full marks: %+v", a.Quality)
	}
	// The coordinator confirmed the payment with the rail itself.
	ev := a.Intent.Reservations[0].Evidence
	st, err := w.rail.Settlement(context.Background(), ev)
	if err != nil || st.Status != app.SettlementSettled || st.AmountMinor != 3_000 || st.Transaction != r.Transaction {
		t.Errorf("rail ledger: %+v %v", st, err)
	}
	// The signed receipt commits to exactly what was observed, and says it is a test.
	c, err := receipt.VerifyIntent(a.Intent.Receipt, w.signer.JWKS())
	if err != nil {
		t.Fatal(err)
	}
	if c.Settlement.Transaction != r.Transaction || c.Execution.ResultHash != r.ResponseHash || c.Execution.RequestHash != r.RequestHash ||
		c.Execution.Status != "fulfilled" || !c.Test || c.Provider.ID != "alpha" {
		t.Errorf("receipt: %+v", c)
	}
	// An identical request now is refused rather than paid again.
	_, err = w.run(t, in.ID, "alpha")
	if err == nil || !strings.Contains(err.Error(), "already_committed") {
		t.Fatalf("a committed outcome must never be paid for twice, got %v", err)
	}
	v, _ := w.svc.View(context.Background(), in.ID, false)
	if v.BlockedAttempts != 1 || v.CommittedMinor != 3_000 || v.Attempts != 1 {
		t.Errorf("one payment, one blocked duplicate: attempts=%d blocked=%d committed=%d", v.Attempts, v.BlockedAttempts, v.CommittedMinor)
	}
}

// The provider charges, stores the result and then drops the response. The
// executor asks again with the same idempotency key and no payment, gets the
// stored result, and the intent commits with one payment and a result.
func TestE2E_LostResponseIsRecoveredWithoutPayingTwice(t *testing.T) {
	w := newWorld(t, time.Minute, 10_000_000)
	w.faults.set("/alpha", "drop_response")
	in := w.intent(t, 50_000)

	rep, err := w.run(t, in.ID, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Delivered {
		t.Fatalf("the result should have been recovered: %+v", rep.Attempts[0].Result)
	}
	a := rep.Attempts[0]
	if a.Intent.State != econ.StateCommitted || a.Intent.CommittedMinor != 3_000 || len(a.Intent.Reservations) != 1 {
		t.Errorf("exactly one payment: %s %d (%d attempts)", a.Intent.State, a.Intent.CommittedMinor, len(a.Intent.Reservations))
	}
	if !strings.Contains(string(a.Body), `"risk_score"`) {
		t.Errorf("body: %s", a.Body)
	}
	if a.Result.HTTPStatus != 200 {
		t.Errorf("the replay's status is what was delivered: %d", a.Result.HTTPStatus)
	}
}

// The provider fails before capturing. Nothing was charged, but the signed
// payment could still be captured until it expires, so the executor must not
// fall back yet. Once the rail can prove the payment can never land, the
// intent reopens and the fallback provider is paid instead.
func TestE2E_FallbackAfterTheRailProvesNothingMoved(t *testing.T) {
	w := newWorld(t, 1200*time.Millisecond, 10_000_000)
	w.faults.set("/alpha", "fail_before_capture")
	in := w.intent(t, 50_000)

	rep, err := w.run(t, in.ID, "alpha", "beta")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Delivered || !rep.Pending || len(rep.Attempts) != 1 || rep.Stopped != "outcome_unknown" {
		t.Fatalf("while the first payment could still land, no fallback: delivered=%v pending=%v attempts=%d stopped=%s",
			rep.Delivered, rep.Pending, len(rep.Attempts), rep.Stopped)
	}
	first := rep.Attempts[0].Result
	if first.Provider != "alpha" || first.Payment != routing.PaymentUnknown || first.Failure == nil {
		t.Errorf("first attempt: %+v", first)
	}
	// And the executor really did not touch the second provider.
	if _, err := w.run(t, in.ID, "beta"); err == nil {
		t.Fatal("a new attempt must be refused while the outcome is unknown")
	}

	// The payment authority's deadline passes; the sweeper's reconciliation
	// can now prove that it never landed.
	time.Sleep(1500 * time.Millisecond)
	if _, err := w.svc.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	v, _ := w.svc.View(context.Background(), in.ID, false)
	if v.State != econ.StateOpen || v.Commitment != econ.CommitmentNone || v.CommittedMinor != 0 {
		t.Fatalf("proven no money moved, so the intent reopens: %s/%s/%d", v.State, v.Commitment, v.CommittedMinor)
	}

	// Fallback.
	rep2, err := w.run(t, in.ID, "beta")
	if err != nil {
		t.Fatal(err)
	}
	if !rep2.Delivered || rep2.Attempts[0].Result.Provider != "beta" || rep2.Attempts[0].Result.Attempt != 2 {
		t.Fatalf("beta should now deliver as attempt 2: %+v", rep2.Attempts[0].Result)
	}
	final, _ := w.svc.View(context.Background(), in.ID, true)
	if final.State != econ.StateCommitted || final.CommittedMinor != 3_000 {
		t.Errorf("one commitment: %s %d", final.State, final.CommittedMinor)
	}
	// The history shows both attempts and why the first one didn't commit.
	hist, err := w.exec.Executions(context.Background(), "user_1", in.ID)
	if err != nil || len(hist) != 2 {
		t.Fatalf("history: %v %d", err, len(hist))
	}
	if hist[0].Result.Provider != "alpha" || hist[0].Result.Payment != routing.PaymentNotSettled || hist[0].Result.Succeeded() {
		t.Errorf("the sweeper's later proof must be reflected in the first attempt's record: %+v", hist[0].Result)
	}
	if hist[1].Result.Payment != routing.PaymentSettled || !hist[1].Result.Succeeded() {
		t.Errorf("second: %+v", hist[1].Result)
	}
	c, err := receipt.VerifyIntent(final.Receipt, w.signer.JWKS())
	if err != nil || c.Provider.ID != "beta" || c.Reservation.Attempt != 2 || !c.Coordination.ReconciliationRequired {
		t.Errorf("the receipt is for the attempt that committed and shows the reconciliation: %v %+v", err, c)
	}
}

// The pass budget is the person's hard limit on this agent. It is checked
// again when each attempt is reserved, counting what earlier intents already
// committed, so an agent can't spend past it by asking for many small things.
func TestE2E_AnAgentWithoutBudgetIsStoppedBeforeAnyPayment(t *testing.T) {
	w := newWorld(t, time.Minute, 5_000) // the pass allows 0.005 USDC in total; each call costs 0.003
	first := w.intent(t, 3_000)
	if rep, err := w.run(t, first.ID, "alpha"); err != nil || !rep.Delivered {
		t.Fatalf("the first call fits the budget: %v", err)
	}

	second, _, err := w.svc.CreateIntent(context.Background(), "agent_1", econ.Spec{
		Capability: "solana.token-risk", Input: json.RawMessage(`{"mint":"JUP"}`), Window: "e2e", Currency: "USDC", BudgetMaxMinor: 3_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.run(t, second.ID, "alpha")
	var nr *app.NoRoute
	if !asNoRoute(err, &nr) || len(nr.Rejected) == 0 || nr.Rejected[0].Code != routing.RejectPolicy {
		t.Fatalf("the second would take the agent past its pass: want a policy rejection, got %v", err)
	}
	v, _ := w.svc.View(context.Background(), second.ID, false)
	if v.State != econ.StateOpen || v.Attempts != 0 {
		t.Errorf("no attempt was made: %s %d", v.State, v.Attempts)
	}
}

func asNoRoute(err error, target **app.NoRoute) bool {
	nr, ok := err.(*app.NoRoute)
	if ok {
		*target = nr
	}
	return ok
}
