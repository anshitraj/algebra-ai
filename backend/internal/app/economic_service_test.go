package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/receipt"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
)

// fakeRail is a controllable payment rail: it records every payment
// authority it releases and answers settlement from what the test says
// happened on the "chain".
type fakeRail struct {
	mu         sync.Mutex
	settled    map[string]Settlement
	missing    SettlementStatus // answer for a payment the rail never saw
	authorized atomic.Int64
}

func newFakeRail() *fakeRail {
	return &fakeRail{settled: map[string]Settlement{}, missing: SettlementPending}
}

func (f *fakeRail) Name() string { return "sandbox" }

func (f *fakeRail) Settlement(_ context.Context, ev econ.Evidence) (Settlement, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.settled[ev.PaymentID]; ok {
		return s, nil
	}
	return Settlement{Status: f.missing, Detail: "not seen"}, nil
}

func (f *fakeRail) Authorize(_ context.Context, r *econ.Reservation, _ PaymentRequest) (*PaymentAuthority, error) {
	f.authorized.Add(1)
	amount := r.HoldMinor
	return &PaymentAuthority{Header: "X-PAYMENT", Value: "sbx." + r.ID, AmountMinor: amount, Evidence: econ.Evidence{
		Rail: "sandbox", Protocol: "x402", Scheme: "exact", Network: "sandbox", Asset: "USDC", PaymentID: "pay_" + r.ID, AmountMinor: amount, Test: true,
	}}, nil
}

// land records that a payment reached the chain.
func (f *fakeRail) land(r *econ.Reservation, amount int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settled["pay_"+r.ID] = Settlement{Status: SettlementSettled, AmountMinor: amount, Transaction: "tx_" + r.ID, Network: "sandbox", Asset: "USDC", Test: true}
}

type fakeRecovery struct{ answer Recovery }

func (f fakeRecovery) Recover(context.Context, econ.Reservation) (Recovery, error) {
	return f.answer, nil
}

type econRig struct {
	svc    *EconomicService
	store  *memEconStore
	passes *fakePassStore
	agents *fakeAgentStore
	rail   *fakeRail
	signer *receipt.Signer
}

