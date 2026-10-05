package routing

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
)

func okResult() ExecutionResult {
	return ExecutionResult{
		ID: "xres_1", IntentID: "eint_1", ReservationID: "rsv_1", Attempt: 1, PlanID: "plan_1", PlanRank: 1,
		CandidateID: "cand_1", QuoteID: "quo_1", Provider: "birdeye", Capability: "solana.token-risk", ExecutionType: ExecX402,
		QuotedCostMinor: 3000, ActualCostMinor: 3000, StartedAt: t0, HTTPStatus: 200,
		Payment: PaymentSettled, Delivery: econ.FulfillmentFulfilled, Transaction: "5Gf…sig", Network: "solana", Asset: "USDC",
		RequestHash: "sha256:aa", ResponseHash: "sha256:bb",
	}
}

func TestResultFinishAndSucceeded(t *testing.T) {
	r := okResult()
	r.Finish(t0.Add(241 * time.Millisecond))
	if r.LatencyMS != 241 || !r.CompletedAt.Equal(t0.Add(241*time.Millisecond)) {
		t.Errorf("latency = %d, completed %v", r.LatencyMS, r.CompletedAt)
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("a good result validates: %v", err)
	}
	if !r.Succeeded() {
		t.Error("settled + fulfilled is a success")
	}

	free := okResult()
	free.Payment, free.ActualCostMinor, free.QuotedCostMinor, free.Transaction = PaymentNotAttempted, 0, 0, ""
	if !free.Succeeded() || free.Validate() != nil {
		t.Error("a free call that delivered is a success")
	}

	for name, mutate := range map[string]func(*ExecutionResult){
		"paid but result unknown": func(r *ExecutionResult) { r.Delivery = econ.FulfillmentUnknown },
		"not fulfilled":           func(r *ExecutionResult) { r.Delivery = econ.FulfillmentNotFulfilled },
		"payment unknown":         func(r *ExecutionResult) { r.Payment = PaymentUnknown },
		"failure recorded":        func(r *ExecutionResult) { r.Failure = &Failure{Class: FailInvalid} },
	} {
		x := okResult()
		mutate(&x)
		if x.Succeeded() {
			t.Errorf("%s must not count as a success", name)
		}
	}
}

func TestResultFinishNeverGoesNegative(t *testing.T) {
	r := okResult()
	r.Finish(t0.Add(-time.Second)) // clock skew
	if r.LatencyMS != 0 {
		t.Errorf("latency = %d, want 0", r.LatencyMS)
	}
}

func TestResultValidateRefusesContradictions(t *testing.T) {
	cases := map[string]func(*ExecutionResult){
		"no intent":              func(r *ExecutionResult) { r.IntentID = "" },
		"no candidate":           func(r *ExecutionResult) { r.CandidateID = "" },
		"no provider":            func(r *ExecutionResult) { r.Provider = "" },
		"bad type":               func(r *ExecutionResult) { r.ExecutionType = "x" },
		"no start":               func(r *ExecutionResult) { r.StartedAt = time.Time{} },
		"completes before start": func(r *ExecutionResult) { r.CompletedAt = t0.Add(-time.Second) },
		"bad payment status":     func(r *ExecutionResult) { r.Payment = "PAIDISH" },
		"bad delivery status":    func(r *ExecutionResult) { r.Delivery = "MAYBE" },
		"negative cost":          func(r *ExecutionResult) { r.ActualCostMinor = -1 },
		"cost without payment":   func(r *ExecutionResult) { r.Payment, r.Delivery = PaymentNotAttempted, econ.FulfillmentNone },
		"fulfilled and failed":   func(r *ExecutionResult) { r.Failure = &Failure{Class: FailProvider} },
	}
	for name, mutate := range cases {
		r := okResult()
		mutate(&r)
		if r.Validate() == nil {
			t.Errorf("%s should be refused", name)
		}
	}
	// A response that arrived before the chain showed the payment is real, and
	// is recordable; it just isn't a success until the payment settles.
	early := okResult()
	early.Payment = PaymentUnknown
	if err := early.Validate(); err != nil || early.Succeeded() {
		t.Errorf("delivered with the payment unconfirmed: valid, but not yet a success: %v", err)
	}
	// Paid but the result never came is a legitimate, recordable state.
	paid := okResult()
	paid.Delivery, paid.Failure = econ.FulfillmentUnknown, &Failure{Class: FailTimeout, Message: "no answer"}
	if err := paid.Validate(); err != nil {
		t.Errorf("settled with an unknown result is a real state: %v", err)
	}
}

