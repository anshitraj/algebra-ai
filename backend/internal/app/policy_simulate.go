package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
	"github.com/project-algebra/algebra/policy"
)

// PolicyReader is what a dry run reads from the economic store without
// taking a lock: the same numbers the coordinator checks under one.
type PolicyReader interface {
	PassSpend(ctx context.Context, passID string, since time.Time) (int64, error)
	PassAttemptsRecent(ctx context.Context, passID, provider string, since time.Time) (int, error)
	ProviderPaidBy(ctx context.Context, principalID, provider string) (bool, error)
}

// SimulateRequest is "would this be allowed, and who would do it?".
type SimulateRequest struct {
	// AgentID is the agent whose Spend Pass is asked; the caller resolves it
	// (the console's agent names one of the person's passes).
	AgentID    string
	Spec       econ.Spec
	Candidates []routing.Candidate
	// LiveQuotes asks each candidate for its price with an unpaid request.
	// Without it, nothing leaves Algebra and listed prices stand in.
	LiveQuotes bool
}

// Verdict is a dry run's overall answer.
type Verdict string

const (
	VerdictAllow           Verdict = "ALLOW"
	VerdictRequireApproval Verdict = "REQUIRE_APPROVAL"
	VerdictDeny            Verdict = "DENY"
)

// CandidateVerdict is one provider's fate in a dry run.
type CandidateVerdict struct {
	Provider    string   `json:"provider"`
	CandidateID string   `json:"candidate_id,omitempty"`
	Network     string   `json:"network,omitempty"`
	PriceMinor  int64    `json:"price_minor"`
	PriceSource string   `json:"price_source"` // "live" or "listed"
	Verdict     Verdict  `json:"verdict"`
	Reasons     []string `json:"reasons,omitempty"`
}

// Simulation is the dry run's answer: the verdict, why, what the plan would
// be and what each provider would meet. Nothing was reserved or paid.
type Simulation struct {
	Verdict Verdict  `json:"verdict"`
	Reasons []string `json:"reasons"`
	// WouldPay is the first plan step: who would be paid and how much.
	WouldPay   *CandidateVerdict   `json:"would_pay,omitempty"`
	Plan       *RoutingSummary     `json:"plan,omitempty"`
	Candidates []CandidateVerdict  `json:"candidates"`
	Rejected   []routing.Rejection `json:"rejected,omitempty"`
	// Pass is what the Spend Pass looked like for this question.
	Pass struct {
		ID              string             `json:"id"`
		RemainingMinor  int64              `json:"remaining_minor"`
		CallsLastMinute int                `json:"calls_last_minute"`
		Controls        spendpass.Controls `json:"controls"`
		Frozen          bool               `json:"frozen"`
	} `json:"pass"`
	LiveQuotes bool      `json:"live_quotes"`
	At         time.Time `json:"at"`
}

