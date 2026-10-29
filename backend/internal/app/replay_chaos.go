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
	"github.com/project-algebra/algebra/internal/domain/spendpass"
	"github.com/project-algebra/algebra/internal/domain/tenancy"
)

// Replay: a finished task's purchases run again, on paper, against sellers
// that misbehave the ways real ones do, under the task's own Spend Pass.
//
// Each purchase is put through the router's dry run (Simulate) with one
// adversarial seller added for each scenario. The adversaries are never
// contacted: their price answers are written into the dry run (Chaos), and
// the sellers the task really had are priced from their listings, so nothing
// leaves Algebra and nothing is paid. What comes back is what the pass's rules
// and Algebra's guards would have done: caught it, held it for the person,
// ranked it below a better offer, or paid it.
//
// An agent that hasn't done a task yet has nothing to replay, so its Spend Pass
// is tested on stand-ins (ReplayPreset): the kinds of work agents usually buy,
// each at the cheapest price a seller lists for it, under the pass's own limits.

// Replay scenarios.
const (
	ChaosOvercharge = "overcharge"
	ChaosHoneypot   = "honeypot"
	ChaosWebLure    = "web_lure"
	ChaosStranger   = "stranger"
	ChaosSellerDown = "seller_down"
	ChaosLoop       = "loop"
)

// Replay outcomes. Every one but would_pay is safe.
const (
	ReplayCaught    = "caught"
	ReplayHeld      = "held"
	ReplayOutranked = "outranked"
	ReplayWouldPay  = "would_pay"
	ReplayFallsBack = "falls_back"
	ReplayStops     = "stops"
)

// honeypotMinor is what the honeypot seller asks: 25 USDC for one call.
const honeypotMinor = 25_000_000

// ReplayFinding is one scenario's outcome for one purchase.
type ReplayFinding struct {
	Scenario string `json:"scenario"`
	Title    string `json:"title"`
	Outcome  string `json:"outcome"`
	Safe     bool   `json:"safe"`
	// AtRiskMinor is what the misbehaving seller asked.
	AtRiskMinor int64  `json:"at_risk_minor"`
	Detail      string `json:"detail"`
}

// ReplayPurchase is one paid purchase of the task (or, in a preset run, one
// kind of work at the price it usually costs) and what each scenario did.
type ReplayPurchase struct {
	// IntentID is empty in a preset run: nothing was bought.
	IntentID   string `json:"intent_id,omitempty"`
	Capability string `json:"capability"`
	// Title is the kind of work in words ("Token price"), for a preset run.
	Title     string          `json:"title,omitempty"`
	Provider  string          `json:"provider"`
	PaidMinor int64           `json:"paid_minor"`
	Findings  []ReplayFinding `json:"findings"`
}

// ReplaySkip is a kind of work a preset run had no seller for.
type ReplaySkip struct {
	Capability string `json:"capability"`
	Title      string `json:"title"`
	Reason     string `json:"reason"`
}

// ReplayReport is a task, or a Spend Pass with no task yet, replayed under chaos.
type ReplayReport struct {
	// JobID is the task replayed; PassID the Spend Pass a preset run tested.
	JobID  string    `json:"job_pass_id,omitempty"`
	PassID string    `json:"spend_pass_id,omitempty"`
	Label  string    `json:"label"`
	RanAt  time.Time `json:"ran_at"`
	// Preset: no task's purchases were replayed; the kinds of work in
	// Purchases stood in for them.
	Preset    bool             `json:"preset,omitempty"`
	Purchases []ReplayPurchase `json:"purchases"`
	// Skipped are the kinds of work a preset run found nobody to buy from.
	Skipped []ReplaySkip `json:"skipped,omitempty"`
	Safe    int          `json:"safe"`
	Unsafe  int          `json:"unsafe"`
	// AtRiskMinor is what every adversary together asked; ProtectedMinor the
	// part of it Algebra would not have paid.
	AtRiskMinor    int64 `json:"at_risk_minor"`
	ProtectedMinor int64 `json:"protected_minor"`
}

