package app

import (
	"context"
	"testing"

	"github.com/project-algebra/algebra/internal/domain/routing"
)

// A request that names a provider wants that provider's answer. Handing it the
// kept answer of another provider, for nothing, is not what was asked for, and it
// is what stops two providers ever being asked the same question (multi-select,
// cross-checking). Without a name, any provider's fresh answer will do.
func TestMemory_ARequestThatNamesAProviderIsNeverAnsweredByAnother(t *testing.T) {
	rig, _ := keepingRig(t)
	ctx := context.Background()
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)
	rig.runner.behaviors["beta"] = rig.delivers(goodBody)
	if _, err := rig.do(t, "agent_1", "w1", false, "alpha"); err != nil {
		t.Fatal(err)
	}

	// Beta, by name: a purchase of its own, not alpha's answer.
	got, err := rig.exec.Do(ctx, DoRequest{AgentID: "agent_1", Spec: doSpec("w2"), Candidates: []routing.Candidate{cand("beta")}, Named: []string{"beta"}})
	if err != nil || got.Replayed || got.Report.Reused != nil || !got.Created || rig.runner.ran("beta") != 1 {
		t.Fatalf("naming beta pays beta: %+v %v ran=%d", got, err, rig.runner.ran("beta"))
	}
	// Alpha, by name (however it is written): its kept answer.
	again, err := rig.exec.Do(ctx, DoRequest{AgentID: "agent_1", Spec: doSpec("w3"), Candidates: []routing.Candidate{cand("alpha")}, Named: []string{" Alpha "}})
	if err != nil || !again.Replayed || again.Report.Reused == nil || again.Report.Reused.Provider != "alpha" || rig.runner.ran("alpha") != 1 {
		t.Fatalf("naming alpha reuses alpha's answer: %+v %v", again, err)
	}
	// Naming nobody: whichever fresh answer there is.
	any, err := rig.exec.Do(ctx, DoRequest{AgentID: "agent_1", Spec: doSpec("w4"), Candidates: []routing.Candidate{cand("alpha"), cand("beta")}})
	if err != nil || !any.Replayed || any.Report.Reused == nil {
		t.Fatalf("with nobody named a fresh answer is reused: %+v %v", any, err)
	}
	// Naming a provider that was never paid: nothing to reuse.
	rig.runner.behaviors["gamma"] = rig.delivers(goodBody)
	gamma, err := rig.exec.Do(ctx, DoRequest{AgentID: "agent_1", Spec: doSpec("w5"), Candidates: []routing.Candidate{cand("gamma")}, Named: []string{"gamma"}})
	if err != nil || gamma.Replayed || rig.runner.ran("gamma") != 1 {
		t.Fatalf("naming gamma pays gamma: %+v %v", gamma, err)
	}
}

func TestMemory_TheDryRunAnswersForTheNamedProviderToo(t *testing.T) {
	rig, _ := keepingRig(t)
	ctx := context.Background()
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)
	if _, err := rig.do(t, "agent_1", "w1", false, "alpha"); err != nil {
		t.Fatal(err)
	}
	for named, wantReuse := range map[string]bool{"alpha": true, "beta": false} {
		sim, err := rig.exec.Simulate(ctx, SimulateRequest{AgentID: "agent_1", Spec: doSpec("w2"), Candidates: []routing.Candidate{cand(named)}, Named: []string{named}})
		if err != nil {
			t.Fatal(err)
		}
		if (sim.Reuse != nil) != wantReuse {
			t.Errorf("a dry run naming %s: reuse = %+v, want reuse %v", named, sim.Reuse, wantReuse)
		}
	}
}
