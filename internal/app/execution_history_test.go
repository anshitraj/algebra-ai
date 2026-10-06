package app

import (
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/routing"
)

func attempt(started time.Time, payment routing.PaymentStatus, delivery econ.Fulfillment, latencyMS, cost int64, quality *float64, fail *routing.Failure) StoredExecution {
	r := routing.ExecutionResult{
		ID: "x", IntentID: "i", CandidateID: "c", Provider: "p", Capability: "cap", ExecutionType: routing.ExecX402,
		StartedAt: started, CompletedAt: started.Add(time.Duration(latencyMS) * time.Millisecond), LatencyMS: latencyMS,
		Payment: payment, Delivery: delivery, ActualCostMinor: cost, Failure: fail,
	}
	rec := StoredExecution{Result: r}
	if quality != nil {
		rec.Quality = &routing.QualityResult{Evaluator: "generic@1", FinalQuality: quality}
	}
	return rec
}

func f64(v float64) *float64 { return &v }

func TestSummarizeHistory(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	ok := func(i int, latency int64, q float64) StoredExecution {
		return attempt(t0.Add(time.Duration(i)*time.Minute), routing.PaymentSettled, econ.FulfillmentFulfilled, latency, 3_000, f64(q), nil)
	}
	recs := []StoredExecution{
		ok(1, 100, 100), ok(2, 200, 80), ok(3, 300, 90), ok(4, 400, 90),
		// Paid, but the response was unusable: a call, a failure, a paid invalid response.
		attempt(t0.Add(5*time.Minute), routing.PaymentSettled, econ.FulfillmentNotFulfilled, 500, 3_000, f64(30), &routing.Failure{Class: routing.FailInvalid}),
		// Never paid: the provider errored before authority was released.
		attempt(t0.Add(6*time.Minute), routing.PaymentNotAttempted, econ.FulfillmentNotFulfilled, 50, 0, nil, &routing.Failure{Class: routing.FailQuote}),
		// Over, but still unresolved or in flight: not counted yet.
		attempt(t0.Add(7*time.Minute), routing.PaymentUnknown, econ.FulfillmentUnknown, 900, 0, nil, &routing.Failure{Class: routing.FailAmbiguous}),
		attempt(t0.Add(8*time.Minute), routing.PaymentAuthorized, econ.FulfillmentNone, 0, 0, nil, nil),
	}
	// A record with no completion time is still running.
	running := attempt(t0.Add(9*time.Minute), routing.PaymentNotAttempted, econ.FulfillmentNone, 0, 0, nil, nil)
	running.Result.CompletedAt = time.Time{}
	recs = append(recs, running)

	h := SummarizeHistory(recs)
	if h.Calls != 6 {
		t.Fatalf("six attempts are over and resolved: %d", h.Calls)
	}
	if h.SuccessRate < 0.666 || h.SuccessRate > 0.667 { // 4 of 6
		t.Errorf("success: %v", h.SuccessRate)
	}
	if h.ValidRate != 0.8 { // of the 5 paid responses, 4 were usable
		t.Errorf("valid rate: %v", h.ValidRate)
	}
	// Latency is the delivered attempts' (100, 200, 300, 400): nearest-rank p50 = 200, p95 = 400.
	if h.P50LatencyMS != 200 || h.P95LatencyMS != 400 {
		t.Errorf("latency: p50 %d p95 %d", h.P50LatencyMS, h.P95LatencyMS)
	}
	if h.AvgQuality != 90 { // (100+80+90+90)/4, delivered ones only
		t.Errorf("quality: %v", h.AvgQuality)
	}
	if h.AvgCostMinor != 3_000 {
		t.Errorf("average cost over settled attempts: %d", h.AvgCostMinor)
	}
	if !h.LastCallAt.Equal(t0.Add(6 * time.Minute)) {
		t.Errorf("the last call that counts: %s", h.LastCallAt)
	}
	if err := h.Validate(); err != nil {
		t.Errorf("a summary is always a valid history: %v", err)
	}
}

func TestSummarizeHistoryOfNothingIsZero(t *testing.T) {
	if h := SummarizeHistory(nil); h.Calls != 0 || h.SuccessRate != 0 {
		t.Errorf("no record: %+v", h)
	}
	only := []StoredExecution{attempt(time.Now(), routing.PaymentUnknown, econ.FulfillmentUnknown, 10, 0, nil, nil)}
	if h := SummarizeHistory(only); h.Calls != 0 {
		t.Errorf("an unresolved attempt doesn't count yet: %+v", h)
	}
}

func TestSummarizeHistoryWithoutDeliveriesHasNoLatencyOrQuality(t *testing.T) {
	t0 := time.Now().Add(-time.Hour)
	var recs []StoredExecution
	for i := 0; i < 6; i++ {
		recs = append(recs, attempt(t0.Add(time.Duration(i)*time.Minute), routing.PaymentNotAttempted, econ.FulfillmentNotFulfilled, 80, 0, nil, &routing.Failure{Class: routing.FailProvider}))
	}
	h := SummarizeHistory(recs)
	if h.Calls != 6 || h.SuccessRate != 0 || h.P50LatencyMS != 0 || h.AvgQuality != 0 || h.ValidRate != 0 {
		t.Errorf("six failures: %+v", h)
	}
	if err := h.Validate(); err != nil {
		t.Error(err)
	}
}
