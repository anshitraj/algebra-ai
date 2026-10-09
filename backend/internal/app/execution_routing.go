package app

import (
	"github.com/project-algebra/algebra/internal/domain/routing"
)

// RoutingSummary says how a provider was chosen, in a form an agent, the
// console and a log can all read: the strategy, and every offer the router
// priced and ranked, best first, with the reasons. It carries no payment
// requirements or payee: those belong to the quote, not the explanation.
type RoutingSummary struct {
	// Mode is CHEAPEST, FASTEST or AUTO, from the intent's strategy.
	Mode string `json:"mode"`
	// PlanHash commits to the ranked quotes; the receipt carries the same hash.
	PlanHash string `json:"plan_hash"`
	// Offers are the ranked steps. The first is the one tried first; the rest
	// are the fallbacks, used only if an attempt is proven to have moved no money.
	Offers []RoutedOffer `json:"offers"`
}

// RoutedOffer is one ranked step.
type RoutedOffer struct {
	Rank       int                `json:"rank"`
	Provider   string             `json:"provider"`
	Network    string             `json:"network,omitempty"`
	CostMinor  int64              `json:"cost_minor"`
	LatencyMS  int                `json:"expected_latency_ms,omitempty"`
	Trust      string             `json:"trust"`
	Score      float64            `json:"score"`
	Components map[string]float64 `json:"components,omitempty"`
	Notes      []string           `json:"notes,omitempty"`
	Test       bool               `json:"test,omitempty"`
}

// SummarizePlan reads a plan into its explanation. Nil for no plan.
func SummarizePlan(p *routing.ExecutionPlan) *RoutingSummary {
	if p == nil {
		return nil
	}
	out := &RoutingSummary{Mode: string(p.Mode), PlanHash: p.Hash, Offers: make([]RoutedOffer, 0, len(p.Steps))}
	for _, st := range p.Steps {
		out.Offers = append(out.Offers, RoutedOffer{
			Rank: st.Rank, Provider: st.Quote.Provider, Network: st.Quote.Network, CostMinor: st.Quote.Cost.Total(),
			LatencyMS: st.Quote.EstimatedLatencyMS, Trust: string(st.Trust), Score: st.Score.Total,
			Components: st.Score.Components, Notes: st.Score.Notes, Test: st.Quote.Test,
		})
	}
	return out
}

// rankingEvent is the ranking as a log entry: small, and enough to see why a
// provider was first.
func rankingEvent(p *routing.ExecutionPlan) []map[string]any {
	out := make([]map[string]any, 0, len(p.Steps))
	for _, st := range p.Steps {
		out = append(out, map[string]any{
			"rank": st.Rank, "provider": st.Quote.Provider, "cost_minor": st.Quote.Cost.Total(), "score": st.Score.Total, "notes": st.Score.Notes,
		})
	}
	return out
}
