package app

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
)

// keepingRig is an executor that keeps the answers to paid calls.
func keepingRig(t *testing.T) (*execRig, *time.Time) {
	t.Helper()
	rig := newExecRig(t)
	v, _, clock := testVault(t, time.Hour, 0)
	rig.exec.SetResults(v)
	return rig, clock
}

func doSpec(window string) econ.Spec {
	return econ.Spec{Capability: "solana.token-risk", Input: []byte(`{"mint":"SOL"}`), Window: window, Currency: "USDC", BudgetMaxMinor: 50_000}
}

func (r *execRig) do(t *testing.T, agent, window string, discard bool, providers ...string) (*DoResult, error) {
	t.Helper()
	var cs []routing.Candidate
	for _, p := range providers {
		cs = append(cs, cand(p))
	}
	return r.exec.Do(context.Background(), DoRequest{AgentID: agent, Spec: doSpec(window), Candidates: cs, DiscardResult: discard})
}

func TestReplay_AskingAgainReturnsTheKeptAnswerAndPaysNothing(t *testing.T) {
	rig, _ := keepingRig(t)
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)

	first, err := rig.do(t, "agent_1", "w1", false, "alpha")
	if err != nil || !first.Created || !first.Report.Delivered || first.Replayed || first.Report.Replayed {
		t.Fatalf("first: %+v %v", first, err)
	}
	paidOnce := rig.rail.authorized.Load()

	// The same outcome again: committed already, so nothing runs and nothing is paid.
	again, err := rig.do(t, "agent_1", "w1", false, "alpha")
	if err != nil {
		t.Fatalf("a repeat request is answered, not refused: %v", err)
	}
	if again.Created || !again.Replayed || !again.Report.Replayed || !again.Report.Delivered || again.Report.Stopped != "replayed" {
		t.Fatalf("replayed: %+v %+v", again, again.Report)
	}
	if rig.runner.ran("alpha") != 1 || rig.rail.authorized.Load() != paidOnce || rig.runner.quoted("alpha") != 1 {
		t.Errorf("no provider was contacted and nothing was paid: ran=%d quoted=%d authorized=%d", rig.runner.ran("alpha"), rig.runner.quoted("alpha"), rig.rail.authorized.Load())
	}
	f := again.Report.Final()
	if f == nil || string(f.Body) != goodBody || f.ContentType != "application/json" || f.Result.ResponseHash != econ.HashBytes([]byte(goodBody)) {
		t.Fatalf("the answer that was kept: %+v", f)
	}
	// It is the same attempt's record: what was paid and how it was judged.
	if f.Result.Payment != routing.PaymentSettled || f.Result.ActualCostMinor != 3_000 || f.Quality == nil || f.Intent.State != econ.StateCommitted {
		t.Errorf("the record of the original attempt: %+v", f.Result)
	}
	// The outcome says so, with the receipt, and no routing since nothing was routed.
	out := OutcomeOf(&again.Created, again.Report)
	if !out.Replayed || !out.Delivered || out.Receipt == "" || out.Response == nil || out.Routing != nil {
		t.Errorf("outcome: replayed=%v delivered=%v receipt=%v", out.Replayed, out.Delivered, out.Receipt != "")
	}
	v, _ := rig.econRig.svc.View(context.Background(), first.Intent.ID, true)
	var seen []string
	for _, e := range v.Events {
		seen = append(seen, e.Event)
	}
	for _, want := range []string{"result.stored", "result.replayed"} {
		if !slices.Contains(seen, want) {
			t.Errorf("missing event %s in %v", want, seen)
		}
	}
	// And any number of repeats stay free.
	if _, err := rig.do(t, "agent_1", "w1", false, "alpha"); err != nil || rig.rail.authorized.Load() != paidOnce {
		t.Errorf("repeats stay free: %v", err)
	}
}

func TestReplay_ARequestThatDeclinedKeepingGetsNothingToReplay(t *testing.T) {
	rig, _ := keepingRig(t)
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)

	first, err := rig.do(t, "agent_1", "w2", true, "alpha")
	if err != nil || !first.Report.Delivered {
		t.Fatalf("first: %v", err)
	}
	if string(first.Report.Final().Body) != goodBody {
		t.Error("the caller still gets its answer")
	}
	_, err = rig.do(t, "agent_1", "w2", false, "alpha")
	var rej *ReservationRejected
	if !errors.As(err, &rej) || rej.Reason != RejectCommitted {
		t.Fatalf("nothing was kept, so the repeat is refused as before: %v", err)
	}
	if _, err := rig.exec.Result(context.Background(), "user_1", first.Intent.ID); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("no result to read: %v", err)
	}
	v, _ := rig.econRig.svc.View(context.Background(), first.Intent.ID, true)
	for _, e := range v.Events {
		if e.Event == "result.stored" {
			t.Error("a declined result must not be recorded as stored")
		}
	}
}

