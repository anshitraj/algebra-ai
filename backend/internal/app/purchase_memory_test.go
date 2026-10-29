package app

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/routing"
)

// The same work with the same input, asked again in another request while the
// answer is fresh: the kept answer, nothing paid, no new intent.
func TestMemory_ReusesAFreshAnswerAcrossRequests(t *testing.T) {
	rig, _ := keepingRig(t)
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)
	first, err := rig.do(t, "agent_1", "w1", false, "alpha")
	if err != nil || !first.Report.Delivered {
		t.Fatalf("first: %+v %v", first, err)
	}
	paid := rig.rail.authorized.Load()

	again, err := rig.do(t, "agent_1", "w2", false, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	r := again.Report
	if !again.Replayed || r.Stopped != "reused" || r.Reused == nil || r.Reused.IntentID != first.Intent.ID || r.Reused.Provider != "alpha" ||
		r.Reused.PaidMinor != 3_000 || r.Reused.FreshForSeconds != int((30*time.Minute).Seconds()) {
		t.Fatalf("reused: %+v %+v", again, r.Reused)
	}
	if rig.runner.ran("alpha") != 1 || rig.rail.authorized.Load() != paid {
		t.Errorf("nothing was run or paid: ran=%d authorized=%d", rig.runner.ran("alpha"), rig.rail.authorized.Load())
	}
	if f := r.Final(); f == nil || string(f.Body) != goodBody {
		t.Fatalf("the kept answer: %+v", f)
	}
	if out := OutcomeOf(nil, r); out.Reused == nil || !out.Replayed || !out.Delivered || out.Response == nil {
		t.Errorf("the outcome says it was reused: %+v", out)
	}
	if list, _ := rig.econRig.store.ListIntents(context.Background(), "user_1", 50); len(list) != 1 {
		t.Errorf("no new intent for a reused answer: %d intents", len(list))
	}
	v, _ := rig.econRig.svc.View(context.Background(), first.Intent.ID, true)
	if !slices.ContainsFunc(v.Events, func(e EconEvent) bool { return e.Event == "result.reused" }) {
		t.Error("the reuse is on the original purchase's record")
	}

	// Asking for a fresh one pays.
	fresh, err := rig.exec.Do(context.Background(), DoRequest{AgentID: "agent_1", Spec: doSpec("w3"), Candidates: []routing.Candidate{cand("alpha")}, Fresh: true})
	if err != nil || fresh.Replayed || fresh.Report.Reused != nil || rig.runner.ran("alpha") != 2 {
		t.Fatalf("fresh pays: %+v %v ran=%d", fresh, err, rig.runner.ran("alpha"))
	}
}

// Past its freshness, or for another provider list, an answer isn't reused.
func TestMemory_OnlyWhatIsFreshAndAllowed(t *testing.T) {
	rig, _ := keepingRig(t)
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)
	rig.runner.behaviors["beta"] = rig.delivers(goodBody)
	if _, err := rig.do(t, "agent_1", "w1", false, "alpha"); err != nil {
		t.Fatal(err)
	}
	// Only beta may do it: alpha's answer isn't this request's.
	spec := doSpec("w2")
	spec.ProviderPolicy = econ.ProviderPolicy{Providers: []string{"beta"}}
	got, err := rig.exec.Do(context.Background(), DoRequest{AgentID: "agent_1", Spec: spec, Candidates: []routing.Candidate{cand("beta")}})
	if err != nil || got.Report.Reused != nil || rig.runner.ran("beta") != 1 {
		t.Fatalf("a request limited to beta pays beta: %+v %v", got, err)
	}
	// Half an hour later, token risk is no longer fresh.
	later := time.Now().Add(31 * time.Minute)
	rig.econRig.svc.now = func() time.Time { return later }
	rig.exec.now = func() time.Time { return later }
	got, err = rig.do(t, "agent_1", "w3", false, "alpha")
	if err != nil || got.Report.Reused != nil || rig.runner.ran("alpha") != 2 {
		t.Fatalf("a stale answer is bought again: %+v %v", got, err)
	}
	// A request's own max age is stricter than the class's.
	spec = doSpec("w4")
	spec.Constraints.MaxAgeSeconds = 1
	rig.econRig.svc.now = func() time.Time { return later.Add(5 * time.Second) }
	rig.exec.now = rig.econRig.svc.now
	got, err = rig.exec.Do(context.Background(), DoRequest{AgentID: "agent_1", Spec: spec, Candidates: []routing.Candidate{cand("alpha")}})
	if err != nil || got.Report.Reused != nil {
		t.Fatalf("max_age_seconds 1 never reuses a 5-second-old answer: %+v %v", got, err)
	}
}

// The same thing bought again and again is what a loop looks like: the next
// one waits for the person.
func TestMemory_RepeatPurchasesWaitForAYes(t *testing.T) {
	rig := newExecRig(t) // no kept answers: every request pays
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)
	for i := range RepeatLimit {
		if got, err := rig.do(t, "agent_1", "w"+string(rune('a'+i)), false, "alpha"); err != nil || !got.Report.Delivered {
			t.Fatalf("purchase %d: %+v %v", i+1, got, err)
		}
	}
	got, err := rig.do(t, "agent_1", "w-next", false, "alpha")
	var rej *ReservationRejected
	if !errors.As(err, &rej) || rej.Reason != RejectApproval || got == nil || got.Intent == nil || got.Intent.State != econ.StateAwaitingApproval {
		t.Fatalf("the sixth identical purchase in ten minutes waits: %+v %v", got, err)
	}
	v, _ := rig.econRig.svc.View(context.Background(), got.Intent.ID, true)
	if !slices.ContainsFunc(v.Events, func(e EconEvent) bool {
		codes, _ := e.Data["reason_codes"].([]string)
		return e.Event == "authority.evaluated" && slices.Contains(codes, ReasonRepeatPurchase)
	}) {
		t.Errorf("the reason is recorded: %+v", v.Events)
	}
	// Other input is a different purchase, and goes ahead.
	spec := doSpec("w-other")
	spec.Input = []byte(`{"mint":"BONK"}`)
	if got, err := rig.exec.Do(context.Background(), DoRequest{AgentID: "agent_1", Spec: spec, Candidates: []routing.Candidate{cand("alpha")}}); err != nil || !got.Report.Delivered {
		t.Fatalf("other input: %+v %v", got, err)
	}
}

// The dry run says when an answer would be reused.
func TestMemory_DryRunSaysItWouldReuse(t *testing.T) {
	rig, _ := keepingRig(t)
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)
	if _, err := rig.do(t, "agent_1", "w1", false, "alpha"); err != nil {
		t.Fatal(err)
	}
	sim, err := rig.exec.Simulate(context.Background(), SimulateRequest{AgentID: "agent_1", Spec: doSpec("w2"), Candidates: []routing.Candidate{cand("alpha")}})
	if err != nil || sim.Reuse == nil || sim.Reuse.Provider != "alpha" || sim.Reuse.PaidMinor != 3_000 {
		t.Fatalf("dry run: %+v %v", sim, err)
	}
	if rig.runner.ran("alpha") != 1 {
		t.Error("a dry run pays nothing")
	}
}
