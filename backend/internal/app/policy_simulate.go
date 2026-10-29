package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
	"github.com/project-algebra/algebra/policy"
)

// PolicyReader is what a dry run reads from the economic store without
// taking a lock: the same numbers the coordinator checks under one.
type PolicyReader interface {
	PassSpend(ctx context.Context, passID string, since, now time.Time) (int64, error)
	PassAttemptsRecent(ctx context.Context, passID, provider string, since time.Time) (int, error)
	ProviderPaidBy(ctx context.Context, principalID, provider string) (bool, error)
}

// VaultReader reads a pass's vault for a payment on a network.
type VaultReader interface {
	VaultStatus(ctx context.Context, passID, network string) VaultState
}

// vaultDetail is the plain reason a vault code gives in a refusal.
func vaultDetail(code string) string {
	switch code {
	case DenyOnchainRequired:
		return "this network pays only from a Spend Pass vault, and this pass has none"
	case DenyOnchainUnfunded:
		return "the vault holds less than this call costs"
	case DenyOnchainFrozen:
		return "the pass is frozen on Solana by its owner"
	case DenyOnchainRevoked:
		return "the pass was revoked on Solana by its owner"
	case DenyOnchainExpired:
		return "the pass has expired on Solana"
	case DenyOnchainLimit:
		return "over a limit the pass enforces on Solana"
	case DenyOnchainAgentChanged:
		return "the vault was made for an earlier Algebra payer key: withdraw it and make a new one"
	case DenyOnchainMissing:
		return "the pass is not on Solana any more (closed by its owner)"
	case DenyOnchainOtherNetwork:
		return "the pass's vault is on another network"
	}
	return code
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

// Decision is a verdict in the words the console shows: ALLOW runs, HOLD
// waits for a person's yes, BLOCK is refused and nothing is paid.
func (v Verdict) Decision() string {
	switch v {
	case VerdictAllow:
		return "ALLOW"
	case VerdictRequireApproval:
		return "HOLD"
	}
	return "BLOCK"
}

// Seller is what Algebra knows about who it would pay, as separate facts: a
// listing is not a quote, a payment that worked is not a verified identity.
type Seller struct {
	// Known: the provider registry has this endpoint at all.
	Known bool `json:"known"`
	// Trust is "verified" (identity: every check of the standard passed and a
	// person reviewed who runs it), "unverified" or "blocked". Empty when unknown.
	Trust string `json:"trust,omitempty"`
	// PaymentTested: Algebra has paid this endpoint and it delivered. It says
	// a transaction worked and nothing about who the seller is.
	PaymentTested bool `json:"payment_tested"`
	// PayeePinned: a payee is pinned, so a different one is refused.
	PayeePinned bool `json:"payee_pinned"`
}

// CandidateVerdict is one provider's fate in a dry run.
type CandidateVerdict struct {
	Provider    string `json:"provider"`
	CandidateID string `json:"candidate_id,omitempty"`
	Network     string `json:"network,omitempty"`
	PriceMinor  int64  `json:"price_minor"`
	// TotalMinor is the estimated total cost: the provider's price plus any fee.
	TotalMinor  int64    `json:"total_minor"`
	PriceSource string   `json:"price_source"` // "live" or "listed"
	Verdict     Verdict  `json:"verdict"`
	Decision    string   `json:"decision"`
	Reasons     []string `json:"reasons,omitempty"`
	Seller      Seller   `json:"seller"`
}

// Recommendation is one priced offer as a person compares it: who, how much,
// how fast and how that speed is known, how reliable, what is known about
// the seller, and whether paying it would run, wait or be refused.
type Recommendation struct {
	// Strategy is cheapest, balanced or fastest on a recommendation; empty
	// on a compared offer.
	Strategy    string `json:"strategy,omitempty"`
	Rank        int    `json:"rank,omitempty"`
	Provider    string `json:"provider"`
	CandidateID string `json:"candidate_id"`
	Network     string `json:"network,omitempty"`
	// TotalMinor is the estimated total cost; PriceSource is "live" (an
	// unpaid request asked it), "listed" or "unlisted" (the catalog states
	// no price: unknown until asked).
	TotalMinor  int64  `json:"total_minor"`
	PriceSource string `json:"price_source"`
	// LatencyMS is the speed it was ranked on, and LatencySource how it is
	// known: "observed", "estimate", "price_check" or "" (unknown).
	LatencyMS     int    `json:"latency_ms,omitempty"`
	LatencySource string `json:"latency_source,omitempty"`
	// Reliability is the share of calls expected to deliver (0 to 1), from
	// Calls of Algebra's own calls smoothed towards a prior when they are few.
	Reliability float64  `json:"reliability"`
	Calls       int      `json:"calls,omitempty"`
	Score       float64  `json:"score"`
	Decision    string   `json:"decision"`
	Reasons     []string `json:"reasons,omitempty"`
	Seller      Seller   `json:"seller"`
	Notes       []string `json:"notes,omitempty"`
	Test        bool     `json:"test,omitempty"`
}

// recommendFor ranks the same options each way and keeps each way's winner.
func recommendFor(opts []routing.Option, verdicts map[string]CandidateVerdict) []Recommendation {
	if len(opts) == 0 {
		return nil
	}
	out := make([]Recommendation, 0, 3)
	for _, w := range []struct {
		name string
		mode routing.Mode
	}{{econ.StrategyCheapest, routing.ModeCheapest}, {econ.StrategyBalanced, routing.ModeAuto}, {econ.StrategyFastest, routing.ModeFastest}} {
		steps := routing.Rank(w.mode, opts)
		r := recommendationOf(steps[0], verdicts[steps[0].Quote.CandidateID])
		r.Strategy, r.Rank = w.name, 0
		out = append(out, r)
	}
	return out
}

// compareFor is every option ranked for one mode.
func compareFor(mode routing.Mode, opts []routing.Option, verdicts map[string]CandidateVerdict) []Recommendation {
	steps := routing.Rank(mode, opts)
	out := make([]Recommendation, 0, len(steps))
	for _, st := range steps {
		out = append(out, recommendationOf(st, verdicts[st.Quote.CandidateID]))
	}
	return out
}

func recommendationOf(st routing.PlanStep, cv CandidateVerdict) Recommendation {
	r := Recommendation{
		Rank: st.Rank, Provider: st.Quote.Provider, CandidateID: st.Quote.CandidateID, Network: st.Quote.Network,
		TotalMinor: cv.TotalMinor, PriceSource: cv.PriceSource, LatencyMS: st.Score.LatencyMS, LatencySource: st.Score.LatencySource,
		Reliability: st.Score.Components[routing.ScoreReliability], Calls: st.Score.Calls, Score: st.Score.Total,
		Decision: cv.Verdict.Decision(), Reasons: cv.Reasons, Seller: cv.Seller, Notes: st.Score.Notes, Test: st.Quote.Test,
	}
	if cv.PriceSource == "unlisted" {
		r.TotalMinor = 0
	}
	return r
}

// Simulation is the dry run's answer: the verdict, why, what the plan would
// be and what each provider would meet. Nothing was reserved or paid.
type Simulation struct {
	Verdict Verdict `json:"verdict"`
	// Decision is Verdict as ALLOW, HOLD or BLOCK.
	Decision string   `json:"decision"`
	Reasons  []string `json:"reasons"`
	// Strategy is how the plan was ranked: CHEAPEST, FASTEST or AUTO.
	Strategy string `json:"strategy,omitempty"`
	// WouldPay is the first plan step: who would be paid and how much.
	WouldPay   *CandidateVerdict   `json:"would_pay,omitempty"`
	Plan       *RoutingSummary     `json:"plan,omitempty"`
	Candidates []CandidateVerdict  `json:"candidates"`
	Rejected   []routing.Rejection `json:"rejected,omitempty"`
	// Recommendations is the best offer for each way of choosing (cheapest,
	// balanced, fastest), from the same priced offers; Compared is every
	// offer that could be paid or approved, ranked for the request's own
	// strategy. They include offers that would wait for the person (HOLD).
	Recommendations []Recommendation `json:"recommendations,omitempty"`
	Compared        []Recommendation `json:"compared,omitempty"`
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
	sim, err := s.simulate(ctx, req)
	if sim != nil {
		sim.Decision = sim.Verdict.Decision()
		for i := range sim.Candidates {
			sim.Candidates[i].Decision = sim.Candidates[i].Verdict.Decision()
		}
	}
	return sim, err
}

func (s *ExecutionService) simulate(ctx context.Context, req SimulateRequest) (*Simulation, error) {
	p, err := s.econ.passes.GetByAgent(ctx, req.AgentID)
	if err != nil {
		return nil, fmt.Errorf("%w: this agent has no Spend Pass", shared.ErrUnauthorized)
	}
	now := s.now()
	if req.Spec.Currency == "" {
		req.Spec.Currency = p.Currency
	}
	// The limits the intent itself would get: a demo account stays in the
	// sandbox and a project's pass in its environment, so a sandbox key's dry
	// run never names a mainnet provider it could not pay. With mainnet
	// payments off, a mainnet dry run still prices and compares, and says no.
	var hardDeny, envDeny []string
	if err := s.econ.restrictDemo(ctx, p.UserID, &req.Spec); err != nil {
		var d *AuthorityDenied
		if !errors.As(err, &d) {
			return nil, err
		}
		hardDeny = d.ReasonCodes
	} else if err := s.econ.restrictEnvironment(p, &req.Spec); err != nil {
		var d *AuthorityDenied
		if !errors.As(err, &d) {
			return nil, err
		}
		if slices.Contains(d.ReasonCodes, DenyMainnetDisabled) {
			envDeny = d.ReasonCodes
		} else {
			hardDeny = d.ReasonCodes
		}
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
	if len(hardDeny) > 0 {
		sim.Verdict, sim.Reasons = VerdictDeny, hardDeny
		return sim, nil
	}

	reader, _ := s.econ.store.(PolicyReader)
	var spent int64
	if reader != nil {
		if spent, err = reader.PassSpend(ctx, p.ID, p.WindowStart(now), now); err != nil {
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

	// Every candidate: the pass, the router's guards, the pass's rules, with
	// the same record of each provider the router ranks on.
	g := s.newGuards(ctx, req.Candidates)
	var norm []routing.Candidate
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
		norm = append(norm, c)
	}
	s.attachHistory(ctx, norm)

	type entry struct {
		c  routing.Candidate
		cv CandidateVerdict
	}
	var pending []entry
	for _, c := range norm {
		cv := CandidateVerdict{Provider: c.Provider, CandidateID: c.ID, Network: c.Network, PriceMinor: c.PriceMinor, TotalMinor: c.PriceMinor, PriceSource: "listed", Verdict: VerdictAllow}
		if e, ok := g.trust[c.ID]; ok {
			cv.Seller = Seller{Known: true, Trust: string(e.Trust), PaymentTested: e.TestedAt != nil, PayeePinned: e.PinnedPayTo != ""}
		}
		var held *routing.Rejection
		if c, held = g.before(view, c); held != nil {
			cv.Verdict, cv.Reasons = VerdictDeny, []string{held.Code + ": " + held.Detail}
			sim.Rejected = append(sim.Rejected, *held)
			sim.Candidates = append(sim.Candidates, cv)
			continue
		}
		pending = append(pending, entry{c, cv})
	}
	// Live prices are asked all at once, as the router asks them.
	var live []pricing
	if req.LiveQuotes && len(pending) > 0 {
		cs := make([]routing.Candidate, len(pending))
		for i, e := range pending {
			cs[i] = e.c
		}
		live = s.quoteAll(ctx, view, cs)
	}

	var opts, gated, vaultBlocked []routing.Option
	var gatedOnly []CandidateVerdict
	var vaultCodes []string
	// One read of the vault per network, shared by every candidate on it.
	vaultCache := map[string]VaultState{}
	vaultOf := func(network string) VaultState {
		if s.vaults == nil || network == "" {
			return VaultState{}
		}
		key := chain.NormalizeNetwork(network)
		if v, ok := vaultCache[key]; ok {
			return v
		}
		v := s.vaults.VaultStatus(ctx, p.ID, network)
		vaultCache[key] = v
		return v
	}
	verdicts := map[string]CandidateVerdict{}
	verified := map[string]bool{}
	for i, e := range pending {
		c, cv := e.c, e.cv
		deny := func(r routing.Rejection) {
			cv.Verdict, cv.Reasons = VerdictDeny, append(cv.Reasons, r.Code+": "+r.Detail)
			sim.Rejected = append(sim.Rejected, r)
			sim.Candidates = append(sim.Candidates, cv)
		}
		hold := func(code string, q routing.Quote) {
			cv.Verdict, cv.Reasons = VerdictRequireApproval, append(cv.Reasons, code)
			gatedOnly = append(gatedOnly, cv)
			gated = append(gated, routing.Option{Candidate: c, Quote: q})
			verdicts[c.ID] = cv
			sim.Candidates = append(sim.Candidates, cv)
		}
		q := routing.Quote{
			ID: "sim", CandidateID: c.ID, Capability: c.Capability, Provider: c.Provider, ExecutionType: c.ExecutionType, Endpoint: c.Endpoint,
			Method: c.Method, Cost: routing.Cost{ProviderMinor: c.PriceMinor}, Asset: c.Asset, Network: c.Network,
			EstimatedLatencyMS: c.EstimatedLatencyMS, QuotedAt: now, ValidUntil: now.Add(s.quoteTTL), Test: c.Network != "" && strings.Contains(c.Network, "devnet"),
		}
		switch {
		case live != nil:
			if live[i].err != nil {
				deny(routing.Rejection{CandidateID: c.ID, Provider: c.Provider, Code: routing.RejectUnquotable, Detail: briefly(live[i].err.Error())})
				continue
			}
			q, cv.PriceSource, cv.PriceMinor, cv.TotalMinor = live[i].q, "live", live[i].q.Cost.ProviderMinor, live[i].q.Cost.Total()
			if r := g.after(c, q); r != nil {
				deny(*r)
				continue
			}
		case c.PriceMinor == 0:
			// The catalog states no price. It is unknown until asked, so it is
			// ranked as if it cost the whole budget, never as if it were free.
			cv.PriceSource = "unlisted"
			q.Cost.ProviderMinor = in.BudgetMaxMinor
			if q.Asset == "" {
				q.Asset = in.Currency
			}
		}
		if r := screen(view, c, q); r != nil {
			deny(*r)
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
			continue
		case policy.RequireApproval:
			cv.Verdict, cv.Reasons = VerdictRequireApproval, append(cv.Reasons, d.ReasonCodes...)
		}
		// A network that pays only from a vault: the vault must be able to pay
		// this much. The offer stays on the list, refused, so a person sees what
		// they would buy once they have one.
		priced := int64(0)
		if cv.PriceSource != "unlisted" {
			priced = q.Cost.Total()
		}
		if code := vaultOf(c.Network).For(priced); code != "" {
			cv.Verdict, cv.Reasons = VerdictDeny, []string{code + ": " + vaultDetail(code)}
			verdicts[c.ID] = cv
			sim.Candidates = append(sim.Candidates, cv)
			sim.Rejected = append(sim.Rejected, routing.Rejection{CandidateID: c.ID, Provider: c.Provider, Code: code, Detail: vaultDetail(code)})
			vaultBlocked = append(vaultBlocked, routing.Option{Candidate: c, Quote: q})
			vaultCodes = append(vaultCodes, code)
			continue
		}
		if reader != nil {
			one, _ := reader.PassAttemptsRecent(ctx, p.ID, c.Provider, now.Add(-time.Minute))
			if code := ctrl.Velocity(sim.Pass.CallsLastMinute, one); code != "" {
				deny(routing.Rejection{CandidateID: c.ID, Provider: c.Provider, Code: RejectProviderRateLimited, Detail: code})
				continue
			}
			paid, _ := reader.ProviderPaidBy(ctx, p.UserID, c.Provider)
			if code, _ := ctrl.NewProvider(amount, paid, false); code != "" {
				hold(code, q)
				continue
			}
		}
		// The pass's rule on providers whose identity isn't verified.
		if ctrl.UnverifiedProviders != spendpass.UnverifiedAllow {
			v, ok := verified[c.Provider]
			if !ok {
				v = providerVerifiedBy(ctx, s.econ.trust, c.Provider, s.log)
				verified[c.Provider] = v
			}
			if code, approvable := ctrl.Unverified(v, false); code != "" {
				if approvable {
					hold(code, q)
				} else {
					deny(routing.Rejection{CandidateID: c.ID, Provider: c.Provider, Code: routing.RejectPolicy, Detail: code})
				}
				continue
			}
		}
		// The pass's rule on endpoints found only on the open web.
		if code, approvable := ctrl.WebFound(c.Trust() == routing.TrustUnverified, false); code != "" {
			if approvable {
				hold(code, q)
			} else {
				deny(routing.Rejection{CandidateID: c.ID, Provider: c.Provider, Code: routing.RejectPolicy, Detail: code})
			}
			continue
		}
		verdicts[c.ID] = cv
		sim.Candidates = append(sim.Candidates, cv)
		opts = append(opts, routing.Option{Candidate: c, Quote: q})
	}

	mode, err := routing.ParseMode(in.ProviderPolicy.Strategy)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", shared.ErrConflict, err)
	}
	sim.Strategy = string(mode)
	// The cards a person chooses from: each way's winner, and the full field,
	// counting the offers that would wait for their yes.
	if rankable := append(append(slices.Clone(opts), gated...), vaultBlocked...); len(rankable) > 0 {
		sim.Recommendations = recommendFor(rankable, verdicts)
		sim.Compared = compareFor(mode, rankable, verdicts)
	}
	defer func() {
		// Mainnet payments are off here: the comparison stands, nothing is payable.
		if len(envDeny) == 0 {
			return
		}
		sim.Verdict, sim.Reasons = VerdictDeny, envDeny
		for _, list := range [][]Recommendation{sim.Recommendations, sim.Compared} {
			for i := range list {
				list[i].Decision, list[i].Reasons = VerdictDeny.Decision(), envDeny
			}
		}
	}()
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
		// The request asked for a person's yes: stricter, never looser.
		if req.Spec.AskFirst && sim.Verdict == VerdictAllow {
			sim.Verdict = VerdictRequireApproval
			sim.Reasons = append(sim.Reasons, ReasonAskedFirst)
		}
	case len(gatedOnly) > 0:
		sim.Verdict = VerdictRequireApproval
		sim.Reasons = slices.Clone(gatedOnly[0].Reasons)
		sim.WouldPay = &gatedOnly[0]
	case len(vaultBlocked) > 0:
		// Everything that could be bought needs a vault (or a funded one).
		sim.Verdict = VerdictDeny
		sim.Reasons = []string{vaultCodes[0]}
	default:
		sim.Verdict = VerdictDeny
		sim.Reasons = []string{"no_provider_passes"}
	}
	return sim, nil
}