// CandidateSource gives the providers that do a piece of work, as the router
// would see them for a new request.
type CandidateSource func(ctx context.Context, capability string) []routing.Candidate

// maxReplayPurchases bounds the purchases replayed for one task.
const maxReplayPurchases = 10

// Replay runs a task's paid purchases again under chaos, as agentID (the
// agent of the task's Spend Pass).
func (s *ExecutionService) Replay(ctx context.Context, job *JobView, agentID string, source CandidateSource) (*ReplayReport, error) {
	out := &ReplayReport{JobID: job.ID, Label: job.Label, RanAt: s.now().UTC(), Purchases: []ReplayPurchase{}}
	seen := map[string]bool{}
	var walk func(v JobView)
	var purchases []JobAttempt
	walk = func(v JobView) {
		for _, a := range v.Attempts {
			if a.State == econ.ReservationCommitted && !seen[a.IntentID] {
				seen[a.IntentID] = true
				purchases = append(purchases, a)
			}
		}
		for _, c := range v.Children {
			walk(c)
		}
	}
	walk(*job)
	if len(purchases) > maxReplayPurchases {
		purchases = purchases[:maxReplayPurchases]
	}
	for _, a := range purchases {
		view, err := s.econ.View(ctx, a.IntentID, false)
		if err != nil {
			return nil, err
		}
		p, err := s.replayPurchase(ctx, agentID, view, a, source(ctx, view.Capability), out)
		if err != nil {
			return nil, err
		}
		out.Purchases = append(out.Purchases, p)
	}
	return out, nil
}

var replayScenarios = []string{ChaosOvercharge, ChaosHoneypot, ChaosWebLure, ChaosStranger, ChaosSellerDown, ChaosLoop}

// replayPurchase runs every scenario against one purchase and counts what they
// found into out's totals.
func (s *ExecutionService) replayPurchase(ctx context.Context, agentID string, view *IntentView, paid JobAttempt, base []routing.Candidate, out *ReplayReport) (ReplayPurchase, error) {
	p := ReplayPurchase{IntentID: paid.IntentID, Capability: view.Capability, Provider: paid.ProviderID, PaidMinor: paid.HoldMinor}
	for _, sc := range replayScenarios {
		f, err := s.chaos(ctx, agentID, view, paid, base, sc)
		if err != nil {
			return p, err
		}
		if out.Preset && sc == ChaosSellerDown {
			f.Title = "The seller the agent pays is down"
		}
		p.Findings = append(p.Findings, f)
		out.AtRiskMinor += f.AtRiskMinor
		if f.Safe {
			out.Safe++
			out.ProtectedMinor += f.AtRiskMinor
		} else {
			out.Unsafe++
		}
	}
	return p, nil
}

// replayPresets are the kinds of work a Spend Pass is tested on while its agent
// has no task: the cheap lookups agents buy most.
var replayPresets = []string{"token.price", "solana.token-risk", "web.search"}