// Simulate answers a dry run. It builds the intent the request would create
// without storing it, applies the Spend Pass, the pass's controls and the
// router's guards to every candidate, and ranks what is left. No intent is
// created, nothing is reserved, and nothing is paid; with LiveQuotes the only
// thing that leaves Algebra is the same unpaid request a quote is.
func (s *ExecutionService) Simulate(ctx context.Context, req SimulateRequest) (*Simulation, error) {
	p, err := s.econ.passes.GetByAgent(ctx, req.AgentID)
	if err != nil {
		return nil, fmt.Errorf("%w: this agent has no Spend Pass", shared.ErrUnauthorized)
	}
	now := s.now()
	if req.Spec.Currency == "" {
		req.Spec.Currency = p.Currency
	}
	in, err := econ.New("sim", p.UserID, p.ID, req.AgentID, req.Spec, now)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", shared.ErrConflict, err.Error())
	}
	if c, ok := routing.ClassByID(in.Capability); ok {
		if err := c.CheckInput(in.Input); err != nil {
			return nil, fmt.Errorf("%w: %s", shared.ErrConflict, err.Error())
		}
	}
	view := &IntentView{Intent: *in}
	sim := &Simulation{Candidates: []CandidateVerdict{}, LiveQuotes: req.LiveQuotes, At: now.UTC()}
	ctrl := p.Controls.Effective()
	sim.Pass.ID, sim.Pass.Controls, sim.Pass.Frozen = p.ID, ctrl, p.FrozenAt != nil

	reader, _ := s.econ.store.(PolicyReader)
	var spent int64
	if reader != nil {
		if spent, err = reader.PassSpend(ctx, p.ID, p.WindowStart(now)); err != nil {
			return nil, err
		}
		if sim.Pass.CallsLastMinute, err = reader.PassAttemptsRecent(ctx, p.ID, "", now.Add(-time.Minute)); err != nil {
			return nil, err
		}
	}
	if o, err := s.econ.ordersSpent(ctx, p); err == nil {
		spent += o
	}
	sim.Pass.RemainingMinor = p.Remaining(spent)

	// The intent as a whole: what the pass says about its ceiling.
	whole := evaluate(p, "", in.BudgetMaxMinor, spent, in.Currency, now)
	if whole.Decision == policy.Deny {
		sim.Verdict, sim.Reasons = VerdictDeny, whole.ReasonCodes
		return sim, nil
	}
	if code := ctrl.Velocity(sim.Pass.CallsLastMinute, 0); code == spendpass.ReasonRateLimited {
		sim.Verdict, sim.Reasons = VerdictDeny, []string{code}
		return sim, nil
	}
	approvalAtCreate := whole.Decision == policy.RequireApproval

	// Every candidate: the pass, the router's guards, the new-provider rule.
	g := s.newGuards(ctx, req.Candidates)
	var opts []routing.Option
	var gatedOnly []CandidateVerdict
	seen := map[string]bool{}
	for _, raw := range req.Candidates {
		c, err := raw.Normalize()
		if err != nil {
			sim.Rejected = append(sim.Rejected, routing.Rejection{Provider: raw.Provider, Code: routing.RejectUnquotable, Detail: err.Error()})
			continue
		}
		if seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		cv := CandidateVerdict{Provider: c.Provider, CandidateID: c.ID, Network: c.Network, PriceMinor: c.PriceMinor, PriceSource: "listed", Verdict: VerdictAllow}
		deny := func(r routing.Rejection) {
			cv.Verdict, cv.Reasons = VerdictDeny, append(cv.Reasons, r.Code+": "+r.Detail)
			sim.Rejected = append(sim.Rejected, r)
		}
		var held *routing.Rejection
		if c, held = g.before(view, c); held != nil {
			deny(*held)
			sim.Candidates = append(sim.Candidates, cv)
			continue
		}
		q := routing.Quote{
			ID: "sim", CandidateID: c.ID, Capability: c.Capability, Provider: c.Provider, ExecutionType: c.ExecutionType, Endpoint: c.Endpoint,
			Method: c.Method, Cost: routing.Cost{ProviderMinor: c.PriceMinor}, Asset: c.Asset, Network: c.Network,
			EstimatedLatencyMS: c.EstimatedLatencyMS, QuotedAt: now, ValidUntil: now.Add(s.quoteTTL), Test: c.Network != "" && strings.Contains(c.Network, "devnet"),
		}
		if req.LiveQuotes {
			live := s.quoteAll(ctx, view, []routing.Candidate{c})[0]
			if live.err != nil {
				deny(routing.Rejection{CandidateID: c.ID, Provider: c.Provider, Code: routing.RejectUnquotable, Detail: briefly(live.err.Error())})
				sim.Candidates = append(sim.Candidates, cv)
				continue
			}
			q, cv.PriceSource, cv.PriceMinor = live.q, "live", live.q.Cost.ProviderMinor
			if r := g.after(c, q); r != nil {
				deny(*r)
				sim.Candidates = append(sim.Candidates, cv)
				continue
			}
		}
		if r := screen(view, c, q); r != nil {
			deny(*r)
			sim.Candidates = append(sim.Candidates, cv)
			continue
		}
		amount := q.Cost.Total()
		if amount == 0 {
			amount = in.BudgetMaxMinor
		}
		d := evaluate(p, c.Provider, amount, spent, in.Currency, now)
		switch d.Decision {
		case policy.Deny:
			deny(routing.Rejection{CandidateID: c.ID, Provider: c.Provider, Code: routing.RejectPolicy, Detail: strings.Join(d.ReasonCodes, ", ")})
			sim.Candidates = append(sim.Candidates, cv)
			continue
		case policy.RequireApproval:
			cv.Verdict, cv.Reasons = VerdictRequireApproval, append(cv.Reasons, d.ReasonCodes...)
		}
		if reader != nil {
			one, _ := reader.PassAttemptsRecent(ctx, p.ID, c.Provider, now.Add(-time.Minute))
			if code := ctrl.Velocity(sim.Pass.CallsLastMinute, one); code != "" {
				deny(routing.Rejection{CandidateID: c.ID, Provider: c.Provider, Code: RejectProviderRateLimited, Detail: code})
				sim.Candidates = append(sim.Candidates, cv)
				continue
			}
			paid, _ := reader.ProviderPaidBy(ctx, p.UserID, c.Provider)
			if code, _ := ctrl.NewProvider(amount, paid, false); code != "" {
				cv.Verdict, cv.Reasons = VerdictRequireApproval, append(cv.Reasons, code)
				gatedOnly = append(gatedOnly, cv)
				sim.Candidates = append(sim.Candidates, cv)
				continue
			}
		}
		sim.Candidates = append(sim.Candidates, cv)
		opts = append(opts, routing.Option{Candidate: c, Quote: q})
	}

	mode, err := routing.ParseMode(in.ProviderPolicy.Strategy)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", shared.ErrConflict, err)
	}
	switch {
	case len(opts) > 0:
		steps := routing.Rank(mode, opts)
		if len(steps) > routing.MaxPlanSteps {
			steps = steps[:routing.MaxPlanSteps]
		}
		if plan, err := routing.NewPlan("sim", routing.PlanSpec{Capability: in.Capability, Mode: mode, Steps: steps}, now); err == nil {
			sim.Plan = SummarizePlan(plan)
			first := steps[0].Quote
			i := slices.IndexFunc(sim.Candidates, func(c CandidateVerdict) bool { return c.CandidateID == first.CandidateID })
			if i >= 0 {
				sim.WouldPay = &sim.Candidates[i]
			}
		}
		sim.Verdict = VerdictAllow
		if approvalAtCreate || (sim.WouldPay != nil && sim.WouldPay.Verdict == VerdictRequireApproval) {
			sim.Verdict = VerdictRequireApproval
			sim.Reasons = append(sim.Reasons, whole.ReasonCodes...)
		}
	case len(gatedOnly) > 0:
		sim.Verdict = VerdictRequireApproval
		sim.Reasons = []string{spendpass.ReasonNewProviderApproval}
		sim.WouldPay = &gatedOnly[0]
	default:
		sim.Verdict = VerdictDeny
		sim.Reasons = []string{"no_provider_passes"}
	}
	return sim, nil
}
