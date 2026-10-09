package routing

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"
)

// Option is a candidate together with the live quote it was priced at: the
// two things the router needs to compare one provider with another.
type Option struct {
	Candidate Candidate
	Quote     Quote
}

// Score component names. They are the same in a plan, a log and the console.
const (
	ScoreCost        = "cost"
	ScoreLatency     = "latency"
	ScoreQuality     = "quality"
	ScoreReliability = "reliability"
	ScoreTrust       = "trust"
	// ScorePriceHonesty is how close the live price is to the price the source
	// listed. A provider that asks more than it advertised scores below one.
	ScorePriceHonesty = "price_honesty"
)

// autoWeights are how AUTO trades the components against each other. They sum
// to one, so a step's Total reads as "how close to the best possible".
var autoWeights = map[string]float64{
	ScoreCost:         0.30,
	ScoreReliability:  0.25,
	ScoreQuality:      0.15,
	ScoreLatency:      0.12,
	ScoreTrust:        0.10,
	ScorePriceHonesty: 0.08,
}

// A provider Algebra has no record of is assumed to be as reliable as its
// source makes it likely to be, and the assumption fades as real calls come
// in: after ObservedMinCalls calls, the record outweighs it.
var priorReliability = map[Trust]float64{
	TrustNative:     0.90,
	TrustObserved:   0.70,
	TrustListed:     0.70,
	TrustUnverified: 0.40,
}

// trustScore is how far a provider's source can be trusted, as a number.
var trustScore = map[Trust]float64{
	TrustNative:     1.00,
	TrustObserved:   0.90,
	TrustListed:     0.75,
	TrustUnverified: 0.40,
}

const (
	// priorWeight is how many calls' worth of evidence the prior counts for.
	priorWeight = 3.0
	// unreliableBelow: an observed provider that delivers less often than this
	// is ranked after every other, whatever it costs or how fast it is.
	unreliableBelow = 0.5
	// neutral is what a component scores when nothing is known about it: not a
	// reward and not a penalty.
	neutral = 0.5
	// honestyTolerance: a live price this far over the listed one (5%) is
	// rounding, not a different price.
	honestyTolerance = 1.05
)

// tier groups options that must never be mixed in a ranking, whatever they
// score: real money before test money, and providers that mostly deliver
// before those that mostly don't. The two add up, so on a test network the
// providers that mostly fail still come last.
type tier int

const (
	tierNormal      tier = 0
	tierUnreliable  tier = 1
	tierTestNetwork tier = 2
)

type ranked struct {
	opt      Option
	trust    Trust
	tier     tier
	cost     int64
	latency  int // expected milliseconds; 0 when unknown
	reliable float64
	quality  float64
	honesty  float64
	notes    []string
}

// Rank orders options best-first for a mode and explains each one's place. It
// is a pure function and a stable one: the same options in the same order give
// the same ranking, so a plan can be reproduced from its quotes and its hash
// checked, and options that tie on every measure keep the order they were given
// in. That is how a caller prefers one provider to another that is otherwise
// its equal: by naming it first.
//
//   - CHEAPEST puts the lowest total cost first. The cost is the live quote,
//     fees included, never the price a catalog advertised.
//   - FASTEST puts the shortest expected time to a result first, judged from
//     Algebra's own record of the provider when it has one.
//   - AUTO weighs cost, reliability, quality, speed, trust and price honesty.
//
// In every mode, options on a test network come after real ones, and an
// observed provider that mostly fails comes after those that don't.
func Rank(mode Mode, opts []Option) []PlanStep {
	if len(opts) == 0 {
		return nil
	}
	rs := make([]ranked, len(opts))
	for i, o := range opts {
		rs[i] = describe(o)
	}
	minCost := rs[0].cost
	var minLatency int
	for _, r := range rs {
		minCost = min(minCost, r.cost)
		if r.latency > 0 && (minLatency == 0 || r.latency < minLatency) {
			minLatency = r.latency
		}
	}

	steps := make([]PlanStep, len(rs))
	order := make([]int, len(rs))
	for i, r := range rs {
		order[i] = i
		comp := map[string]float64{
			ScoreCost:         costScore(r.cost, minCost),
			ScoreLatency:      latencyScore(r.latency, minLatency),
			ScoreReliability:  r.reliable,
			ScoreQuality:      r.quality,
			ScoreTrust:        trustScore[r.trust],
			ScorePriceHonesty: r.honesty,
		}
		sc := Score{Components: roundAll(comp), Notes: r.notes}
		switch mode {
		case ModeCheapest:
			sc.Total, sc.Weights = round(comp[ScoreCost]), map[string]float64{ScoreCost: 1}
		case ModeFastest:
			sc.Total, sc.Weights = round(comp[ScoreLatency]), map[string]float64{ScoreLatency: 1}
		default:
			var total float64
			for name, w := range autoWeights {
				total += w * comp[name]
			}
			sc.Total, sc.Weights = round(total), copyWeights(autoWeights)
		}
		steps[i] = PlanStep{Quote: r.opt.Quote, Trust: r.trust, Score: sc}
	}

	slices.SortStableFunc(order, func(a, b int) int {
		ra, rb := rs[a], rs[b]
		if c := cmp.Compare(ra.tier, rb.tier); c != 0 {
			return c
		}
		var c int
		switch mode {
		case ModeCheapest:
			c = cmp.Or(cmp.Compare(ra.cost, rb.cost), cmp.Compare(rb.reliable, ra.reliable), compareLatency(ra.latency, rb.latency))
		case ModeFastest:
			c = cmp.Or(compareLatency(ra.latency, rb.latency), cmp.Compare(ra.cost, rb.cost), cmp.Compare(rb.reliable, ra.reliable))
		default:
			c = cmp.Or(cmp.Compare(steps[b].Score.Total, steps[a].Score.Total), cmp.Compare(ra.cost, rb.cost), compareLatency(ra.latency, rb.latency))
		}
		return c
	})

	out := make([]PlanStep, len(order))
	for i, idx := range order {
		out[i] = steps[idx]
		out[i].Rank = i + 1
		if i == 0 && len(rs) > 1 {
			out[i].Score.Notes = append([]string{headline(mode, len(rs))}, out[i].Score.Notes...)
		}
	}
	return out
}