// ReplayPreset tests a Spend Pass with no task behind it. Each preset kind of
// work is put through the same scenarios as a task's purchase, as if the agent
// had just paid the cheapest seller that lists a price for it, asking with the
// widest budget the pass allows one purchase: a pass that holds against that
// holds against a smaller ask. Nothing is contacted or paid.
func (s *ExecutionService) ReplayPreset(ctx context.Context, p *spendpass.Pass, source CandidateSource) (*ReplayReport, error) {
	switch {
	case p.Currency != "USDC":
		return nil, fmt.Errorf("%w: only a pass that pays for APIs and data (USDC) can be tested, not one that shops", ErrInvalidRequest)
	case !p.Active(s.now()):
		return nil, fmt.Errorf("%w: this pass is revoked or expired", ErrInvalidRequest)
	case p.FrozenAt != nil:
		return nil, fmt.Errorf("%w: this pass is frozen, so it refuses everything: unfreeze it to test it", ErrInvalidRequest)
	}
	networks, err := s.presetNetworks(ctx, p)
	if err != nil {
		return nil, err
	}
	out := &ReplayReport{PassID: p.ID, Label: p.Label, Preset: true, RanAt: s.now().UTC(), Purchases: []ReplayPurchase{}}
	budget := presetBudgetMinor(p)
	for _, id := range replayPresets {
		class, ok := routing.ClassByID(id)
		if !ok {
			continue
		}
		base := payableOn(source(ctx, id), networks)
		ref, ok := cheapestListed(base, p.Currency)
		if !ok {
			out.Skipped = append(out.Skipped, ReplaySkip{
				Capability: id, Title: class.Title,
				Reason: "no seller for it lists a price on this pass's networks (" + strings.Join(networks, ", ") + ")",
			})
			continue
		}
		view := &IntentView{Intent: econ.Intent{
			Capability: id, Input: class.Sample, Currency: p.Currency, BudgetMaxMinor: budget,
			Constraints: econ.Constraints{AllowedNetworks: networks},
		}}
		paid := JobAttempt{Capability: id, ProviderID: ref.Provider, HoldMinor: ref.PriceMinor, Network: ref.Network}
		rp, err := s.replayPurchase(ctx, p.AgentID, view, paid, base, out)
		if err != nil {
			return nil, err
		}
		rp.Title = class.Title
		out.Purchases = append(out.Purchases, rp)
	}
	return out, nil
}

// presetNetworks are the networks the pass would let an intent pay on, found
// the way a real request finds them. Mainnet payments being off is said, not
// tested: every scenario would only be refused for that.
func (s *ExecutionService) presetNetworks(ctx context.Context, p *spendpass.Pass) ([]string, error) {
	var spec econ.Spec
	err := s.econ.restrictDemo(ctx, p.UserID, &spec)
	if err == nil {
		err = s.econ.restrictEnvironment(p, &spec)
	}
	var denied *AuthorityDenied
	switch {
	case errors.As(err, &denied) && slices.Contains(denied.ReasonCodes, DenyMainnetDisabled):
		return nil, fmt.Errorf("%w: Mainnet payments are off, so there is nothing to test yet: test a Testnet pass", ErrInvalidRequest)
	case err != nil:
		return nil, err
	}
	if n := spec.Constraints.AllowedNetworks; len(n) > 0 {
		return n, nil
	}
	// A pass made before projects existed has no environment: test it on Testnet.
	return tenancy.EnvDevnet.Networks(), nil
}

// presetBudgetMinor is the widest budget the pass lets one purchase have: its
// per-purchase limit, else its whole budget.
func presetBudgetMinor(p *spendpass.Pass) int64 {
	switch {
	case p.MaxPerPurchaseMinorUnits != nil && *p.MaxPerPurchaseMinorUnits > 0:
		return *p.MaxPerPurchaseMinorUnits
	case p.BudgetMinorUnits > 0:
		return p.BudgetMinorUnits
	}
	return 1_000_000
}

// payableOn keeps the candidates a pass could actually pay: on one of its
// networks, over a rail that is ready.
func payableOn(base []routing.Candidate, networks []string) []routing.Candidate {
	out := make([]routing.Candidate, 0, len(base))
	for _, c := range base {
		n, err := c.Normalize()
		if err != nil {
			continue
		}
		if slices.Contains(networks, n.Network) && chain.Payable(n.Network) {
			out = append(out, n)
		}
	}
	return out
}

// cheapestListed is the seller with the lowest listed price in currency: the
// one a preset run pretends the agent paid. A tie goes to the first by name, so
// two runs pick the same.
func cheapestListed(base []routing.Candidate, currency string) (routing.Candidate, bool) {
	var best routing.Candidate
	found := false
	for _, c := range base {
		if c.PriceMinor <= 0 || !strings.EqualFold(c.Asset, currency) {
			continue
		}
		if !found || c.PriceMinor < best.PriceMinor || (c.PriceMinor == best.PriceMinor && c.Provider < best.Provider) {
			best, found = c, true
		}
	}
	return best, found
}

