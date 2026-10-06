package routing

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
)

// MaxPlanSteps bounds how deep a fallback chain can go. Each step is another
// provider that might be paid, so the chain is short on purpose.
const MaxPlanSteps = 5

// Score explains why a step ranks where it does. Components are named so the
// explanation reads the same in the console, a receipt and a log: "cost",
// "latency", "quality", "reliability", "trust" and "price_honesty" (see
// Rank), and capability-specific ones (output, slippage) for a trade.
type Score struct {
	// Total is 0 to 1; higher is better.
	Total      float64            `json:"total"`
	Components map[string]float64 `json:"components,omitempty"`
	Weights    map[string]float64 `json:"weights,omitempty"`
	Notes      []string           `json:"notes,omitempty"`
}

// PlanStep is one ranked option: a live quote, how far its provider is
// trusted, and why it ranks here.
type PlanStep struct {
	// Rank is 1 for the primary choice; NewPlan assigns it from position.
	Rank  int   `json:"rank"`
	Quote Quote `json:"quote"`
	Trust Trust `json:"trust"`
	Score Score `json:"score"`
}

// Rejection codes: why a candidate didn't make the plan. One vocabulary for
// the console, receipts and logs.
const (
	RejectPolicy          = "policy_denied"
	RejectOverBudget      = "over_budget"
	RejectNetwork         = "network_not_allowed"
	RejectAsset           = "asset_not_allowed"
	RejectReliability     = "below_min_reliability"
	RejectQuality         = "below_min_quality"
	RejectLatency         = "too_slow"
	RejectSlippage        = "slippage_too_high"
	RejectStaleQuote      = "quote_expired"
	RejectUnquotable      = "no_quote"
	RejectUnknownProvider = "unknown_provider"
	RejectExcluded        = "provider_not_allowed"
	RejectLookalikeAsset  = "lookalike_asset"
)

// Rejection records one candidate that was considered and left out.
type Rejection struct {
	CandidateID string `json:"candidate_id,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Code        string `json:"code"`
	Detail      string `json:"detail,omitempty"`
}

// ExecutionPlan is the router's answer for one intent: a ranked list of live
// quotes. Steps[0] is the primary; the rest are fallbacks, each to be tried
// only when the attempt before it is proven to have moved no money. The
// plan decides order; the economic coordinator decides whether a next
// attempt is safe.
type ExecutionPlan struct {
	ID         string      `json:"id"`
	IntentID   string      `json:"intent_id,omitempty"`
	IntentHash string      `json:"intent_hash,omitempty"`
	Capability string      `json:"capability"`
	Mode       Mode        `json:"mode"`
	Steps      []PlanStep  `json:"steps"`
	Rejected   []Rejection `json:"rejected,omitempty"`
	CreatedAt  time.Time   `json:"created_at"`
	// Hash commits to the intent, the mode and the ordered quotes.
	Hash string `json:"hash"`
}

// PlanSpec is what a router hands NewPlan.
type PlanSpec struct {
	IntentID   string
	IntentHash string
	Capability string
	Mode       Mode
	Steps      []PlanStep
	Rejected   []Rejection
}

// NewPlan validates a plan and seals it with a hash. It refuses a plan with
// no steps, too many, a step for another capability, the same candidate
// twice, or a quote that has already expired: a plan is only worth making
// from offers that can still be paid.
func NewPlan(id string, s PlanSpec, now time.Time) (*ExecutionPlan, error) {
	capability, err := econ.NormalizeCapability(s.Capability)
	if err != nil {
		return nil, err
	}
	if !s.Mode.Valid() {
		return nil, fmt.Errorf("plan mode must be CHEAPEST, FASTEST or AUTO, not %q", s.Mode)
	}
	if len(s.Steps) == 0 {
		return nil, errors.New("a plan needs at least one step")
	}
	if len(s.Steps) > MaxPlanSteps {
		return nil, fmt.Errorf("a plan has at most %d steps", MaxPlanSteps)
	}
	steps := slices.Clone(s.Steps)
	seen := make(map[string]bool, len(steps))
	for i := range steps {
		q := steps[i].Quote
		if err := q.Validate(); err != nil {
			return nil, fmt.Errorf("step %d: %w", i+1, err)
		}
		if q.Capability != capability {
			return nil, fmt.Errorf("step %d quotes %q, not %q", i+1, q.Capability, capability)
		}
		if seen[q.CandidateID] {
			return nil, fmt.Errorf("step %d repeats candidate %s", i+1, q.CandidateID)
		}
		seen[q.CandidateID] = true
		if q.Expired(now) {
			return nil, fmt.Errorf("step %d: the quote expired at %s", i+1, q.ValidUntil.UTC().Format(time.RFC3339))
		}
		steps[i].Rank = i + 1
	}
	p := &ExecutionPlan{
		ID: id, IntentID: s.IntentID, IntentHash: s.IntentHash, Capability: capability, Mode: s.Mode,
		Steps: steps, Rejected: slices.Clone(s.Rejected), CreatedAt: now.UTC(),
	}
	p.Hash = p.computeHash()
	return p, nil
}

// Primary is the first choice.
func (p *ExecutionPlan) Primary() PlanStep { return p.Steps[0] }

// Fallbacks are the steps after the primary, in order.
func (p *ExecutionPlan) Fallbacks() []PlanStep { return p.Steps[1:] }

// Next is the best-ranked step whose candidate hasn't been attempted and
// whose quote can still be paid at now. Whether trying it is safe is the
// coordinator's call, not the plan's.
func (p *ExecutionPlan) Next(attempted []string, now time.Time) (PlanStep, bool) {
	for _, st := range p.Steps {
		if slices.Contains(attempted, st.Quote.CandidateID) || st.Quote.Expired(now) {
			continue
		}
		return st, true
	}
	return PlanStep{}, false
}

func (p *ExecutionPlan) computeHash() string {
	type step struct {
		Rank      int    `json:"rank"`
		QuoteHash string `json:"quote_hash"`
	}
	v := struct {
		V          int    `json:"v"`
		IntentID   string `json:"intent_id"`
		IntentHash string `json:"intent_hash"`
		Capability string `json:"capability"`
		Mode       Mode   `json:"mode"`
		Steps      []step `json:"steps"`
	}{V: 1, IntentID: p.IntentID, IntentHash: p.IntentHash, Capability: p.Capability, Mode: p.Mode}
	for _, st := range p.Steps {
		v.Steps = append(v.Steps, step{st.Rank, st.Quote.Hash()})
	}
	b, _ := json.Marshal(v)
	return econ.HashBytes(b)
}
