package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/receipt"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
)

// memExecStore is an in-memory ExecutionStore.
type memExecStore struct {
	mu   sync.Mutex
	recs map[string]StoredExecution
}

func newMemExecStore() *memExecStore { return &memExecStore{recs: map[string]StoredExecution{}} }

func (m *memExecStore) SaveResult(_ context.Context, _ string, rec StoredExecution) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recs[rec.Result.ID] = rec
	return nil
}

func (m *memExecStore) ForIntent(_ context.Context, intentID string) ([]StoredExecution, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []StoredExecution
	for _, r := range m.recs {
		if r.Result.IntentID == intentID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Result.Attempt < out[j].Result.Attempt })
	return out, nil
}

func (m *memExecStore) Recent(_ context.Context, candidateIDs []string, since time.Time, depth int) (map[string][]StoredExecution, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string][]StoredExecution{}
	for _, r := range m.recs {
		if slices.Contains(candidateIDs, r.Result.CandidateID) && !r.Result.StartedAt.Before(since) {
			out[r.Result.CandidateID] = append(out[r.Result.CandidateID], r)
		}
	}
	for id, recs := range out {
		sort.Slice(recs, func(i, j int) bool { return recs[i].Result.StartedAt.After(recs[j].Result.StartedAt) })
		if len(recs) > depth {
			recs = recs[:depth]
		}
		out[id] = recs
	}
	return out, nil
}

func (m *memExecStore) ForReservation(_ context.Context, reservationID string) (*StoredExecution, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.recs {
		if r.Result.ReservationID == reservationID {
			cp := r
			return &cp, nil
		}
	}
	return nil, shared.ErrNotFound
}

// fakeRunner is a controllable x402-style runner: per provider, a cost and a
// behaviour standing in for "what the provider and the network did".
type fakeRunner struct {
	mu        sync.Mutex
	costs     map[string]int64
	latencies map[string]int // expected milliseconds per provider; zero means 300
	quoteErrs map[string]error
	behaviors map[string]func(ctx context.Context, call StepCall) StepObservation
	runs      map[string]int
	quotes    map[string]int

	// quotePanics makes pricing a provider panic; quoteDelay makes every
	// price take that long (or until the caller gives up), quoteDelays one
	// provider's; maxInflight is the most prices that were being worked out
	// at once, and quoteCancelled counts prices the router gave up on.
	quotePanics    map[string]bool
	quoteDelay     time.Duration
	quoteDelays    map[string]time.Duration
	quoteCancelled int
	inflight       int
	maxInflight    int
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{
		costs: map[string]int64{}, latencies: map[string]int{}, quotePanics: map[string]bool{}, quoteErrs: map[string]error{}, quoteDelays: map[string]time.Duration{},
		behaviors: map[string]func(context.Context, StepCall) StepObservation{}, runs: map[string]int{}, quotes: map[string]int{},
	}
}

// quoted is how many times a provider was asked for a price.
func (f *fakeRunner) quoted(provider string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.quotes[provider]
}

func (f *fakeRunner) Type() routing.ExecutionType        { return routing.ExecX402 }
func (f *fakeRunner) Rail(routing.Quote) (string, error) { return "sandbox", nil }
func (f *fakeRunner) ran(provider string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runs[provider]
}

func (f *fakeRunner) Quote(ctx context.Context, c routing.Candidate, _ json.RawMessage) (routing.Quote, error) {
	f.mu.Lock()
	f.quotes[c.Provider]++
	panics, delay, latency := f.quotePanics[c.Provider], f.quoteDelay, f.latencies[c.Provider]
	if d, ok := f.quoteDelays[c.Provider]; ok {
		delay = d
	}
	f.inflight++
	f.maxInflight = max(f.maxInflight, f.inflight)
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.inflight--
		f.mu.Unlock()
	}()
	if panics {
		panic("the provider's pricing blew up")
	}
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			f.mu.Lock()
			f.quoteCancelled++
			f.mu.Unlock()
			return routing.Quote{}, ctx.Err()
		}
	}
	if err := f.quoteErrs[c.Provider]; err != nil {
		return routing.Quote{}, err
	}
	cost := f.costs[c.Provider]
	if cost == 0 {
		cost = 3_000
	}
	if latency == 0 {
		latency = 300
	}
	return routing.Quote{
		Cost: routing.Cost{ProviderMinor: cost}, Asset: "USDC", Network: "sandbox", PayTo: "payee-" + c.Provider,
		Semantics: econ.SemanticsPrepaidExact, EstimatedLatencyMS: latency,
	}, nil
}

func (f *fakeRunner) Run(ctx context.Context, call StepCall) StepObservation {
	f.mu.Lock()
	f.runs[call.Quote.Provider]++
	b := f.behaviors[call.Quote.Provider]
	f.mu.Unlock()
	if b == nil {
		return StepObservation{Class: routing.FailProvider, Message: "no behaviour scripted"}
	}
	return b(ctx, call)
}

type execRig struct {
	*econRig
	exec   *ExecutionService
	runner *fakeRunner
	store  *memExecStore
}

func newExecRig(t *testing.T) *execRig {
	t.Helper()
	rig := &execRig{econRig: newEconRig(t, 1, 10_000_000), runner: newFakeRunner(), store: newMemExecStore()}
	rig.exec = NewExecutionService(rig.econRig.svc, rig.store)
	rig.exec.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	rig.exec.RegisterRunner(rig.runner)
	return rig
}

const goodBody = `{"mint":"SOL","risk_score":12,"sandbox":true}`

// delivers is a provider that charges and delivers: payment authority is
// released, the payment lands, the body comes back.
func (r *execRig) delivers(body string) func(context.Context, StepCall) StepObservation {
	return func(ctx context.Context, call StepCall) StepObservation {
		auth, err := call.Pay(ctx, PaymentRequest{})
		if err != nil {
			return StepObservation{Class: routing.FailPayment, Message: err.Error()}
		}
		r.rail.land(&call.Reservation, auth.AmountMinor)
		return StepObservation{
			AuthorityReleased: true, Delivered: true, HTTPStatus: 200, ContentType: "application/json", Body: []byte(body),
			RequestHash: "sha256:request", SettleTx: "tx_" + call.Reservation.ID,
		}
	}
}

