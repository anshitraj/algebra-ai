package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/receipt"
)

// An executor that stops after Begin, before asking for payment authority,
// can't have moved any money: Algebra never released the means to. The
// intent must reopen, not sit RECONCILING behind a rail that can't answer
// about a payment it never saw.
func TestEconomic_AttemptThatNeverGotPaymentAuthorityReopens(t *testing.T) {
	ctx := context.Background()
	rig := newEconRig(t, 2, 10_000_000)
	in := rig.intent(t, "agent_1", 50_000)

	r, err := rig.svc.Reserve(ctx, "agent_1", in.ID, reserveReq)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rig.svc.Begin(ctx, "agent_1", in.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	// The executor vanishes. The execution deadline passes and the sweeper runs.
	later := time.Now().Add(DefaultExecutionTimeout + time.Minute)
	rig.svc.now = func() time.Time { return later }
	if _, err := rig.svc.Sweep(ctx); err != nil {
		t.Fatal(err)
	}

	v, _ := rig.svc.View(ctx, in.ID, true)
	if v.State != econ.StateOpen || v.Commitment != econ.CommitmentNone {
		t.Fatalf("no authority was ever released, so the intent reopens: %s/%s", v.State, v.Commitment)
	}
	released := false
	for _, e := range v.Events {
		if e.Event == "reservation.released" && strings.Contains(e.Data["reason"].(string), "never released payment authority") {
			released = true
		}
	}
	if !released {
		t.Errorf("the release must say why: %+v", v.Events)
	}
	// And the fallback executor is no longer blocked.
	if _, err := rig.svc.Reserve(ctx, "agent_2", in.ID, ReserveRequest{ProviderID: "x402:other.example", Rail: "sandbox", QuoteMinor: 3_000}); err != nil {
		t.Errorf("a fallback should be able to take the intent: %v", err)
	}
}

// The fix is for attempts that never had authority. One that did, and whose
// rail can't say, must stay frozen: that is exactly the ambiguous case.
func TestEconomic_AttemptWithPaymentAuthorityStaysFrozenWhenRailCantSay(t *testing.T) {
	ctx := context.Background()
	rig := newEconRig(t, 1, 10_000_000)
	in := rig.intent(t, "agent_1", 50_000)
	rig.fullRun(t, "agent_1", in.ID) // authority released; the rail has no settlement yet

	later := time.Now().Add(DefaultExecutionTimeout + time.Minute)
	rig.svc.now = func() time.Time { return later }
	if _, err := rig.svc.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	v, _ := rig.svc.View(ctx, in.ID, false)
	if v.State != econ.StateReconciling {
		t.Fatalf("money may have moved and the rail can't say: must stay blocked, got %s", v.State)
	}
}

func TestEconomic_PaidButUnusableResultIsNotFulfilled(t *testing.T) {
	ctx := context.Background()
	rig := newEconRig(t, 1, 10_000_000)
	in := rig.intent(t, "agent_1", 50_000)
	r := rig.fullRun(t, "agent_1", in.ID)
	rig.rail.land(r, 3_000)

	v, err := rig.svc.Complete(ctx, "agent_1", in.ID, r.ID, CompletionReport{
		Outcome: econ.OutcomeSettled, Fulfillment: econ.FulfillmentNotFulfilled,
		Evidence: econ.Evidence{ResultHash: "sha256:garbage"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if v.State != econ.StateCommitted || v.Commitment != econ.CommitmentSettled || v.Fulfillment != econ.FulfillmentNotFulfilled {
		t.Fatalf("paid and unusable: committed/settled/not fulfilled, got %s/%s/%s", v.State, v.Commitment, v.Fulfillment)
	}
	if !strings.Contains(v.Summary, "didn't deliver a usable result") || !strings.Contains(v.Summary, "Duplicate payment blocked") {
		t.Errorf("summary must say what happened and that a repeat is blocked: %q", v.Summary)
	}
	c, err := receipt.VerifyIntent(v.Receipt, rig.signer.JWKS())
	if err != nil || c.Execution.Status != "not_fulfilled" || c.Final.Fulfillment != "NOT_FULFILLED" {
		t.Errorf("the receipt must not claim a result: %v %+v", err, c)
	}
}

// Claiming "unusable" must never commit money by itself: the rail still has
// to prove the payment.
func TestEconomic_NotFulfilledClaimStillNeedsTheRailsProof(t *testing.T) {
	ctx := context.Background()
	rig := newEconRig(t, 1, 10_000_000)
	in := rig.intent(t, "agent_1", 50_000)
	r := rig.fullRun(t, "agent_1", in.ID) // the rail has not seen it land

	v, err := rig.svc.Complete(ctx, "agent_1", in.ID, r.ID, CompletionReport{Outcome: econ.OutcomeSettled, Fulfillment: econ.FulfillmentNotFulfilled})
	if err != nil {
		t.Fatal(err)
	}
	if v.State != econ.StateUnknown || v.CommittedMinor != 0 {
		t.Fatalf("an unproven payment is UNKNOWN, whatever the executor says about the result: %s %d", v.State, v.CommittedMinor)
	}
}

// A fulfilled claim can't be smuggled in through the new field.
func TestEconomic_FulfillmentFieldCannotClaimSuccess(t *testing.T) {
	ctx := context.Background()
	rig := newEconRig(t, 1, 10_000_000)
	in := rig.intent(t, "agent_1", 50_000)
	r := rig.fullRun(t, "agent_1", in.ID)
	rig.rail.land(r, 3_000)

	v, err := rig.svc.Complete(ctx, "agent_1", in.ID, r.ID, CompletionReport{Outcome: econ.OutcomeSettled, Fulfillment: econ.FulfillmentFulfilled})
	if err != nil {
		t.Fatal(err)
	}
	if v.Fulfillment != econ.FulfillmentUnknown {
		t.Errorf("only outcome FULFILLED claims success; got %s", v.Fulfillment)
	}
}

func TestEconomic_ReconcileTakesTheProvidersNotFulfilledAnswer(t *testing.T) {
	ctx := context.Background()
	rig := newEconRig(t, 1, 10_000_000)
	rig.svc.RegisterRecovery("x402:risk.example", fakeRecovery{Recovery{Status: RecoveryNotFulfilled, Detail: "provider refused to deliver"}})
	in := rig.intent(t, "agent_1", 50_000)
	r := rig.fullRun(t, "agent_1", in.ID)
	rig.rail.land(r, 3_000)
	if _, err := rig.svc.Complete(ctx, "agent_1", in.ID, r.ID, CompletionReport{Outcome: econ.OutcomeUnknown}); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.svc.Reconcile(ctx, in.ID); err != nil {
		t.Fatal(err)
	}
	v, _ := rig.svc.View(ctx, in.ID, false)
	if v.State != econ.StateCommitted || v.Fulfillment != econ.FulfillmentNotFulfilled {
		t.Errorf("settled + provider says it didn't deliver: %s/%s", v.State, v.Fulfillment)
	}
}