// newEconRig: one principal (user_1) and n executors, each with its own
// USDC Spend Pass of `budget` micro-USDC.
func newEconRig(t *testing.T, n int, budget int64) *econRig {
	t.Helper()
	rig := &econRig{store: newMemEconStore(), passes: newFakePassStore(), agents: newFakeAgentStore(), rail: newFakeRail()}
	now := time.Now()
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("agent_%d", i)
		rig.agents.put(&agent.Identity{ID: id, UserID: "user_1", Name: "Executor " + id, ClientID: "spend-pass:custom"})
		_ = rig.passes.Create(context.Background(), &spendpass.Pass{
			ID: "pass_" + id, UserID: "user_1", AgentID: id, Label: id, AgentKind: spendpass.AgentCustom, Currency: "USDC",
			BudgetMinorUnits: budget, BudgetPeriod: spendpass.PeriodTotal, CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(24 * time.Hour),
		})
	}
	rig.svc = NewEconomicService(rig.store, rig.agents, rig.passes, nil)
	rig.svc.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	rig.svc.RegisterRail(rig.rail)
	signer, err := receipt.NewSigner([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	rig.signer = signer
	rig.svc.SetReceipts(NewIntentReceiptService(signer, "https://algebra.test", rig.agents, rig.store))
	return rig
}

func (rig *econRig) intent(t *testing.T, agentID string, max int64) *IntentView {
	t.Helper()
	v, _, err := rig.svc.CreateIntent(context.Background(), agentID, econ.Spec{
		Capability: "solana.token-risk", Input: json.RawMessage(`{"mint":"SOL"}`), Window: "2026-09-29T10", Currency: "USDC", BudgetMaxMinor: max,
	})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

var reserveReq = ReserveRequest{ProviderID: "x402:risk.example", Rail: "sandbox", QuoteMinor: 3_000}

func TestEconomic_ConcurrentExecutorsGetOneReservation(t *testing.T) {
	ctx := context.Background()
	rig := newEconRig(t, 3, 10_000_000)
	in := rig.intent(t, "agent_1", 50_000)

	const racers = 30
	var granted, dup atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			agentID := fmt.Sprintf("agent_%d", i%3+1)
			_, err := rig.svc.Reserve(ctx, agentID, in.ID, reserveReq)
			var rej *ReservationRejected
			switch {
			case err == nil:
				granted.Add(1)
			case errors.As(err, &rej) && rej.Duplicate:
				dup.Add(1)
			default:
				t.Errorf("unexpected: %v", err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	// The same executor re-asking gets its own reservation back, so the
	// winner's own retries count as granted, not blocked; every other
	// request was a blocked duplicate.
	v, _ := rig.svc.View(ctx, in.ID, true)
	live := 0
	for _, r := range v.Reservations {
		if r.State.Live() {
			live++
		}
	}
	if live != 1 || len(v.Reservations) != 1 {
		t.Fatalf("exactly one reservation may exist, got %d (live %d)", len(v.Reservations), live)
	}
	if granted.Load()+dup.Load() != racers || dup.Load() < racers-racers/3-1 {
		t.Errorf("granted=%d duplicates blocked=%d of %d racers", granted.Load(), dup.Load(), racers)
	}
	if int64(v.BlockedAttempts) != dup.Load() {
		t.Errorf("intent counts %d blocked attempts, racers saw %d", v.BlockedAttempts, dup.Load())
	}
}

func TestEconomic_SameOutcomeFromManyAgentsIsOneIntent(t *testing.T) {
	ctx := context.Background()
	rig := newEconRig(t, 3, 10_000_000)
	ids := make(chan string, 12)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, _, err := rig.svc.CreateIntent(ctx, fmt.Sprintf("agent_%d", i%3+1), econ.Spec{
				Capability: "solana.token-risk", Input: json.RawMessage(`{ "mint": "SOL" }`), Window: "2026-09-29T10", BudgetMaxMinor: 50_000,
			})
			if err != nil {
				t.Error(err)
				return
			}
			ids <- v.ID
		}(i)
	}
	wg.Wait()
	close(ids)
	seen := map[string]bool{}
	for id := range ids {
		seen[id] = true
	}
	if len(seen) != 1 {
		t.Errorf("one outcome must be one intent, got %d", len(seen))
	}
}

// fullRun reserves, begins, authorizes payment and returns the attempt.
func (rig *econRig) fullRun(t *testing.T, agentID, intentID string) *econ.Reservation {
	t.Helper()
	ctx := context.Background()
	r, err := rig.svc.Reserve(ctx, agentID, intentID, reserveReq)
	if err != nil {
		t.Fatal(err)
	}
	if r, err = rig.svc.Begin(ctx, agentID, intentID, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.svc.AuthorizePayment(ctx, agentID, intentID, r.ID, PaymentRequest{}); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestEconomic_NormalExecutionCommitsAndSignsVerifiableReceipt(t *testing.T) {
	ctx := context.Background()
	rig := newEconRig(t, 1, 10_000_000)
	in := rig.intent(t, "agent_1", 50_000)
	r := rig.fullRun(t, "agent_1", in.ID)
	rig.rail.land(r, 3_000)

	v, err := rig.svc.Complete(ctx, "agent_1", in.ID, r.ID, CompletionReport{Outcome: econ.OutcomeFulfilled, Evidence: econ.Evidence{ResultHash: "sha256:abc"}})
	if err != nil {
		t.Fatal(err)
	}
	if v.State != econ.StateCommitted || v.Commitment != econ.CommitmentSettled || v.Fulfillment != econ.FulfillmentFulfilled || v.CommittedMinor != 3_000 {
		t.Fatalf("want committed/settled/fulfilled 3000, got %s/%s/%s %d", v.State, v.Commitment, v.Fulfillment, v.CommittedMinor)
	}
	if v.Receipt == "" {
		t.Fatal("a committed intent gets a signed receipt")
	}
	c, err := receipt.VerifyIntent(v.Receipt, rig.signer.JWKS())
	if err != nil {
		t.Fatal(err)
	}
	if c.Intent.Hash != v.IntentHash || c.Settlement.Transaction != "tx_"+r.ID || c.Settlement.Amount.MinorUnits != 3_000 ||
		c.Execution.Status != "fulfilled" || !c.Test || c.Reservation.Attempt != 1 {
		t.Errorf("receipt claims don't match what was observed: %+v", c)
	}
	// The hold shrinks to what actually settled: unused authority is free.
	if exp := v.Reservations[0].HoldMinor; exp != 3_000 {
		t.Errorf("committed hold should be the settled amount, got %d", exp)
	}
	// A tampered receipt fails.
	parts := strings.Split(v.Receipt, ".")
	tampered := parts[0] + "." + strings.Replace(parts[1], "A", "B", 1) + "." + parts[2]
	if _, err := receipt.VerifyIntent(tampered, rig.signer.JWKS()); err == nil {
		t.Error("a tampered receipt must not verify")
	}
	// Spend receipts and intent receipts can't be swapped for each other.
	if _, err := receipt.Verify(v.Receipt, rig.signer.JWKS()); err == nil {
		t.Error("an intent receipt must not verify as a spend receipt")
	}
}

func TestEconomic_PostPaymentTimeoutFreezesAndReconciles(t *testing.T) {
	ctx := context.Background()
	rig := newEconRig(t, 2, 10_000_000)
	rig.svc.RegisterRecovery("x402:risk.example", fakeRecovery{Recovery{Status: RecoveryFulfilled, ResultHash: "sha256:recovered", OperationID: "op_1"}})
	in := rig.intent(t, "agent_1", 50_000)
	r := rig.fullRun(t, "agent_1", in.ID)
	rig.rail.land(r, 3_000) // the payment landed; the response was lost

	v, err := rig.svc.Complete(ctx, "agent_1", in.ID, r.ID, CompletionReport{Outcome: econ.OutcomeUnknown, Detail: "timeout"})
	if err != nil {
		t.Fatal(err)
	}
	if v.State != econ.StateUnknown || v.Commitment != econ.CommitmentUnknown {
		t.Fatalf("a timeout after payment is UNKNOWN, not FAILED: %s/%s", v.State, v.Commitment)
	}
	// The fallback agent is blocked.
	_, err = rig.svc.Reserve(ctx, "agent_2", in.ID, ReserveRequest{ProviderID: "x402:other.example", Rail: "sandbox"})
	var rej *ReservationRejected
	if !errors.As(err, &rej) || rej.Reason != RejectUnknown || !rej.Duplicate {
		t.Fatalf("fallback must be blocked while the outcome is unknown, got %v", err)
	}
	res, err := rig.svc.Reconcile(ctx, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.State != econ.StateCommitted || res.Settlement != SettlementSettled || res.Recovery != RecoveryFulfilled {
		t.Fatalf("reconciliation should find settlement and result: %+v", res)
	}
	v, _ = rig.svc.View(ctx, in.ID, true)
	if v.Fulfillment != econ.FulfillmentFulfilled || v.Reservations[0].Evidence.ResultHash != "sha256:recovered" {
		t.Errorf("recovered result not recorded: %+v", v.Reservations[0].Evidence)
	}
	if rig.rail.authorized.Load() != 1 {
		t.Errorf("exactly one payment authority, got %d", rig.rail.authorized.Load())
	}
	c, err := receipt.VerifyIntent(v.Receipt, rig.signer.JWKS())
	if err != nil || !c.Coordination.ReconciliationRequired || c.Coordination.DuplicateCommitAttemptsBlocked != 1 {
		t.Errorf("receipt should show reconciliation and the blocked fallback: %v %+v", err, c)
	}
}

func TestEconomic_PaymentConfirmedResultUnknownIsReportedHonestly(t *testing.T) {
	ctx := context.Background()
	rig := newEconRig(t, 1, 10_000_000)
	in := rig.intent(t, "agent_1", 50_000)
	r := rig.fullRun(t, "agent_1", in.ID)
	rig.rail.land(r, 3_000)
	if _, err := rig.svc.Complete(ctx, "agent_1", in.ID, r.ID, CompletionReport{Outcome: econ.OutcomeUnknown}); err != nil {
		t.Fatal(err)
	}
	res, err := rig.svc.Reconcile(ctx, in.ID) // no recovery adapter for this provider
	if err != nil {
		t.Fatal(err)
	}
	v, _ := rig.svc.View(ctx, in.ID, false)
	if v.State != econ.StateCommitted || v.Commitment != econ.CommitmentSettled || v.Fulfillment != econ.FulfillmentUnknown {
		t.Fatalf("payment confirmed, result unknown: %s/%s/%s", v.State, v.Commitment, v.Fulfillment)
	}
	if !strings.Contains(res.Summary, "resource outcome is unknown") || !strings.Contains(v.Summary, "resource outcome unknown") {
		t.Errorf("summaries must say exactly what's proven: %q / %q", res.Summary, v.Summary)
	}
	c, _ := receipt.VerifyIntent(v.Receipt, rig.signer.JWKS())
	if c == nil || c.Execution.Status != "result_unknown" {
		t.Errorf("the receipt must not claim a result: %+v", c)
	}
}

func TestEconomic_ProvenNoSettlementReopensSafely(t *testing.T) {
	ctx := context.Background()
	rig := newEconRig(t, 2, 10_000_000)
	rig.rail.missing = SettlementNotSettled // e.g. blockhash expired, never landed
	in := rig.intent(t, "agent_1", 50_000)
	r := rig.fullRun(t, "agent_1", in.ID)
	if _, err := rig.svc.Complete(ctx, "agent_1", in.ID, r.ID, CompletionReport{Outcome: econ.OutcomeUnknown}); err != nil {
		t.Fatal(err)
	}
	res, err := rig.svc.Reconcile(ctx, in.ID)
	if err != nil || res.State != econ.StateOpen {
		t.Fatalf("proven no settlement should reopen: %v %+v", err, res)
	}
	r2, err := rig.svc.Reserve(ctx, "agent_2", in.ID, reserveReq)
	if err != nil || r2.Attempt != 2 {
		t.Fatalf("a second attempt is now safe: %v %+v", err, r2)
	}
}

func TestEconomic_UnresolvedStaysBlocked(t *testing.T) {
	ctx := context.Background()
	rig := newEconRig(t, 2, 10_000_000)
	rig.rail.missing = SettlementUnknown
	in := rig.intent(t, "agent_1", 50_000)
	r := rig.fullRun(t, "agent_1", in.ID)
	_, _ = rig.svc.Complete(ctx, "agent_1", in.ID, r.ID, CompletionReport{Outcome: econ.OutcomeUnknown})
	res, err := rig.svc.Reconcile(ctx, in.ID)
	if err != nil || !res.Unresolved || res.State != econ.StateReconciling {
		t.Fatalf("no evidence either way stays unresolved: %v %+v", err, res)
	}
	if _, err := rig.svc.Reserve(ctx, "agent_2", in.ID, reserveReq); err == nil {
		t.Fatal("an unresolved intent must block new commitments")
	}
	if _, err := rig.svc.Cancel(ctx, "user_1", in.ID); !errors.Is(err, ErrExecutionFrozen) {
		t.Errorf("cancelling must not paper over money that may have moved: %v", err)
	}
}

func TestEconomic_PrePaymentFailureReleases(t *testing.T) {
	ctx := context.Background()
	rig := newEconRig(t, 2, 10_000_000)
	in := rig.intent(t, "agent_1", 50_000)
	r, _ := rig.svc.Reserve(ctx, "agent_1", in.ID, reserveReq)
	r, _ = rig.svc.Begin(ctx, "agent_1", in.ID, r.ID)
	// The provider rejected the request before any payment authority.
	v, err := rig.svc.Complete(ctx, "agent_1", in.ID, r.ID, CompletionReport{Outcome: econ.OutcomeNoCommitment, Detail: "provider 400"})
	if err != nil || v.State != econ.StateOpen || v.Commitment != econ.CommitmentNone {
		t.Fatalf("no payment authority, no money: reopen. %v %s", err, v.State)
	}
	if _, err := rig.svc.Reserve(ctx, "agent_2", in.ID, reserveReq); err != nil {
		t.Fatalf("fallback may proceed: %v", err)
	}
}

func TestEconomic_AgentCannotSelfCertify(t *testing.T) {
	ctx := context.Background()
	rig := newEconRig(t, 1, 10_000_000)
	in := rig.intent(t, "agent_1", 50_000)
	r := rig.fullRun(t, "agent_1", in.ID)
	// Claims fulfilled, but the rail has seen nothing: not committed.
	v, err := rig.svc.Complete(ctx, "agent_1", in.ID, r.ID, CompletionReport{Outcome: econ.OutcomeFulfilled})
	if err != nil {
		t.Fatal(err)
	}
	if v.State == econ.StateCommitted {
		t.Fatal("an executor's word is not evidence")
	}
	if v.State != econ.StateUnknown {
		t.Errorf("pending settlement is unknown until proven, got %s", v.State)
	}
	// After payment authority, "nothing happened" also needs proof.
	rig2 := newEconRig(t, 1, 10_000_000)
	in2 := rig2.intent(t, "agent_1", 50_000)
	r2 := rig2.fullRun(t, "agent_1", in2.ID)
	v2, _ := rig2.svc.Complete(ctx, "agent_1", in2.ID, r2.ID, CompletionReport{Outcome: econ.OutcomeNoCommitment})
	if v2.State == econ.StateOpen {
		t.Error("once payment authority is out, a no-commitment claim needs rail proof")
	}
	// One attempt, one payment authority.
	rig3 := newEconRig(t, 1, 10_000_000)
	in3 := rig3.intent(t, "agent_1", 50_000)
	r3 := rig3.fullRun(t, "agent_1", in3.ID)
	if _, err := rig3.svc.AuthorizePayment(ctx, "agent_1", in3.ID, r3.ID, PaymentRequest{}); err == nil {
		t.Error("one attempt gets one payment authority")
	}
}

func TestEconomic_CrashBeforeBeginIsSafeCrashAfterIsUnknown(t *testing.T) {
	ctx := context.Background()
	rig := newEconRig(t, 2, 10_000_000)
	rig.svc.SetTimings(10*time.Millisecond, 10*time.Millisecond)

	in := rig.intent(t, "agent_1", 50_000)
	if _, err := rig.svc.Reserve(ctx, "agent_1", in.ID, reserveReq); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond) // agent_1 crashed before beginning
	if _, err := rig.svc.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if v, _ := rig.svc.View(ctx, in.ID, false); v.State != econ.StateOpen {
		t.Fatalf("an expired lease with nothing irreversible reopens the intent, got %s", v.State)
	}
	r := rig.fullRun(t, "agent_2", in.ID)
	time.Sleep(20 * time.Millisecond) // agent_2 crashed mid-execution
	if _, err := rig.svc.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	v, _ := rig.svc.View(ctx, in.ID, false)
	if v.State != econ.StateReconciling && v.State != econ.StateUnknown {
		t.Fatalf("a crash after the irreversible boundary is UNKNOWN, got %s", v.State)
	}
	_ = r
}

func TestEconomic_PassBudgetHoldsAcrossConcurrentIntents(t *testing.T) {
	ctx := context.Background()
	rig := newEconRig(t, 1, 100_000) // 0.10 USDC
	var ids []string
	for i := 0; i < 6; i++ {
		v, _, err := rig.svc.CreateIntent(ctx, "agent_1", econ.Spec{Capability: "company-report", Input: json.RawMessage(fmt.Sprintf(`{"n":%d}`, i)), BudgetMaxMinor: 30_000})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, v.ID)
	}
	var granted atomic.Int64
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if _, err := rig.svc.Reserve(ctx, "agent_1", id, ReserveRequest{ProviderID: "x402:reports", Rail: "sandbox"}); err == nil {
				granted.Add(1)
			}
		}(id)
	}
	wg.Wait()
	if granted.Load() != 3 {
		t.Errorf("0.10 of authority covers three 0.03 holds, got %d reservations", granted.Load())
	}
}

func TestEconomic_PolicyRecheckedBeforeExecution(t *testing.T) {
	ctx := context.Background()
	rig := newEconRig(t, 1, 10_000_000)
	in := rig.intent(t, "agent_1", 50_000)
	r, err := rig.svc.Reserve(ctx, "agent_1", in.ID, reserveReq)
	if err != nil {
		t.Fatal(err)
	}
	// The person revokes the pass between reservation and execution.
	_ = rig.passes.Revoke(ctx, "pass_agent_1", time.Now())
	if _, err := rig.svc.Begin(ctx, "agent_1", in.ID, r.ID); err == nil {
		t.Fatal("a revoked pass must stop execution at the recheck")
	}
}

func TestEconomic_ApprovalIsHumanOnly(t *testing.T) {
	ctx := context.Background()
	rig := newEconRig(t, 1, 10_000_000)
	p, _ := rig.passes.GetByAgent(ctx, "agent_1")
	line := int64(10_000)
	p.ApproveAboveMinorUnits = &line
	_ = rig.passes.Create(ctx, p)

	in := rig.intent(t, "agent_1", 50_000)
	if in.State != econ.StateAwaitingApproval {
		t.Fatalf("over the ask-me line needs approval, got %s", in.State)
	}
	var rej *ReservationRejected
	if _, err := rig.svc.Reserve(ctx, "agent_1", in.ID, reserveReq); !errors.As(err, &rej) || rej.Reason != RejectApproval {
		t.Fatalf("no reservation before approval: %v", err)
	}
	if _, err := rig.svc.Approve(ctx, "someone_else", in.ID); err == nil {
		t.Fatal("only the principal can approve")
	}
	if _, err := rig.svc.Approve(ctx, "user_1", in.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.svc.Reserve(ctx, "agent_1", in.ID, reserveReq); err != nil {
		t.Fatalf("approved intent can be reserved: %v", err)
	}
}

func TestEconomic_ExpiryNeverOverridesAmbiguity(t *testing.T) {
	ctx := context.Background()
	rig := newEconRig(t, 1, 10_000_000)
	clock := time.Now()
	rig.svc.now = func() time.Time { return clock }
	v, _, err := rig.svc.CreateIntent(ctx, "agent_1", econ.Spec{Capability: "company-report", BudgetMaxMinor: 10_000, TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	r := rig.fullRun(t, "agent_1", v.ID)
	_, _ = rig.svc.Complete(ctx, "agent_1", v.ID, r.ID, CompletionReport{Outcome: econ.OutcomeUnknown})
	clock = clock.Add(2 * time.Hour)
	_, _ = rig.svc.Sweep(ctx)
	cur, _ := rig.svc.View(ctx, v.ID, false)
	if cur.State == econ.StateExpired {
		t.Fatal("an intent whose money may have moved can't simply expire")
	}
}