// fails503AfterPaying releases payment authority, then the provider errors
// without capturing: whether the payment can still land is the rail's to say.
func fails503AfterPaying(ctx context.Context, call StepCall) StepObservation {
	if _, err := call.Pay(ctx, PaymentRequest{}); err != nil {
		return StepObservation{Class: routing.FailPayment, Message: err.Error()}
	}
	return StepObservation{AuthorityReleased: true, HTTPStatus: 503, Class: routing.FailProvider, Message: "provider unavailable"}
}

func cand(provider string) routing.Candidate {
	return routing.Candidate{
		Capability: "solana.token-risk", Provider: provider, ExecutionType: routing.ExecX402,
		Endpoint: "https://" + provider + ".example.com/risk", Method: "POST", Network: "sandbox",
		Sources: []routing.DiscoverySource{routing.SourceConfigured},
	}
}

func (r *execRig) run(t *testing.T, intentID string, providers ...string) (*PlanReport, error) {
	t.Helper()
	var cs []routing.Candidate
	for _, p := range providers {
		cs = append(cs, cand(p))
	}
	return r.exec.ExecuteCandidates(context.Background(), CandidatesRequest{AgentID: "agent_1", IntentID: intentID, Candidates: cs})
}

func TestExecution_HappyPathCommitsVerifiesAndSignsAReceipt(t *testing.T) {
	rig := newExecRig(t)
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)
	in := rig.intent(t, "agent_1", 50_000)

	rep, err := rig.run(t, in.ID, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Delivered || rep.Stopped != "committed" || len(rep.Attempts) != 1 {
		t.Fatalf("report: delivered=%v stopped=%s attempts=%d", rep.Delivered, rep.Stopped, len(rep.Attempts))
	}
	a := rep.Attempts[0]
	r := a.Result
	if r.Payment != routing.PaymentSettled || r.Delivery != econ.FulfillmentFulfilled || !r.Succeeded() || r.ActualCostMinor != 3_000 || r.QuotedCostMinor != 3_000 {
		t.Errorf("result: %+v", r)
	}
	if r.Transaction != "tx_"+r.ReservationID || r.PlanRank != 1 || r.Mode != routing.ModeAuto || r.PlanHash != rep.Plan.Hash || r.QuoteHash == "" {
		t.Errorf("provenance: %+v", r)
	}
	if r.ResponseHash != econ.HashBytes([]byte(goodBody)) || r.RequestHash != "sha256:request" {
		t.Errorf("hashes: %+v", r)
	}
	if a.Quality == nil || a.Quality.FinalQuality == nil || *a.Quality.FinalQuality != 100 || a.Quality.Evaluator != "generic@1" {
		t.Errorf("quality: %+v", a.Quality)
	}
	if string(a.Body) != goodBody || a.ContentType != "application/json" {
		t.Errorf("the caller gets the provider's response: %q", a.Body)
	}
	if a.Intent.State != econ.StateCommitted || a.Intent.Fulfillment != econ.FulfillmentFulfilled {
		t.Errorf("intent: %s/%s", a.Intent.State, a.Intent.Fulfillment)
	}

	// Stored: hashes and verdicts, never the body.
	stored, _ := rig.store.ForReservation(context.Background(), r.ReservationID)
	if stored == nil || stored.Result.ID != r.ID || stored.Result.Payment != routing.PaymentSettled || stored.Quality == nil {
		t.Fatalf("stored: %+v", stored)
	}
	if b, _ := json.Marshal(stored); strings.Contains(string(b), "risk_score") {
		t.Error("a response body must never be persisted")
	}
	// The receipt was signed at commit and commits to the same hashes.
	c, err := receipt.VerifyIntent(a.Intent.Receipt, rig.signer.JWKS())
	if err != nil || c.Execution.ResultHash != r.ResponseHash || c.Execution.RequestHash != "sha256:request" || c.Final.Fulfillment != "FULFILLED" {
		t.Errorf("receipt: %v %+v", err, c)
	}
	// And it says how the provider was chosen and how the result was judged.
	if c.Routing == nil || c.Routing.Mode != "AUTO" || c.Routing.PlanHash != rep.Plan.Hash || c.Routing.QuoteHash != r.QuoteHash ||
		c.Routing.CandidateID != r.CandidateID || c.Routing.Rank != 1 || c.Routing.Fallback {
		t.Errorf("receipt routing: %+v", c.Routing)
	}
	if q := c.Execution.Quality; q == nil || q.Evaluator != "generic@1" || q.Score == nil || *q.Score != 100 || q.SchemaValid == nil || !*q.SchemaValid {
		t.Errorf("receipt quality: %+v", c.Execution.Quality)
	}
	// Events tell the routing story.
	v, _ := rig.econRig.svc.View(context.Background(), in.ID, true)
	var seen []string
	for _, e := range v.Events {
		seen = append(seen, e.Event)
	}
	for _, want := range []string{"routing.plan_selected", "reservation.acquired", "execution.started", "payment.authorized", "payment.confirmed", "intent.committed", "routing.attempt_result"} {
		if !slices.Contains(seen, want) {
			t.Errorf("missing event %s in %v", want, seen)
		}
	}
}

