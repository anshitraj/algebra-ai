package app

import (
	"math"
	"slices"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/routing"
)

// HistoryWindow is how far back the router looks, and HistoryDepth how many of
// a candidate's most recent attempts it counts. A provider that was bad a year
// ago and has been fine since should be judged on since.
const (
	HistoryWindow = 30 * 24 * time.Hour
	HistoryDepth  = 100
)

// SummarizeHistory turns a candidate's recent attempts into the record the
// router ranks with. Every store builds it the same way, so a ranking doesn't
// depend on where the records live.
//
//   - Only attempts that are over count: one still in flight, or whose money
//     outcome is unknown, says nothing yet (reconciliation brings it up to date
//     and it counts then).
//   - An attempt succeeded when a result was delivered and its payment is
//     settled or no payment was needed (routing.ExecutionResult.Succeeded).
//   - Latency is how long delivered attempts took, not failures: a timeout is
//     slow for a reason that says nothing about how fast the provider answers.
//   - ValidRate is the share of paid-for responses that were usable, and zero
//     when nothing was paid for.
func SummarizeHistory(recs []StoredExecution) routing.History {
	var h routing.History
	var successes, paid, usable int
	var latencies []int
	var qualitySum float64
	var qualityN, settled int
	var costSum int64
	for _, rec := range recs {
		r := rec.Result
		if r.CompletedAt.IsZero() || r.Payment == routing.PaymentUnknown || r.Payment == routing.PaymentAuthorized {
			continue
		}
		h.Calls++
		if r.StartedAt.After(h.LastCallAt) {
			h.LastCallAt = r.StartedAt
		}
		if r.Succeeded() {
			successes++
		}
		if r.Payment == routing.PaymentSettled {
			settled++
			costSum += r.ActualCostMinor
			if r.Delivery == econ.FulfillmentFulfilled || r.Delivery == econ.FulfillmentNotFulfilled {
				paid++
				if r.Delivery == econ.FulfillmentFulfilled {
					usable++
				}
			}
		}
		if r.Delivery == econ.FulfillmentFulfilled {
			if r.LatencyMS > 0 {
				latencies = append(latencies, int(r.LatencyMS))
			}
			if rec.Quality != nil && rec.Quality.FinalQuality != nil {
				qualitySum += *rec.Quality.FinalQuality
				qualityN++
			}
		}
	}
	if h.Calls == 0 {
		return h
	}
	h.SuccessRate = float64(successes) / float64(h.Calls)
	if paid > 0 {
		h.ValidRate = float64(usable) / float64(paid)
	}
	if len(latencies) > 0 {
		slices.Sort(latencies)
		h.P50LatencyMS, h.P95LatencyMS = percentile(latencies, 0.50), percentile(latencies, 0.95)
	}
	if qualityN > 0 {
		h.AvgQuality = qualitySum / float64(qualityN)
	}
	if settled > 0 {
		h.AvgCostMinor = costSum / int64(settled)
	}
	return h
}

// percentile is the nearest-rank percentile of an ascending slice.
func percentile(sorted []int, p float64) int {
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	return sorted[min(max(idx, 0), len(sorted)-1)]
}