func TestReplay_AnExpiredAnswerIsNotReplayed(t *testing.T) {
	rig, clock := keepingRig(t)
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)
	first, err := rig.do(t, "agent_1", "w3", false, "alpha")
	if err != nil || !first.Report.Delivered {
		t.Fatal(err)
	}
	*clock = clock.Add(2 * time.Hour) // the vault's clock; the retention is an hour
	_, err = rig.do(t, "agent_1", "w3", false, "alpha")
	var rej *ReservationRejected
	if !errors.As(err, &rej) || rej.Reason != RejectCommitted {
		t.Fatalf("an expired answer is gone: %v", err)
	}
}

func TestReplay_ChecksTheAskingAgentsOwnPass(t *testing.T) {
	rig := &execRig{econRig: newEconRig(t, 2, 10_000_000), runner: newFakeRunner(), store: newMemExecStore()}
	rig.exec = NewExecutionService(rig.econRig.svc, rig.store)
	rig.exec.RegisterRunner(rig.runner)
	v, _, _ := testVault(t, time.Hour, 0)
	rig.exec.SetResults(v)
	// agent_2's pass allows one provider, and it isn't alpha.
	now := time.Now()
	_ = rig.passes.Create(context.Background(), &spendpass.Pass{
		ID: "pass_agent_2", UserID: "user_1", AgentID: "agent_2", Label: "narrow", AgentKind: spendpass.AgentCustom, Currency: "USDC",
		BudgetMinorUnits: 10_000_000, BudgetPeriod: spendpass.PeriodTotal, CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(24 * time.Hour),
		AllowedMerchants: []string{"gamma"},
	})
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)

	first, err := rig.do(t, "agent_1", "w4", false, "alpha")
	if err != nil || !first.Report.Delivered {
		t.Fatal(err)
	}
	// agent_2 asks for the same outcome. Its pass doesn't allow alpha, so it
	// doesn't get alpha's answer either: the request is refused at the pass, as
	// it always was, without alpha being contacted.
	quotedBefore := rig.runner.quoted("alpha")
	_, err = rig.do(t, "agent_2", "w4", false, "alpha")
	var nr *NoRoute
	if !errors.As(err, &nr) || len(nr.Rejected) != 1 || nr.Rejected[0].Code != routing.RejectPolicy {
		t.Fatalf("a pass that doesn't allow the provider gets no replay: %v", err)
	}
	if rig.runner.quoted("alpha") != quotedBefore {
		t.Error("the provider was not even asked for a price")
	}
	// agent_1, whose pass allows it, still does.
	if again, err := rig.do(t, "agent_1", "w4", false, "alpha"); err != nil || !again.Replayed {
		t.Errorf("the allowed agent is replayed: %v", err)
	}
}

func TestReplay_ResultReadsBackForThePersonOnly(t *testing.T) {
	rig, _ := keepingRig(t)
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)
	first, err := rig.do(t, "agent_1", "w5", false, "alpha")
	if err != nil || !first.Report.Delivered {
		t.Fatal(err)
	}
	r, err := rig.exec.Result(context.Background(), "user_1", first.Intent.ID)
	if err != nil || string(r.Body) != goodBody || r.ContentType != "application/json" || r.SHA256 != econ.HashBytes([]byte(goodBody)) || !r.ExpiresAt.After(r.StoredAt) {
		t.Fatalf("result: %+v %v", r, err)
	}
	if _, err := rig.exec.Result(context.Background(), "user_2", first.Intent.ID); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("another person can't read it: %v", err)
	}
	if _, err := rig.exec.Result(context.Background(), "user_1", "eint_nope"); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("unknown intent: %v", err)
	}
}

func TestReplay_WithoutAVaultNothingChanges(t *testing.T) {
	rig := newExecRig(t) // no vault
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)
	if _, err := rig.do(t, "agent_1", "w6", false, "alpha"); err != nil {
		t.Fatal(err)
	}
	_, err := rig.do(t, "agent_1", "w6", false, "alpha")
	var rej *ReservationRejected
	if !errors.As(err, &rej) || rej.Reason != RejectCommitted {
		t.Errorf("as before: %v", err)
	}
	if n, err := rig.exec.PurgeResults(context.Background()); err != nil || n != 0 {
		t.Errorf("purging nothing: %d %v", n, err)
	}
}

func TestReplay_ABodyThatDoesNotMatchTheCommittedHashIsNotReplayed(t *testing.T) {
	rig, _ := keepingRig(t)
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)
	first, err := rig.do(t, "agent_1", "w7", false, "alpha")
	if err != nil || !first.Report.Delivered {
		t.Fatal(err)
	}
	// Replace what was kept with a different answer, sealed properly: the
	// vault accepts it, but it isn't what the committed attempt received.
	if _, err := rig.exec.results.Keep(context.Background(), "user_1", first.Intent.ID, first.Report.Final().Result.ReservationID, "application/json", 200, []byte(`{"swapped":true}`)); err != nil {
		t.Fatal(err)
	}
	_, err = rig.do(t, "agent_1", "w7", false, "alpha")
	var rej *ReservationRejected
	if !errors.As(err, &rej) || rej.Reason != RejectCommitted {
		t.Fatalf("a different body is not replayed as the original: %v", err)
	}
	if _, err := rig.exec.Result(context.Background(), "user_1", first.Intent.ID); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("nor served by the result endpoint: %v", err)
	}
}