func TestExecution_SchemaInvalidResponseIsPaidButNotFulfilled(t *testing.T) {
	rig := newExecRig(t)
	rig.exec.SetCapabilities(StaticCatalog{"solana.token-risk": {
		ID: "solana.token-risk", Kind: routing.KindData,
		OutputSchema: json.RawMessage(`{"type":"object","required":["risk_score","mint"],"properties":{"risk_score":{"type":"number"},"mint":{"type":"string"}}}`),
	}})
	rig.runner.behaviors["alpha"] = rig.delivers(`{"mint":"SOL","risk_score":"high"}`) // wrong type
	in := rig.intent(t, "agent_1", 50_000)

	rep, err := rig.run(t, in.ID, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	a := rep.Attempts[0]
	if rep.Delivered {
		t.Error("an invalid response is not a delivery")
	}
	if a.Intent.State != econ.StateCommitted || a.Intent.Fulfillment != econ.FulfillmentNotFulfilled {
		t.Errorf("money moved and the result is unusable: %s/%s", a.Intent.State, a.Intent.Fulfillment)
	}
	r := a.Result
	if r.Payment != routing.PaymentSettled || r.Delivery != econ.FulfillmentNotFulfilled || r.Failure == nil || r.Failure.Class != routing.FailInvalid || r.Succeeded() {
		t.Errorf("result: %+v failure=%+v", r, r.Failure)
	}
	if a.Quality.SchemaValid == nil || *a.Quality.SchemaValid || *a.Quality.FinalQuality > 40 || !slices.Contains(a.Quality.Flags, "schema_invalid") {
		t.Errorf("quality: %+v", a.Quality)
	}
	if a.Quality.Completeness == nil || *a.Quality.Completeness != 1 {
		t.Errorf("both required fields are present: %+v", a.Quality.Completeness)
	}
}

func TestExecution_FailureBeforePaymentAuthorityMovesNoMoney(t *testing.T) {
	rig := newExecRig(t)
	rig.runner.behaviors["alpha"] = func(context.Context, StepCall) StepObservation {
		return StepObservation{HTTPStatus: 503, Class: routing.FailQuote, Message: "provider changed its terms"}
	}
	in := rig.intent(t, "agent_1", 50_000)

	rep, err := rig.run(t, in.ID, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	r := rep.Attempts[0].Result
	if rep.Delivered || rep.Attempts[0].Intent.State != econ.StateOpen {
		t.Fatalf("the intent must reopen: delivered=%v state=%s", rep.Delivered, rep.Attempts[0].Intent.State)
	}
	if r.Payment != routing.PaymentNotAttempted || r.ActualCostMinor != 0 || r.Failure == nil || r.Failure.Class != routing.FailQuote {
		t.Errorf("result: %+v failure=%+v", r, r.Failure)
	}
	if rig.rail.authorized.Load() != 0 {
		t.Errorf("no payment authority may have been released, got %d", rig.rail.authorized.Load())
	}
	if rep.Stopped != "no_more_candidates" {
		t.Errorf("stopped = %s", rep.Stopped)
	}
	if a := rep.Attempts[0]; a.Quality == nil || a.Quality.FinalQuality == nil || *a.Quality.FinalQuality != 0 {
		t.Errorf("a failed attempt is a known zero, not unknown: %+v", a.Quality)
	}
}

func TestExecution_PaidButNoResponseIsPaymentConfirmedResultUnknown(t *testing.T) {
	rig := newExecRig(t)
	rig.runner.behaviors["alpha"] = func(ctx context.Context, call StepCall) StepObservation {
		auth, err := call.Pay(ctx, PaymentRequest{})
		if err != nil {
			return StepObservation{Class: routing.FailPayment, Message: err.Error()}
		}
		rig.rail.land(&call.Reservation, auth.AmountMinor) // captured, then the response was lost
		return StepObservation{AuthorityReleased: true, Class: routing.FailTimeout, Message: "no answer"}
	}
	in := rig.intent(t, "agent_1", 50_000)

	rep, err := rig.run(t, in.ID, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	a := rep.Attempts[0]
	if a.Intent.State != econ.StateCommitted || a.Intent.Fulfillment != econ.FulfillmentUnknown {
		t.Fatalf("the rail proved payment, so the intent commits; the result is unknown: %s/%s", a.Intent.State, a.Intent.Fulfillment)
	}
	r := a.Result
	if r.Payment != routing.PaymentSettled || r.Delivery != econ.FulfillmentUnknown || r.Failure == nil || r.Failure.Class != routing.FailTimeout || rep.Delivered {
		t.Errorf("result: %+v failure=%+v", r, r.Failure)
	}
	if rep.Stopped != "committed" {
		t.Errorf("a committed intent is never retried: %s", rep.Stopped)
	}
}

func TestExecution_AmbiguousOutcomeBlocksFallbackUntilReconciled(t *testing.T) {
	rig := newExecRig(t)
	rig.runner.behaviors["alpha"] = fails503AfterPaying // the payment could still land
	rig.runner.behaviors["beta"] = rig.delivers(goodBody)
	in := rig.intent(t, "agent_1", 50_000)

	rep, err := rig.run(t, in.ID, "alpha", "beta")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Pending || rep.Stopped != "outcome_unknown" || rep.Delivered {
		t.Fatalf("an unresolved attempt must stop the plan: pending=%v stopped=%s", rep.Pending, rep.Stopped)
	}
	if rig.runner.ran("beta") != 0 {
		t.Fatal("the fallback must NOT run while the first payment could still land: that is how an agent pays twice")
	}
	if len(rep.Attempts) != 1 || rep.Attempts[0].Result.Payment != routing.PaymentUnknown {
		t.Errorf("attempts: %+v", rep.Attempts)
	}
	if st := rep.Intent.State; st != econ.StateReconciling && st != econ.StateUnknown {
		t.Errorf("intent state = %s", st)
	}
}

func TestExecution_FallbackRunsOnlyAfterProofThatNoMoneyMoved(t *testing.T) {
	rig := newExecRig(t)
	rig.rail.missing = SettlementNotSettled // the rail can prove the first payment can never land
	rig.runner.behaviors["alpha"] = fails503AfterPaying
	rig.runner.behaviors["beta"] = rig.delivers(goodBody)
	rig.runner.costs["beta"] = 4_000
	in := rig.intent(t, "agent_1", 50_000)

	rep, err := rig.run(t, in.ID, "alpha", "beta")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Delivered || rep.Stopped != "committed" || len(rep.Attempts) != 2 {
		t.Fatalf("delivered=%v stopped=%s attempts=%d", rep.Delivered, rep.Stopped, len(rep.Attempts))
	}
	first, second := rep.Attempts[0].Result, rep.Attempts[1].Result
	if first.Provider != "alpha" || first.Payment != routing.PaymentNotSettled || first.PlanRank != 1 || first.Succeeded() {
		t.Errorf("first: %+v", first)
	}
	if second.Provider != "beta" || second.Payment != routing.PaymentSettled || second.PlanRank != 2 || !second.Succeeded() || second.ActualCostMinor != 4_000 {
		t.Errorf("second: %+v", second)
	}
	if rep.Intent.CommittedMinor != 4_000 {
		t.Errorf("exactly one commitment, for the second provider's price: %d", rep.Intent.CommittedMinor)
	}
	v, _ := rig.econRig.svc.View(context.Background(), in.ID, true)
	fallback := false
	for _, e := range v.Events {
		if e.Event == "routing.fallback" && e.Data["from_provider"] == "alpha" {
			fallback = true
		}
	}
	if !fallback {
		t.Error("the fallback must be on the record")
	}
	c, _ := receipt.VerifyIntent(v.Receipt, rig.signer.JWKS())
	if c == nil || c.Provider.ID != "beta" || c.Reservation.Attempt != 2 {
		t.Fatalf("the receipt is for the attempt that committed: %+v", c)
	}
	if c.Routing == nil || !c.Routing.Fallback || c.Routing.Rank != 2 {
		t.Errorf("the receipt must show that this was a fallback: %+v", c.Routing)
	}
}

func TestExecution_PolicyDenialOfOneProviderFallsToAnother(t *testing.T) {
	rig := newExecRig(t)
	// The person's Spend Pass only allows provider beta.
	_ = rig.passes.Create(context.Background(), &spendpass.Pass{
		ID: "pass_agent_1", UserID: "user_1", AgentID: "agent_1", Label: "x", AgentKind: spendpass.AgentCustom, Currency: "USDC",
		BudgetMinorUnits: 10_000_000, BudgetPeriod: spendpass.PeriodTotal, AllowedMerchants: []string{"beta"},
		CreatedAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(24 * time.Hour),
	})
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)
	rig.runner.behaviors["beta"] = rig.delivers(goodBody)
	in := rig.intent(t, "agent_1", 50_000)

	rep, err := rig.run(t, in.ID, "alpha", "beta")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Delivered || len(rep.Attempts) != 1 || rep.Attempts[0].Result.Provider != "beta" {
		t.Fatalf("beta should have done it: %+v", rep.Attempts)
	}
	if rig.runner.ran("alpha") != 0 {
		t.Error("a provider the pass doesn't allow must never be called")
	}
	var denied bool
	for _, r := range rep.Rejected {
		if r.Provider == "alpha" && r.Code == routing.RejectPolicy {
			denied = true
		}
	}
	if !denied {
		t.Errorf("the denial should be listed: %+v", rep.Rejected)
	}
}

func TestExecution_ScreeningAgainstTheIntentsOwnLimits(t *testing.T) {
	rig := newExecRig(t)
	rig.runner.behaviors["cheap"] = rig.delivers(goodBody)
	rig.runner.costs["pricey"] = 60_000 // over the 50_000 ceiling
	rig.runner.quoteErrs["down"] = errors.New("provider unreachable")
	in := rig.intent(t, "agent_1", 50_000)

	rep, err := rig.run(t, in.ID, "pricey", "down", "cheap")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Delivered || rep.Attempts[0].Result.Provider != "cheap" {
		t.Fatalf("only the one within limits runs: %+v", rep.Attempts)
	}
	got := map[string]string{}
	for _, r := range rep.Rejected {
		got[r.Provider] = r.Code
	}
	if got["pricey"] != routing.RejectOverBudget || got["down"] != routing.RejectUnquotable {
		t.Errorf("rejections: %+v", got)
	}
	if rig.runner.ran("pricey") != 0 || rig.runner.ran("down") != 0 {
		t.Error("rejected candidates are never run")
	}
}

func TestExecution_NoRouteExplainsWhyEachCandidateWasLeftOut(t *testing.T) {
	rig := newExecRig(t)
	rig.runner.costs["pricey"] = 60_000
	in := rig.intent(t, "agent_1", 50_000)

	_, err := rig.run(t, in.ID, "pricey")
	var nr *NoRoute
	if !errors.As(err, &nr) || !errors.Is(err, ErrNoRoute) || len(nr.Rejected) != 1 || nr.Rejected[0].Code != routing.RejectOverBudget {
		t.Fatalf("want NoRoute with the reason, got %v", err)
	}
	// A candidate nothing can run is also just a rejection.
	mcp := cand("mcpy")
	mcp.ExecutionType = routing.ExecMCP
	_, err = rig.exec.ExecuteCandidates(context.Background(), CandidatesRequest{AgentID: "agent_1", IntentID: in.ID, Candidates: []routing.Candidate{mcp}})
	if !errors.As(err, &nr) {
		t.Fatalf("an unrunnable type is a rejection, got %v", err)
	}
}

func TestExecution_IntentConstraintsFilterCandidates(t *testing.T) {
	rig := newExecRig(t)
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)
	v, _, err := rig.econRig.svc.CreateIntent(context.Background(), "agent_1", econ.Spec{
		Capability: "solana.token-risk", Input: json.RawMessage(`{"mint":"JUP"}`), Window: "w2", Currency: "USDC", BudgetMaxMinor: 50_000,
		Constraints:    econ.Constraints{AllowedNetworks: []string{"solana"}, MinReliabilityPct: 90},
		ProviderPolicy: econ.ProviderPolicy{Strategy: "cheapest"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = rig.run(t, v.ID, "alpha") // the quote is on "sandbox", the intent allows only "solana"
	var nr *NoRoute
	if !errors.As(err, &nr) || nr.Rejected[0].Code != routing.RejectNetwork {
		t.Fatalf("want a network rejection, got %v", err)
	}

	// Reliability is judged from Algebra's own history, when it has enough.
	v2, _, _ := rig.econRig.svc.CreateIntent(context.Background(), "agent_1", econ.Spec{
		Capability: "solana.token-risk", Input: json.RawMessage(`{"mint":"BONK"}`), Window: "w3", Currency: "USDC", BudgetMaxMinor: 50_000,
		Constraints: econ.Constraints{MinReliabilityPct: 90},
	})
	flaky := cand("alpha")
	flaky.History = &routing.History{Calls: 20, SuccessRate: 0.6, ValidRate: 1, P50LatencyMS: 100, P95LatencyMS: 300, AvgQuality: 90}
	_, err = rig.exec.ExecuteCandidates(context.Background(), CandidatesRequest{AgentID: "agent_1", IntentID: v2.ID, Candidates: []routing.Candidate{flaky}})
	if !errors.As(err, &nr) || nr.Rejected[0].Code != routing.RejectReliability {
		t.Fatalf("want a reliability rejection, got %v", err)
	}
	// With too little history there is nothing to hold against it.
	fresh := cand("alpha")
	fresh.History = &routing.History{Calls: 2, SuccessRate: 0}
	rep, err := rig.exec.ExecuteCandidates(context.Background(), CandidatesRequest{AgentID: "agent_1", IntentID: v2.ID, Candidates: []routing.Candidate{fresh}})
	if err != nil || !rep.Delivered {
		t.Fatalf("a provider with no real record isn't excluded by one: %v", err)
	}
	// The strategy on the intent picks the plan's mode.
	if rep.Plan.Mode != routing.ModeAuto {
		t.Errorf("mode = %s", rep.Plan.Mode)
	}
}

func TestExecution_BookkeepingSurvivesTheCallerHangingUp(t *testing.T) {
	rig := newExecRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	rig.runner.behaviors["alpha"] = func(rctx context.Context, call StepCall) StepObservation {
		obs := rig.delivers(goodBody)(rctx, call)
		cancel() // the client disconnects after the money moved
		return obs
	}
	in := rig.intent(t, "agent_1", 50_000)

	rep, err := rig.exec.ExecuteCandidates(ctx, CandidatesRequest{AgentID: "agent_1", IntentID: in.ID, Candidates: []routing.Candidate{cand("alpha")}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Attempts[0].Intent.State != econ.StateCommitted {
		t.Fatalf("the outcome must be recorded even though the caller is gone: %s", rep.Attempts[0].Intent.State)
	}
	if st, _ := rig.store.ForReservation(context.Background(), rep.Attempts[0].Result.ReservationID); st == nil || st.Result.Payment != routing.PaymentSettled {
		t.Errorf("the stored record must be final: %+v", st)
	}
}

func TestExecution_ARunnerPanicLeavesNothingStuck(t *testing.T) {
	rig := newExecRig(t)
	rig.runner.behaviors["alpha"] = func(context.Context, StepCall) StepObservation { panic("boom") }
	in := rig.intent(t, "agent_1", 50_000)

	rep, err := rig.run(t, in.ID, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	a := rep.Attempts[0]
	if a.Intent.State != econ.StateOpen {
		t.Fatalf("a crash before any payment authority leaves the intent open, not stuck: %s", a.Intent.State)
	}
	if a.Result.Failure == nil || a.Result.Failure.Class != routing.FailAmbiguous || a.Result.Payment != routing.PaymentNotAttempted {
		t.Errorf("result: %+v failure=%+v", a.Result, a.Result.Failure)
	}
}

func TestExecution_FreeDeliveryMovesNoMoney(t *testing.T) {
	rig := newExecRig(t)
	rig.runner.behaviors["alpha"] = func(context.Context, StepCall) StepObservation {
		return StepObservation{Delivered: true, HTTPStatus: 200, ContentType: "application/json", Body: []byte(goodBody)}
	}
	in := rig.intent(t, "agent_1", 50_000)

	rep, err := rig.run(t, in.ID, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	r := rep.Attempts[0].Result
	if r.Payment != routing.PaymentNotAttempted || r.Delivery != econ.FulfillmentFulfilled || r.ActualCostMinor != 0 || !r.Succeeded() {
		t.Errorf("a provider that answered without charging: %+v", r)
	}
	if rig.rail.authorized.Load() != 0 {
		t.Error("no payment authority for a free answer")
	}
}

func TestExecution_AFailingEvaluatorDoesNotFailTheAttempt(t *testing.T) {
	rig := newExecRig(t)
	// Bypass NewStaticCatalog, which would refuse this schema: this is the
	// "it got through anyway" case.
	rig.exec.SetCapabilities(StaticCatalog{"solana.token-risk": {ID: "solana.token-risk", OutputSchema: json.RawMessage(`{"$ref":"#/$defs/missing"}`)}})
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)
	in := rig.intent(t, "agent_1", 50_000)

	rep, err := rig.run(t, in.ID, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	a := rep.Attempts[0]
	if !rep.Delivered || a.Intent.State != econ.StateCommitted {
		t.Fatalf("the money question is settled elsewhere: %s", a.Intent.State)
	}
	if !slices.Contains(a.Quality.Flags, "evaluator_error") || a.Quality.FinalQuality != nil {
		t.Errorf("the result is unjudged and says so: %+v", a.Quality)
	}
}

func TestExecution_RefusesStaleQuotesAndForeignIntents(t *testing.T) {
	rig := newExecRig(t)
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)
	in := rig.intent(t, "agent_1", 50_000)
	q, err := rig.exec.Quote(context.Background(), "agent_1", in.ID, cand("alpha"))
	if err != nil {
		t.Fatal(err)
	}
	if q.CandidateID == "" || q.ID == "" || q.Cost.ProviderMinor != 3_000 || !q.ValidUntil.After(q.QuotedAt) || !q.Test {
		t.Errorf("quote: %+v", q)
	}

	rig.exec.now = func() time.Time { return time.Now().Add(DefaultQuoteTTL + time.Second) }
	_, err = rig.exec.Execute(context.Background(), ExecuteRequest{AgentID: "agent_1", IntentID: in.ID, Step: routing.PlanStep{Rank: 1, Quote: q}})
	if !errors.Is(err, shared.ErrConflict) {
		t.Errorf("a stale quote must be refused, got %v", err)
	}
	rig.exec.now = time.Now
	if rig.rail.authorized.Load() != 0 || rig.runner.ran("alpha") != 0 {
		t.Error("nothing may happen on a stale quote")
	}

	// A quote for another capability can't be run against this intent.
	other := q
	other.Capability = "swap.spot"
	if _, err := rig.exec.Execute(context.Background(), ExecuteRequest{AgentID: "agent_1", IntentID: in.ID, Step: routing.PlanStep{Rank: 1, Quote: other}}); !errors.Is(err, shared.ErrConflict) {
		t.Errorf("a quote for the wrong capability must be refused, got %v", err)
	}
	// An unknown agent has no intents.
	if _, err := rig.exec.Quote(context.Background(), "agent_nobody", in.ID, cand("alpha")); err == nil {
		t.Error("an unknown agent must not be able to quote")
	}
}

func TestExecution_ListsAttemptsOnlyForTheOwner(t *testing.T) {
	rig := newExecRig(t)
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)
	in := rig.intent(t, "agent_1", 50_000)
	if _, err := rig.run(t, in.ID, "alpha"); err != nil {
		t.Fatal(err)
	}
	recs, err := rig.exec.Executions(context.Background(), "user_1", in.ID)
	if err != nil || len(recs) != 1 || recs[0].Result.Provider != "alpha" {
		t.Fatalf("owner sees the attempt: %v %+v", err, recs)
	}
	if _, err := rig.exec.Executions(context.Background(), "someone_else", in.ID); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("another person must not see it: %v", err)
	}
}

func TestGenericEvaluator(t *testing.T) {
	ev := GenericEvaluator{}
	ctx := context.Background()
	schema := json.RawMessage(`{"type":"object","required":["a","b"],"properties":{"a":{"type":"integer"}}}`)
	capab := routing.Capability{ID: "x.y", OutputSchema: schema}

	// No schema: well-formedness only, and it says so.
	q, err := ev.Evaluate(ctx, routing.Capability{ID: "x.y"}, EvalInput{Response: []byte(`{"ok":true}`), Delivered: true})
	if err != nil || q.SchemaValid == nil || !*q.SchemaValid || !slices.Contains(q.Flags, "no_schema") || q.SemanticQuality != nil || q.Freshness != nil {
		t.Errorf("no schema: %v %+v", err, q)
	}
	q, _ = ev.Evaluate(ctx, routing.Capability{ID: "x.y"}, EvalInput{Response: []byte(`<html>`), Delivered: true})
	if *q.SchemaValid || *q.FinalQuality > 40 {
		t.Errorf("not JSON: %+v", q)
	}
	q, _ = ev.Evaluate(ctx, routing.Capability{ID: "x.y"}, EvalInput{Response: []byte(`   `), Delivered: true})
	if *q.SchemaValid {
		t.Error("an empty body is not a valid response")
	}
	// With a schema.
	q, _ = ev.Evaluate(ctx, capab, EvalInput{Response: []byte(`{"a":1,"b":2}`), Delivered: true})
	if !*q.SchemaValid || *q.Completeness != 1 || *q.FinalQuality != 100 {
		t.Errorf("valid: %+v", q)
	}
	q, _ = ev.Evaluate(ctx, capab, EvalInput{Response: []byte(`{"a":1}`), Delivered: true})
	if *q.SchemaValid || *q.Completeness != 0.5 {
		t.Errorf("one required field missing: %+v", q)
	}
	q, _ = ev.Evaluate(ctx, capab, EvalInput{Response: []byte(`{"a":"x","b":2}`), Delivered: true})
	if *q.SchemaValid || *q.Completeness != 1 {
		t.Errorf("wrong type but complete: %+v", q)
	}
	// Not delivered: a known zero.
	q, _ = ev.Evaluate(ctx, capab, EvalInput{Delivered: false})
	if q.FinalQuality == nil || *q.FinalQuality != 0 || !slices.Contains(q.Flags, "not_delivered") {
		t.Errorf("not delivered: %+v", q)
	}
	// A broken schema is an error, and a remote $ref can't make Algebra fetch anything.
	if _, err := ev.Evaluate(ctx, routing.Capability{ID: "x.y", OutputSchema: json.RawMessage(`{"$ref":"https://evil.example.com/s.json"}`)}, EvalInput{Response: []byte(`{}`), Delivered: true}); err == nil {
		t.Error("a remote $ref must not be resolved")
	}
	if _, err := ev.Evaluate(ctx, routing.Capability{ID: "x.y", OutputSchema: json.RawMessage(`{nope`)}, EvalInput{Response: []byte(`{}`), Delivered: true}); err == nil {
		t.Error("a malformed schema is an error")
	}
}

// An attempt left ambiguous is later resolved by reconciliation, not by the
// executor; the record routing learns from must follow.
func TestExecution_ReconciliationBringsTheStoredRecordUpToDate(t *testing.T) {
	ctx := context.Background()

	t.Run("proven not to have moved money", func(t *testing.T) {
		rig := newExecRig(t)
		rig.runner.behaviors["alpha"] = fails503AfterPaying
		in := rig.intent(t, "agent_1", 50_000)
		rep, err := rig.run(t, in.ID, "alpha")
		if err != nil || !rep.Pending {
			t.Fatalf("setup: %v pending=%v", err, rep != nil && rep.Pending)
		}
		rid := rep.Attempts[0].Result.ReservationID
		if st, _ := rig.store.ForReservation(ctx, rid); st.Result.Payment != routing.PaymentUnknown {
			t.Fatalf("while ambiguous the record says so: %s", st.Result.Payment)
		}

		rig.rail.missing = SettlementNotSettled // the rail can now prove it
		if _, err := rig.econRig.svc.Reconcile(ctx, in.ID); err != nil {
			t.Fatal(err)
		}
		st, _ := rig.store.ForReservation(ctx, rid)
		if st.Result.Payment != routing.PaymentNotSettled || st.Result.ActualCostMinor != 0 || st.Result.Failure == nil || st.Result.Failure.Class != routing.FailProvider || st.Result.Succeeded() {
			t.Errorf("record after reconciliation: %+v failure=%+v", st.Result, st.Result.Failure)
		}
		if st.Quality == nil || st.Quality.FinalQuality == nil || *st.Quality.FinalQuality != 0 {
			t.Errorf("a failed attempt keeps its known zero: %+v", st.Quality)
		}
		v, _ := rig.econRig.svc.View(ctx, in.ID, true)
		resolved := false
		for _, e := range v.Events {
			if e.Event == "routing.attempt_resolved" {
				resolved = true
			}
		}
		if !resolved {
			t.Error("the resolution is on the record")
		}
	})

	t.Run("it had settled after all", func(t *testing.T) {
		rig := newExecRig(t)
		rig.runner.behaviors["alpha"] = func(ctx context.Context, call StepCall) StepObservation {
			obs := fails503AfterPaying(ctx, call)
			return obs
		}
		in := rig.intent(t, "agent_1", 50_000)
		rep, err := rig.run(t, in.ID, "alpha")
		if err != nil || !rep.Pending {
			t.Fatalf("setup: %v", err)
		}
		rid := rep.Attempts[0].Result.ReservationID
		rig.rail.land(&rep.Attempts[0].Intent.Reservations[0], 3_000) // the payment lands late

		if _, err := rig.econRig.svc.Reconcile(ctx, in.ID); err != nil {
			t.Fatal(err)
		}
		st, _ := rig.store.ForReservation(ctx, rid)
		if st.Result.Payment != routing.PaymentSettled || st.Result.ActualCostMinor != 3_000 || st.Result.Delivery != econ.FulfillmentUnknown || st.Result.Failure == nil {
			t.Errorf("paid, result unknown: %+v failure=%+v", st.Result, st.Result.Failure)
		}
		if st.Result.Transaction == "" {
			t.Error("the transaction the rail found is recorded")
		}
	})
}

// flakyEconStore fails every transaction once switched on: a database that
// goes away at the worst moment.
type flakyEconStore struct {
	EconStore
	fail atomic.Bool
}

func (f *flakyEconStore) Atomically(ctx context.Context, intentID, passID string, fn func(EconUnit) error) error {
	if f.fail.Load() {
		return errors.New("database unavailable")
	}
	return f.EconStore.Atomically(ctx, intentID, passID, fn)
}

// If the money moved and the result arrived but the outcome can't be recorded,
// the caller still gets what it paid for, and the attempt is left for the
// sweeper to reconcile against the rail.
func TestExecution_AResultIsNeverLostToABookkeepingFailure(t *testing.T) {
	ctx := context.Background()
	rig := newExecRig(t)
	flaky := &flakyEconStore{EconStore: rig.econRig.store}
	rig.econRig.svc.store = flaky
	rig.runner.behaviors["alpha"] = func(rctx context.Context, call StepCall) StepObservation {
		obs := rig.delivers(goodBody)(rctx, call)
		flaky.fail.Store(true) // the database goes away right after the money moved
		return obs
	}
	in := rig.intent(t, "agent_1", 50_000)

	rep, err := rig.run(t, in.ID, "alpha")
	if err == nil {
		t.Fatal("recording the outcome must have failed")
	}
	if rep == nil || len(rep.Attempts) != 1 || string(rep.Attempts[0].Body) != goodBody {
		t.Fatalf("the paid-for response must survive the error: %+v", rep)
	}
	r := rep.Attempts[0].Result
	if r.Payment != routing.PaymentUnknown || r.Delivery != econ.FulfillmentFulfilled || r.Failure == nil || r.Failure.Class != routing.FailAmbiguous {
		t.Errorf("honest about what is and isn't known: %+v failure=%+v", r, r.Failure)
	}

	// The database comes back. The sweeper finds the overdue attempt, the rail
	// proves the payment, and the intent commits: nothing was lost or doubled.
	flaky.fail.Store(false)
	later := time.Now().Add(DefaultExecutionTimeout + time.Minute)
	rig.econRig.svc.now = func() time.Time { return later }
	if _, err := rig.econRig.svc.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	v, _ := rig.econRig.svc.View(ctx, in.ID, false)
	if v.State != econ.StateCommitted || v.CommittedMinor != 3_000 {
		t.Fatalf("reconciliation commits what the rail proved: %s %d", v.State, v.CommittedMinor)
	}
	if rig.rail.authorized.Load() != 1 {
		t.Errorf("one payment authority throughout: %d", rig.rail.authorized.Load())
	}
	stored, _ := rig.store.ForReservation(ctx, r.ReservationID)
	if stored == nil || stored.Result.Payment != routing.PaymentSettled || stored.Result.ActualCostMinor != 3_000 {
		t.Errorf("and the stored record follows: %+v", stored)
	}
}

// Nothing leaves Algebra before the person says yes: not the result of the
// call, and not even the intent's input in a request for a price.
func TestExecution_NoProviderIsContactedBeforeApproval(t *testing.T) {
	ctx := context.Background()
	rig := newExecRig(t)
	zero := int64(0) // "ask me every time"
	_ = rig.passes.Create(ctx, &spendpass.Pass{
		ID: "pass_agent_1", UserID: "user_1", AgentID: "agent_1", Label: "x", AgentKind: spendpass.AgentCustom, Currency: "USDC",
		BudgetMinorUnits: 10_000_000, BudgetPeriod: spendpass.PeriodTotal, ApproveAboveMinorUnits: &zero,
		CreatedAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(24 * time.Hour),
	})
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)
	spec := econ.Spec{Capability: "solana.token-risk", Input: json.RawMessage(`{"mint":"SOL"}`), Window: "w", Currency: "USDC", BudgetMaxMinor: 50_000}

	_, err := rig.exec.Do(ctx, DoRequest{AgentID: "agent_1", Spec: spec, Candidates: []routing.Candidate{cand("alpha")}})
	var rej *ReservationRejected
	if !errors.As(err, &rej) || rej.Reason != RejectApproval {
		t.Fatalf("want approval_required, got %v", err)
	}
	if rig.runner.quoted("alpha") != 0 || rig.runner.ran("alpha") != 0 {
		t.Fatal("no provider may be contacted before approval, not even for a price")
	}

	// The person approves; asking again continues the same intent.
	v, _, _ := rig.econRig.svc.CreateIntent(ctx, "agent_1", spec)
	if _, err := rig.econRig.svc.Approve(ctx, "user_1", v.ID); err != nil {
		t.Fatal(err)
	}
	res, err := rig.exec.Do(ctx, DoRequest{AgentID: "agent_1", Spec: spec, Candidates: []routing.Candidate{cand("alpha")}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Created || res.Report.Intent.ID != v.ID || !res.Report.Delivered {
		t.Errorf("approval continues the same intent: created=%v delivered=%v", res.Created, res.Report.Delivered)
	}
}

func TestExecution_ProvidersTheSpendPassForbidsAreNotEvenProbed(t *testing.T) {
	ctx := context.Background()
	rig := newExecRig(t)
	_ = rig.passes.Create(ctx, &spendpass.Pass{
		ID: "pass_agent_1", UserID: "user_1", AgentID: "agent_1", Label: "x", AgentKind: spendpass.AgentCustom, Currency: "USDC",
		BudgetMinorUnits: 10_000_000, BudgetPeriod: spendpass.PeriodTotal, AllowedMerchants: []string{"beta"},
		CreatedAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(24 * time.Hour),
	})
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)
	rig.runner.behaviors["beta"] = rig.delivers(goodBody)
	in := rig.intent(t, "agent_1", 50_000)

	rep, err := rig.run(t, in.ID, "alpha", "beta")
	if err != nil || !rep.Delivered {
		t.Fatalf("beta does it: %v", err)
	}
	if rig.runner.quoted("alpha") != 0 {
		t.Error("a provider outside the pass must not receive a request, not even to quote a price")
	}
	if rig.runner.quoted("beta") != 1 {
		t.Errorf("beta was quoted %d times", rig.runner.quoted("beta"))
	}
}

func TestExecution_DoIsIdempotentPerOutcome(t *testing.T) {
	ctx := context.Background()
	rig := newExecRig(t)
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)
	spec := econ.Spec{Capability: "solana.token-risk", Input: json.RawMessage(`{ "mint": "SOL" }`), Window: "w", Currency: "USDC", BudgetMaxMinor: 50_000}
	req := DoRequest{AgentID: "agent_1", Spec: spec, Candidates: []routing.Candidate{cand("alpha")}}

	first, err := rig.exec.Do(ctx, req)
	if err != nil || !first.Created || !first.Report.Delivered {
		t.Fatalf("first: %v", err)
	}
	// The same outcome again, spelled differently: the same intent, and not paid for twice.
	req.Spec.Input = json.RawMessage(`{"mint":"SOL"}`)
	second, err := rig.exec.Do(ctx, req)
	var rej *ReservationRejected
	if !errors.As(err, &rej) || rej.Reason != RejectCommitted || !rej.Duplicate {
		t.Fatalf("want already_committed, got %v", err)
	}
	if second == nil || second.Created {
		t.Errorf("the existing intent is used: %+v", second)
	}
	if rig.rail.authorized.Load() != 1 {
		t.Errorf("exactly one payment authority, got %d", rig.rail.authorized.Load())
	}
}

func TestNewStaticCatalogRefusesBrokenCapabilities(t *testing.T) {
	good := routing.Capability{ID: "solana.token-risk", OutputSchema: json.RawMessage(`{"type":"object","required":["risk_score"]}`)}
	cat, err := NewStaticCatalog(good, routing.Capability{ID: "swap.spot", Kind: routing.KindTrade})
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := cat.Capability("solana.token-risk"); !ok || c.Kind != routing.KindData {
		t.Errorf("normalised on the way in: %+v %v", c, ok)
	}
	for name, bad := range map[string][]routing.Capability{
		"bad id":            {{ID: "x"}},
		"unresolvable $ref": {{ID: "a.b", OutputSchema: json.RawMessage(`{"$ref":"#/$defs/missing"}`)}},
		"remote $ref":       {{ID: "a.b", OutputSchema: json.RawMessage(`{"$ref":"https://evil.example.com/s.json"}`)}},
		"not a schema":      {{ID: "a.b", OutputSchema: json.RawMessage(`{"type": 5}`)}},
		"duplicate":         {good, good},
	} {
		if _, err := NewStaticCatalog(bad...); err == nil {
			t.Errorf("%s should be refused when the catalog is built", name)
		}
	}
}

func TestPlanReportFinal(t *testing.T) {
	var empty PlanReport
	if empty.Final() != nil {
		t.Error("no attempts, no final")
	}
	r := PlanReport{Attempts: []ExecutionReport{{Result: routing.ExecutionResult{Provider: "a"}}, {Result: routing.ExecutionResult{Provider: "b"}}}}
	if r.Final().Result.Provider != "b" {
		t.Error("final is the last attempt")
	}
}
