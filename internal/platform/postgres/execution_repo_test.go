package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

func ptrBool(b bool) *bool        { return &b }
func ptrFloat(f float64) *float64 { return &f }

func TestExecutionRepo_RoundTripAndRefresh(t *testing.T) {
	f := newEconFixture(t, 1, 10_000_000)
	repo := NewExecutionRepo(f.db)
	ctx := context.Background()
	in := f.intent(t, `{"mint":"SOL"}`, 50_000)
	rsv, err := f.svc.Reserve(ctx, f.agents[0], in.ID, app.ReserveRequest{ProviderID: "alpha", Rail: "sandbox", QuoteMinor: 3_000})
	if err != nil {
		t.Fatal(err)
	}

	started := time.Now().UTC().Truncate(time.Microsecond)
	res := routing.ExecutionResult{
		ID: "xres_" + rsv.ID, IntentID: in.ID, ReservationID: rsv.ID, Attempt: rsv.Attempt, PlanID: "plan_1", PlanRank: 2,
		Mode: routing.ModeAuto, PlanHash: "sha256:plan", QuoteHash: "sha256:quote",
		CandidateID: "cand_1", QuoteID: "quo_1", Provider: "alpha", Capability: "solana.token-risk", ExecutionType: routing.ExecX402,
		QuotedCostMinor: 3_000, StartedAt: started, HTTPStatus: 503,
		Payment: routing.PaymentUnknown, Delivery: econ.FulfillmentUnknown, Network: "sandbox", Asset: "USDC",
		RequestHash: "sha256:req", Test: true,
		Failure: &routing.Failure{Class: routing.FailAmbiguous, Message: "money may have moved"},
	}
	q := &routing.QualityResult{Evaluator: "generic@1", SchemaValid: ptrBool(true), Completeness: ptrFloat(1), FinalQuality: ptrFloat(100), Flags: []string{"no_schema"}}
	if err := repo.SaveResult(ctx, f.userID, app.StoredExecution{Result: res, Quality: q}); err != nil {
		t.Fatal(err)
	}

	got, err := repo.ForReservation(ctx, rsv.ID)
	if err != nil {
		t.Fatal(err)
	}
	g := got.Result
	if g.ID != res.ID || g.IntentID != in.ID || g.Attempt != 1 || g.PlanRank != 2 || g.Mode != routing.ModeAuto || g.PlanHash != "sha256:plan" || g.QuoteHash != "sha256:quote" ||
		g.Provider != "alpha" || g.ExecutionType != routing.ExecX402 || g.QuotedCostMinor != 3_000 || g.HTTPStatus != 503 ||
		g.Payment != routing.PaymentUnknown || g.Delivery != econ.FulfillmentUnknown || g.Network != "sandbox" || !g.Test ||
		!g.StartedAt.Equal(started) || !g.CompletedAt.IsZero() {
		t.Errorf("round trip: %+v", g)
	}
	if g.Failure == nil || g.Failure.Class != routing.FailAmbiguous || g.Failure.Message != "money may have moved" {
		t.Errorf("failure: %+v", g.Failure)
	}
	if got.Quality == nil || got.Quality.Evaluator != "generic@1" || got.Quality.FinalQuality == nil || *got.Quality.FinalQuality != 100 ||
		got.Quality.SchemaValid == nil || !*got.Quality.SchemaValid {
		t.Errorf("quality: %+v", got.Quality)
	}

	// Reconciliation resolves the attempt: the same record is brought up to date.
	done := started.Add(2 * time.Second)
	res.Payment, res.Delivery, res.ActualCostMinor, res.Transaction = routing.PaymentSettled, econ.FulfillmentFulfilled, 3_000, "sbxtx_1"
	res.Failure, res.CompletedAt, res.LatencyMS = nil, done, 2_000
	if err := repo.SaveResult(ctx, f.userID, app.StoredExecution{Result: res, Quality: q}); err != nil {
		t.Fatal(err)
	}
	list, err := repo.ForIntent(ctx, in.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("an update replaces, it doesn't add: %v %d", err, len(list))
	}
	u := list[0].Result
	if u.Payment != routing.PaymentSettled || u.ActualCostMinor != 3_000 || u.Transaction != "sbxtx_1" || u.Failure != nil || u.LatencyMS != 2_000 || !u.CompletedAt.Equal(done) {
		t.Errorf("after refresh: %+v", u)
	}
	// Identity doesn't change on update.
	if u.PlanHash != "sha256:plan" || u.Provider != "alpha" || u.PlanRank != 2 {
		t.Errorf("immutable fields moved: %+v", u)
	}

	if _, err := repo.ForReservation(ctx, "rsv_nope"); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("unknown reservation: %v", err)
	}
	// One record per attempt: a different result for the same reservation is refused.
	dup := res
	dup.ID = "xres_other"
	if err := repo.SaveResult(ctx, f.userID, app.StoredExecution{Result: dup}); err == nil {
		t.Error("a second record for one attempt must be refused")
	}
}

func TestExecutionRepo_NoQualityAndNoReservation(t *testing.T) {
	f := newEconFixture(t, 1, 10_000_000)
	repo := NewExecutionRepo(f.db)
	ctx := context.Background()
	in := f.intent(t, `{"mint":"BONK"}`, 50_000)

	res := routing.ExecutionResult{
		ID: "xres_noreservation_" + in.ID, IntentID: in.ID, CandidateID: "cand_9", Provider: "beta", Capability: "solana.token-risk",
		ExecutionType: routing.ExecX402, StartedAt: time.Now().UTC(), Payment: routing.PaymentNotAttempted, Delivery: econ.FulfillmentNone,
	}
	if err := repo.SaveResult(ctx, f.userID, app.StoredExecution{Result: res}); err != nil {
		t.Fatal(err)
	}
	list, err := repo.ForIntent(ctx, in.ID)
	if err != nil || len(list) != 1 || list[0].Quality != nil || list[0].Result.ReservationID != "" || list[0].Result.Failure != nil {
		t.Fatalf("a record without a reservation or a verdict: %v %+v", err, list)
	}
	// Rows the schema forbids are refused, not stored.
	bad := res
	bad.ID, bad.Payment = "xres_bad_"+in.ID, "PAIDISH"
	if err := repo.SaveResult(ctx, f.userID, app.StoredExecution{Result: bad}); err == nil {
		t.Error("an unknown payment status must be refused by the database")
	}
}
