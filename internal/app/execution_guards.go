package app

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// Rejection codes the router's guards add. They mean "Algebra would not pay
// this", not "the person's policy forbids it": they hold for everyone.
const (
	// RejectProviderDown: the free health probe found it failing, twice in a
	// row and recently, so it isn't even asked for a price.
	RejectProviderDown = "provider_down"
	// RejectPriceAboveListing: its live 402 asks for more than its catalog
	// listing says it charges. Of the x402 endpoints tracked publicly in
	// September 2026, one in eighteen did this.
	RejectPriceAboveListing = "price_above_listing"
	// RejectPriceOutlier: it asks many times what this kind of work usually
	// costs: the "honeypot" listings priced to trap agents that pay whatever
	// a 402 says.
	RejectPriceOutlier = "price_outlier"
)

const (
	// listingTolerance: a live price this far over the listed one (5%) is
	// rounding; more is a different price.
	listingTolerancePct = 105
	// outlierFactor and outlierFloorMinor: a price is an outlier when it is
	// more than outlierFactor times the median of its peers and more than
	// outlierFloorMinor ($0.05): ten times a $0.001 call is still a cheap call.
	outlierFactor     = 10
	outlierFloorMinor = 50_000
	// trapPriceMinor: a single call at $1,000 or more is never paid by an
	// agent, whatever its peers charge.
	trapPriceMinor = 1_000 * 1_000_000
	// minPeersForMedian: fewer priced peers than this and there is no
	// "usual price" to compare with.
	minPeersForMedian = 3
)

// guards are the router's own checks on a request's candidates.
type guards struct {
	median int64
	health map[string]EndpointHealth
}

// newGuards prepares the checks for a request: the usual price of the work,
// from what the candidates list, and what the health probe last found.
func (s *ExecutionService) newGuards(ctx context.Context, cands []routing.Candidate) guards {
	g := guards{}
	var prices []int64
	norm := make([]routing.Candidate, 0, len(cands))
	for _, c := range cands {
		if c.PriceMinor > 0 {
			prices = append(prices, c.PriceMinor)
		}
		if n, err := c.Normalize(); err == nil {
			norm = append(norm, n)
		}
	}
	if len(prices) >= minPeersForMedian {
		slices.Sort(prices)
		g.median = prices[len(prices)/2]
	}
	if s.health != nil {
		g.health = s.health.Known(ctx, norm)
	}
	return g
}

// before checks a candidate before it is asked for a price, and fills in what
// the probe measured when the candidate states nothing itself.
func (g guards) before(view *IntentView, c routing.Candidate) (routing.Candidate, *routing.Rejection) {
	reject := func(code, detail string) (routing.Candidate, *routing.Rejection) {
		return c, &routing.Rejection{CandidateID: c.ID, Provider: c.Provider, Code: code, Detail: detail}
	}
	// A candidate on a network the intent can't pay on would only take a
	// pricing slot from one it can.
	if n := view.Constraints.AllowedNetworks; len(n) > 0 && c.Network != "" && !slices.Contains(n, chain.NormalizeNetwork(c.Network)) {
		return reject(routing.RejectNetwork, "listed on "+c.Network)
	}
	if h, ok := g.health[c.ID]; ok {
		if h.Status != HealthUp && h.Failures >= downAfter {
			detail := fmt.Sprintf("%s on its last %d probes", h.Status, h.Failures)
			if h.Error != "" {
				detail += " (" + h.Error + ")"
			}
			return reject(RejectProviderDown, detail)
		}
		if h.Overcharges {
			return reject(RejectPriceAboveListing, fmt.Sprintf("its last probe asked %s; it lists %s", usd(h.LivePriceMinor), usd(h.ListedPriceMinor)))
		}
		if c.EstimatedLatencyMS == 0 && h.Status == HealthUp && h.LatencyMS > 0 {
			c.EstimatedLatencyMS = h.LatencyMS
		}
	}
	if r := g.outlier(c.PriceMinor); r != "" {
		return reject(RejectPriceOutlier, "lists "+r)
	}
	return c, nil
}

// after checks a live quote: what the provider asks right now, against what
// it lists and what its peers ask.
func (g guards) after(c routing.Candidate, q routing.Quote) *routing.Rejection {
	live := q.Cost.ProviderMinor
	if c.PriceMinor > 0 && live*100 > c.PriceMinor*listingTolerancePct {
		return &routing.Rejection{CandidateID: c.ID, Provider: c.Provider, Code: RejectPriceAboveListing,
			Detail: fmt.Sprintf("asks %s; its listing says %s", usd(live), usd(c.PriceMinor))}
	}
	if r := g.outlier(live); r != "" {
		return &routing.Rejection{CandidateID: c.ID, Provider: c.Provider, Code: RejectPriceOutlier, Detail: "asks " + r}
	}
	return nil
}

// outlier describes a price that is a trap or far above its peers, or "".
func (g guards) outlier(price int64) string {
	switch {
	case price >= trapPriceMinor:
		return usd(price) + " for one call"
	case g.median > 0 && price > outlierFloorMinor && price > g.median*outlierFactor:
		return fmt.Sprintf("%s, %d× the usual %s for this work", usd(price), price/g.median, usd(g.median))
	}
	return ""
}

// usd formats micro-USDC for a person.
func usd(minor int64) string {
	if minor%10_000 == 0 {
		return fmt.Sprintf("$%.2f", float64(minor)/1e6)
	}
	return fmt.Sprintf("$%.6g", float64(minor)/1e6)
}

// priceOnly asks a candidate for its price for an input without any intent:
// what the health probe and the policy dry run use. Nothing is reserved and
// nothing can be paid.
func (s *ExecutionService) priceOnly(ctx context.Context, c routing.Candidate, input json.RawMessage) (routing.Quote, error) {
	runner, ok := s.runners[c.ExecutionType]
	if !ok {
		return routing.Quote{}, fmt.Errorf("%w: nothing can run %s candidates yet", shared.ErrNotImplemented, c.ExecutionType)
	}
	in, err := c.Input.Apply(input)
	if err != nil {
		return routing.Quote{}, err
	}
	return runner.Quote(ctx, c, in)
}

// SetHealth gives the router the health probe's findings.
func (s *ExecutionService) SetHealth(h *HealthService) { s.health = h }