var chaosTitles = map[string]string{
	ChaosOvercharge: "A seller lists the usual price, then asks four times as much",
	ChaosHoneypot:   "A seller asks 25 USDC for the same work",
	ChaosWebLure:    "An endpoint found on the open web offers it for half price",
	ChaosStranger:   "A seller the agent has never paid offers the same price",
	ChaosSellerDown: "The seller the task paid is down",
	ChaosLoop:       "The agent asks for the same thing again and again",
}

// chaos runs one scenario for one purchase.
func (s *ExecutionService) chaos(ctx context.Context, agentID string, view *IntentView, paid JobAttempt, base []routing.Candidate, scenario string) (ReplayFinding, error) {
	f := ReplayFinding{Scenario: scenario, Title: chaosTitles[scenario]}
	ref := paid.HoldMinor
	if scenario == ChaosLoop {
		return loopFinding(f, view), nil
	}
	network := paid.Network
	if network == "" && len(view.Constraints.AllowedNetworks) > 0 {
		network = view.Constraints.AllowedNetworks[0]
	}
	// Normalized here so each has the ID the dry run will give it.
	cands := make([]routing.Candidate, 0, len(base)+1)
	for _, c := range base {
		if n, err := c.Normalize(); err == nil {
			c = n
		}
		// One that doesn't normalize goes in as it is: the dry run reports why.
		cands = append(cands, c)
	}
	chaosQuotes := map[string]ChaosQuote{}
	var adversary *routing.Candidate
	add := func(name string, listed, asks int64, sources ...routing.DiscoverySource) error {
		c, err := routing.Candidate{
			Capability: view.Capability, Provider: "chaos:" + name, Name: "Chaos " + name, ExecutionType: routing.ExecX402,
			Endpoint: "https://" + strings.ReplaceAll(name, "_", "-") + ".chaos.invalid/" + view.Capability, Method: "POST",
			Network: network, Asset: "USDC", PriceMinor: listed, Sources: sources,
		}.Normalize()
		if err != nil {
			return err
		}
		if addr, ok := chain.AssetAddress(network, "USDC"); ok {
			c.AssetAddress = addr
		}
		cands = append(cands, c)
		chaosQuotes[c.ID] = ChaosQuote{ProviderMinor: asks}
		adversary = &c
		return nil
	}
	var err error
	switch scenario {
	case ChaosOvercharge:
		f.AtRiskMinor = ref * 4
		err = add(scenario, ref, ref*4, routing.SourceCircle)
	case ChaosHoneypot:
		f.AtRiskMinor = honeypotMinor
		err = add(scenario, honeypotMinor, honeypotMinor, routing.SourceCircle)
	case ChaosWebLure:
		f.AtRiskMinor = max(ref/2, 1)
		err = add(scenario, max(ref/2, 1), max(ref/2, 1), routing.SourceWeb)
	case ChaosStranger:
		f.AtRiskMinor = ref
		err = add(scenario, ref, ref, routing.SourceCircle)
	case ChaosSellerDown:
		for _, c := range cands {
			if c.Provider == paid.ProviderID {
				chaosQuotes[c.ID] = ChaosQuote{Err: "the seller is down (replayed)"}
			}
		}
	}
	if err != nil {
		return f, err
	}
	// Every seller the task had is answered from its listing or by the
	// scenario: none is contacted.
	for _, c := range cands {
		if _, ok := chaosQuotes[c.ID]; !ok && c.ID != "" && c.PriceMinor > 0 {
			chaosQuotes[c.ID] = ChaosQuote{ProviderMinor: c.PriceMinor}
		}
	}
	spec := econ.Spec{
		Capability: view.Capability, Input: view.Input, Window: "replay:" + string(scenario), Currency: view.Currency, BudgetMaxMinor: view.BudgetMaxMinor,
		Constraints: view.Constraints, ProviderPolicy: view.ProviderPolicy,
	}
	sim, err := s.Simulate(ctx, SimulateRequest{AgentID: agentID, Spec: spec, Candidates: cands, Chaos: chaosQuotes})
	if err != nil {
		return f, fmt.Errorf("replaying %s: %w", scenario, err)
	}
	if scenario == ChaosSellerDown {
		if sim.WouldPay != nil && sim.WouldPay.Provider != paid.ProviderID {
			f.Outcome, f.Safe, f.Detail = ReplayFallsBack, true, "it would fall back to "+sim.WouldPay.Provider+" and pay nothing to the seller that is down"
		} else {
			f.Outcome, f.Safe, f.Detail = ReplayStops, true, "no other seller could do it within the limits: nothing would be paid, and the task would say so"
		}
		return f, nil
	}
	return judge(f, sim, adversary), nil
}

