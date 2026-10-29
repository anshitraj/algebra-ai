package app

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/project-algebra/algebra/internal/domain/jobpass"
	"github.com/project-algebra/algebra/internal/domain/routing"
)

// A finished task replayed under chaos: every adversary is caught, held or
// outranked, the down seller is fallen back from, and nothing is paid.
func TestReplay_ATaskUnderChaos(t *testing.T) {
	ctx := context.Background()
	rig := newExecRig(t)
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)
	jobs := NewJobPassService(rig.econRig.store, rig.econRig.passes, nil)
	jobs.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	j, err := jobs.Open(ctx, OpenJob{UserID: "user_1", PassID: "pass_agent_1", Spec: jobpass.Spec{Label: "Check a token", BudgetMinor: 50_000}})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := rig.exec.Do(ctx, DoRequest{AgentID: "agent_1", Spec: doSpec("w1"), Candidates: []routing.Candidate{cand("alpha")}, JobID: j.ID}); err != nil || !got.Report.Delivered {
		t.Fatalf("the task's purchase: %+v %v", got, err)
	}
	v, err := jobs.View(ctx, j.ID)
	if err != nil || len(v.Attempts) != 1 {
		t.Fatalf("view: %+v %v", v, err)
	}
	paidBefore, ranBefore := rig.rail.authorized.Load(), rig.runner.ran("alpha")
	priced := func(p string) routing.Candidate { c := cand(p); c.PriceMinor, c.Asset = 3_000, "USDC"; return c }
	rep, err := rig.exec.Replay(ctx, v, "agent_1", func(context.Context, string) []routing.Candidate {
		return []routing.Candidate{priced("alpha"), priced("beta")}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Purchases) != 1 || len(rep.Purchases[0].Findings) != 6 {
		t.Fatalf("one purchase, six scenarios: %+v", rep)
	}
	by := map[string]ReplayFinding{}
	for _, f := range rep.Purchases[0].Findings {
		by[f.Scenario] = f
	}
	if f := by[ChaosOvercharge]; f.Outcome != ReplayCaught || !strings.Contains(f.Detail, RejectPriceAboveListing) || f.AtRiskMinor != 12_000 {
		t.Errorf("overcharge: %+v", f)
	}
	if f := by[ChaosHoneypot]; f.Outcome != ReplayCaught || f.AtRiskMinor != honeypotMinor {
		t.Errorf("honeypot: %+v", f)
	}
	if f := by[ChaosWebLure]; f.Outcome != ReplayHeld || !strings.Contains(f.Detail, "WEB_FOUND") {
		t.Errorf("a web find waits for a yes by default: %+v", f)
	}
	if f := by[ChaosStranger]; f.Outcome == ReplayCaught {
		t.Errorf("a stranger at the same price is within the default new-provider cap, so it isn't refused: %+v", f)
	}
	if f := by[ChaosSellerDown]; f.Outcome != ReplayFallsBack || !strings.Contains(f.Detail, "beta") {
		t.Errorf("with alpha down it falls back to beta: %+v", f)
	}
	if f := by[ChaosLoop]; f.Outcome != ReplayHeld || !f.Safe {
		t.Errorf("loop: %+v", f)
	}
	if rep.AtRiskMinor <= rep.Purchases[0].PaidMinor || rep.ProtectedMinor < 12_000+honeypotMinor {
		t.Errorf("totals: %+v", rep)
	}
	if rig.rail.authorized.Load() != paidBefore || rig.runner.ran("alpha") != ranBefore || rig.runner.quoted("alpha") != 1 {
		t.Errorf("a replay pays and contacts nothing: authorized=%d ran=%d quoted=%d", rig.rail.authorized.Load(), rig.runner.ran("alpha"), rig.runner.quoted("alpha"))
	}
}
