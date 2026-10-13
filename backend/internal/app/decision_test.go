package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/routing"
)

// ALLOW, HOLD and BLOCK, as the server decides them. A request can ask for a
// person's yes; nothing a request says can remove one that is required.

func TestDecision_AskingFirstHoldsWhatThePassWouldAllow(t *testing.T) {
	ctx := context.Background()
	rig := newExecRig(t)
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)
	spec := econ.Spec{Capability: "solana.token-risk", Input: json.RawMessage(`{"mint":"SOL"}`), Window: "ask-first", Currency: "USDC", BudgetMaxMinor: 50_000}
	cands := []routing.Candidate{cand("alpha")}

	// The dry run says ALLOW for the plain request, HOLD once a yes is asked for.
	sim, err := rig.exec.Simulate(ctx, SimulateRequest{AgentID: "agent_1", Spec: spec, Candidates: cands})
	if err != nil || sim.Decision != "ALLOW" {
		t.Fatalf("plain request: %v %+v", err, sim)
	}
	spec.AskFirst = true
	sim, err = rig.exec.Simulate(ctx, SimulateRequest{AgentID: "agent_1", Spec: spec, Candidates: cands})
	if err != nil || sim.Decision != "HOLD" || sim.Verdict != VerdictRequireApproval {
		t.Fatalf("asked first: %v %+v", err, sim)
	}
	if len(sim.Reasons) == 0 || sim.Reasons[len(sim.Reasons)-1] != ReasonAskedFirst {
		t.Fatalf("the reason is on record: %v", sim.Reasons)
	}

	// Executing it: held, and nothing is contacted or paid.
	var rej *ReservationRejected
	if _, err := rig.exec.Do(ctx, DoRequest{AgentID: "agent_1", Spec: spec, Candidates: cands}); !errors.As(err, &rej) || rej.Reason != RejectApproval {
		t.Fatalf("held for approval: %v", err)
	}
	// The same request again, no longer asking, does not release it.
	spec.AskFirst = false
	if _, err := rig.exec.Do(ctx, DoRequest{AgentID: "agent_1", Spec: spec, Candidates: cands}); !errors.As(err, &rej) || rej.Reason != RejectApproval {
		t.Fatalf("dropping the ask must not skip the approval: %v", err)
	}
	if rig.runner.quoted("alpha") != 0 || rig.runner.ran("alpha") != 0 {
		t.Fatal("a held request must not reach the provider, not even for a price")
	}
	// Only the person's yes releases it, and then it is paid once.
	v, _, _ := rig.econRig.svc.CreateIntent(ctx, "agent_1", spec)
	if _, err := rig.econRig.svc.Approve(ctx, "user_1", v.ID); err != nil {
		t.Fatal(err)
	}
	res, err := rig.exec.Do(ctx, DoRequest{AgentID: "agent_1", Spec: spec, Candidates: cands})
	if err != nil || !res.Report.Delivered || rig.runner.ran("alpha") != 1 {
		t.Fatalf("approved: %v %+v ran=%d", err, res, rig.runner.ran("alpha"))
	}
}

func TestDecision_ADeniedPassNeverAuthorizesPayment(t *testing.T) {
	ctx := context.Background()
	rig := newExecRig(t)
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)
	// A budget ceiling above the pass's remaining budget is denied outright.
	spec := econ.Spec{Capability: "solana.token-risk", Input: json.RawMessage(`{"mint":"SOL"}`), Window: "deny", Currency: "USDC", BudgetMaxMinor: 99_000_000}

	sim, err := rig.exec.Simulate(ctx, SimulateRequest{AgentID: "agent_1", Spec: spec, Candidates: []routing.Candidate{cand("alpha")}})
	if err != nil || sim.Decision != "BLOCK" || sim.Verdict != VerdictDeny {
		t.Fatalf("dry run: %v %+v", err, sim)
	}
	var denied *AuthorityDenied
	if _, err := rig.exec.Do(ctx, DoRequest{AgentID: "agent_1", Spec: spec, Candidates: []routing.Candidate{cand("alpha")}}); !errors.As(err, &denied) {
		t.Fatalf("denied: %v", err)
	}
	if rig.runner.quoted("alpha") != 0 || rig.runner.ran("alpha") != 0 {
		t.Fatal("a blocked request must not reach the provider")
	}
	// Asking for a yes can't turn a BLOCK into a HOLD someone could approve.
	spec.AskFirst = true
	if _, err := rig.exec.Do(ctx, DoRequest{AgentID: "agent_1", Spec: spec, Candidates: []routing.Candidate{cand("alpha")}}); !errors.As(err, &denied) {
		t.Fatalf("still denied when a yes is asked for: %v", err)
	}
}