// judge reads what the dry run did with the adversary.
func judge(f ReplayFinding, sim *Simulation, adversary *routing.Candidate) ReplayFinding {
	var verdict *CandidateVerdict
	for i := range sim.Candidates {
		if sim.Candidates[i].CandidateID == adversary.ID {
			verdict = &sim.Candidates[i]
		}
	}
	switch {
	case verdict == nil:
		f.Outcome, f.Safe, f.Detail = ReplayCaught, true, rejectionOf(sim, adversary.ID)
	case verdict.Verdict == VerdictDeny:
		f.Outcome, f.Safe, f.Detail = ReplayCaught, true, strings.Join(verdict.Reasons, "; ")
	case verdict.Verdict == VerdictRequireApproval:
		f.Outcome, f.Safe, f.Detail = ReplayHeld, true, "it would wait for your yes ("+strings.Join(verdict.Reasons, ", ")+")"
	case sim.WouldPay != nil && sim.WouldPay.CandidateID == adversary.ID:
		f.Outcome, f.Safe, f.Detail = ReplayWouldPay, false, "nothing stops it: it would be paid"
	default:
		f.Outcome, f.Safe, f.Detail = ReplayOutranked, true, "allowed, but a better offer is ranked first"
	}
	return f
}

func rejectionOf(sim *Simulation, candidateID string) string {
	for _, r := range sim.Rejected {
		if r.CandidateID == candidateID {
			return r.Code + ": " + r.Detail
		}
	}
	return "left out before it was priced"
}

// loopFinding is what repeated asking would meet: purchase memory while the
// answer is fresh, then the repeat-purchase rule.
func loopFinding(f ReplayFinding, view *IntentView) ReplayFinding {
	fresh := freshFor(&view.Intent)
	f.Safe = true
	switch {
	case fresh > 0:
		f.Outcome = ReplayHeld
		f.Detail = fmt.Sprintf("asking again within %s hands back the answer already paid for, for nothing; buying it %d times in %s then waits for your yes",
			spoken(fresh), RepeatLimit, spoken(RepeatWindow))
	default:
		f.Outcome = ReplayHeld
		f.Detail = fmt.Sprintf("this kind of answer isn't reused, so each ask is paid; the %dth identical purchase in %s waits for your yes", RepeatLimit+1, spoken(RepeatWindow))
	}
	return f
}

// spoken is a duration as a person says it: "a minute", "10 minutes", "a day".
func spoken(d time.Duration) string {
	unit := func(n int, one, many string) string {
		if n == 1 {
			return one
		}
		return fmt.Sprintf("%d %s", n, many)
	}
	switch {
	case d >= 24*time.Hour && d%(24*time.Hour) == 0:
		return unit(int(d/(24*time.Hour)), "a day", "days")
	case d >= time.Hour && d%time.Hour == 0:
		return unit(int(d/time.Hour), "an hour", "hours")
	case d >= time.Minute && d%time.Minute == 0:
		return unit(int(d/time.Minute), "a minute", "minutes")
	}
	return unit(int(d/time.Second), "a second", "seconds")
}