func approx(t *testing.T, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("got nil, want %.2f", want)
	}
	if math.Abs(*got-want) > 0.01 {
		t.Errorf("got %.4f, want %.2f", *got, want)
	}
}

func TestQualityFinalize(t *testing.T) {
	yes, no := true, false
	f := func(v float64) *float64 { return &v }

	t.Run("not delivered is a known zero", func(t *testing.T) {
		q := QualityResult{Evaluator: "generic@1", SchemaValid: &yes}
		if err := q.Finalize(false); err != nil {
			t.Fatal(err)
		}
		approx(t, q.FinalQuality, 0)
		if !slices.Contains(q.Flags, "not_delivered") {
			t.Errorf("flags: %v", q.Flags)
		}
	})
	t.Run("only a valid schema is judged", func(t *testing.T) {
		q := QualityResult{SchemaValid: &yes}
		_ = q.Finalize(true)
		approx(t, q.FinalQuality, 100)
	})
	t.Run("an invalid schema caps the score whatever else is true", func(t *testing.T) {
		q := QualityResult{SchemaValid: &no, SemanticQuality: f(1), Completeness: f(1), Freshness: f(1)}
		_ = q.Finalize(true)
		approx(t, q.FinalQuality, 40)
		if !slices.Contains(q.Flags, "schema_invalid") {
			t.Errorf("flags: %v", q.Flags)
		}
	})
	t.Run("unjudged parts don't count against a provider", func(t *testing.T) {
		q := QualityResult{SemanticQuality: f(0.5)}
		_ = q.Finalize(true)
		approx(t, q.FinalQuality, 50)
	})
	t.Run("weights renormalise over what was judged", func(t *testing.T) {
		// semantic 1.0 (weight .40) and completeness 0 (weight .20): 40/60.
		q := QualityResult{SemanticQuality: f(1), Completeness: f(0)}
		_ = q.Finalize(true)
		approx(t, q.FinalQuality, 66.67)
	})
	t.Run("everything judged", func(t *testing.T) {
		q := QualityResult{SchemaValid: &yes, SemanticQuality: f(0.9), Completeness: f(1), Freshness: f(0.8)}
		_ = q.Finalize(true)
		// .25*1 + .40*.9 + .20*1 + .15*.8 = .93
		approx(t, q.FinalQuality, 93)
	})
	t.Run("nothing judged leaves quality unknown, not zero", func(t *testing.T) {
		q := QualityResult{Evaluator: "generic@1"}
		_ = q.Finalize(true)
		if q.FinalQuality != nil {
			t.Errorf("want nil, got %v", *q.FinalQuality)
		}
	})
	t.Run("re-finalizing starts fresh", func(t *testing.T) {
		q := QualityResult{SemanticQuality: f(1)}
		_ = q.Finalize(true)
		q.SemanticQuality = f(0.2)
		_ = q.Finalize(true)
		approx(t, q.FinalQuality, 20)
		_ = q.Finalize(false)
		_ = q.Finalize(false)
		if n := len(q.Flags); n != 1 {
			t.Errorf("a flag is added once: %v", q.Flags)
		}
	})
	t.Run("out of range scores are refused", func(t *testing.T) {
		for _, q := range []QualityResult{
			{SemanticQuality: f(1.2)}, {Freshness: f(-0.1)}, {Completeness: f(math.NaN())},
		} {
			if q.Finalize(true) == nil {
				t.Errorf("%+v should be refused", q)
			}
		}
	})
}