// describe reads one option into what ranking needs, and why it is what it is.
func describe(o Option) ranked {
	c, q := o.Candidate, o.Quote
	r := ranked{opt: o, trust: c.Trust(), cost: q.Cost.Total(), honesty: 1}
	h := c.History
	observed := h != nil && h.Calls >= ObservedMinCalls

	// Reliability: the record, smoothed towards what its source makes likely.
	prior := priorReliability[r.trust]
	r.reliable = prior
	switch {
	case h != nil && h.Calls > 0:
		successes := h.SuccessRate * float64(h.Calls)
		r.reliable = (successes + prior*priorWeight) / (float64(h.Calls) + priorWeight)
		r.notes = append(r.notes, fmt.Sprintf("%d of %d calls delivered", int(math.Round(successes)), h.Calls))
	default:
		r.notes = append(r.notes, fmt.Sprintf("no record yet (%s): assumed %d%% reliable", r.trust, int(math.Round(prior*100))))
	}
	if observed && h.SuccessRate < unreliableBelow {
		r.tier = tierUnreliable
		r.notes = append(r.notes, "demoted: it mostly fails")
	}

	// Latency: Algebra's own measurement beats anyone's claim.
	switch {
	case observed && h.P50LatencyMS > 0:
		r.latency = h.P50LatencyMS
	case q.EstimatedLatencyMS > 0:
		r.latency = q.EstimatedLatencyMS
	case c.EstimatedLatencyMS > 0:
		r.latency = c.EstimatedLatencyMS
	}

	// Quality: only a judged record says anything.
	r.quality = neutral
	if observed && h.AvgQuality > 0 {
		r.quality = h.AvgQuality / 100
	}

	// Price honesty: the live price against the listed one.
	if c.PriceMinor > 0 && q.Cost.ProviderMinor > 0 && float64(q.Cost.ProviderMinor) > float64(c.PriceMinor)*honestyTolerance {
		r.honesty = float64(c.PriceMinor) / float64(q.Cost.ProviderMinor)
		r.notes = append(r.notes, fmt.Sprintf("asks %s, listed at %s", minor(q.Cost.ProviderMinor, q.Asset), minor(c.PriceMinor, c.Asset)))
	}
	if q.Test {
		r.tier += tierTestNetwork
		r.notes = append(r.notes, "test network: no real money")
	}
	return r
}

func headline(mode Mode, n int) string {
	switch mode {
	case ModeCheapest:
		return fmt.Sprintf("cheapest of %d offers", n)
	case ModeFastest:
		return fmt.Sprintf("fastest of %d offers", n)
	}
	return fmt.Sprintf("best overall of %d offers", n)
}

// costScore is how near an offer is to the cheapest: one for the cheapest,
// halving as the price doubles. A unit is added to both so a free offer
// doesn't divide by zero, and still beats a paid one by a wide margin.
func costScore(cost, lowest int64) float64 {
	return float64(lowest+1) / float64(cost+1)
}

// latencyScore is how near an offer is to the quickest known one. Unknown is
// neutral: a provider nobody has timed neither wins nor loses on speed.
func latencyScore(ms, fastest int) float64 {
	if ms <= 0 || fastest <= 0 {
		return neutral
	}
	return float64(fastest) / float64(ms)
}

// compareLatency puts known latencies before unknown ones, shortest first.
func compareLatency(a, b int) int {
	switch {
	case a == b:
		return 0
	case a == 0:
		return 1
	case b == 0:
		return -1
	}
	return cmp.Compare(a, b)
}

func round(v float64) float64 { return math.Round(v*10_000) / 10_000 }

func roundAll(m map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(m))
	for k, v := range m {
		out[k] = round(v)
	}
	return out
}

func copyWeights(m map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// minor formats an amount of the settlement asset for a note: micro-USDC as a
// decimal, anything else as plain minor units.
func minor(v int64, asset string) string {
	if strings.EqualFold(asset, "USDC") || asset == "" {
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", float64(v)/1e6), "0"), ".") + " USDC"
	}
	return fmt.Sprintf("%d %s", v, asset)
}
