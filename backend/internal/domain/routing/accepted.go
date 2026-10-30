package routing

import "math"

// Cost per accepted result is what the router compares instead of the price on
// the offer. A provider that charges 0.001 and hands back something usable
// nine times in ten is cheaper than one that charges 0.0008, takes the money
// and fails half the time: getting one usable answer from the second costs two
// attempts and one of them is paid for nothing.
//
// For one offer with price c, acceptance rate p (a usable answer delivered and
// paid for, or free) and waste rate w (paid for, nothing usable came back),
// each attempt costs c with probability p + w and succeeds with probability p.
// Attempts until an accepted answer are geometric with mean 1/p, so
//
//	cost per accepted answer = c × (p + w) / p
//
// Failures that cost nothing (a provider down before payment) don't add money,
// only attempts. Both rates come from Algebra's own record of the provider,
// smoothed towards what its source makes likely, exactly as reliability is.

const (
	// wastePriorShare is the share of an unknown provider's expected failures
	// assumed to happen after it was paid. Half: x402 is pay-then-deliver, but
	// many failures (down, wrong price, refused input) come before payment.
	wastePriorShare = 0.5
	// maxCostMultiple caps the cost per accepted answer at this many times the
	// price: past it a provider is useless, and a bigger number says nothing more.
	maxCostMultiple = 50
)

// acceptance is one offer's expected acceptance and waste, and the cost per
// accepted answer that follows from them.
func acceptance(cost int64, prior float64, h *History) (accept, waste float64, perAccepted int64) {
	accept = prior
	waste = (1 - prior) * wastePriorShare
	if h != nil && h.Calls > 0 {
		n := float64(h.Calls)
		accept = (h.SuccessRate*n + prior*priorWeight) / (n + priorWeight)
		waste = (h.WastedRate*n + (1-prior)*wastePriorShare*priorWeight) / (n + priorWeight)
	}
	return accept, waste, perAcceptedCost(cost, accept, waste)
}

func perAcceptedCost(cost int64, accept, waste float64) int64 {
	if cost <= 0 {
		return 0
	}
	ceiling := float64(cost) * maxCostMultiple
	if accept <= 0 {
		return int64(ceiling)
	}
	return int64(math.Round(min(float64(cost)*(accept+waste)/accept, ceiling)))
}

// Expectation is what running steps in order is expected to come to, falling
// back to the next step whenever one doesn't deliver: the money it is expected
// to spend in all, the chance it ends with an accepted answer, and the
// expected cost per accepted answer. A step is only reached when every step
// before it failed, and only the steps that are reached can cost anything.
func Expectation(steps []PlanStep) (costMinor int64, accept float64, perAcceptedMinor int64) {
	reach, cost := 1.0, 0.0
	for _, st := range steps {
		p, w := st.Score.AcceptRate, st.Score.WasteRate
		cost += reach * float64(st.Quote.Cost.Total()) * (p + w)
		accept += reach * p
		reach *= 1 - p
	}
	if accept > 0 {
		perAcceptedMinor = int64(math.Round(cost / accept))
	}
	return int64(math.Round(cost)), round(accept), perAcceptedMinor
}
