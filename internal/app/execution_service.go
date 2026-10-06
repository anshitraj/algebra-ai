package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/policy"
)

// StepRunner is the provider-facing half of one execution type: it prices a
// candidate and makes the call. It never decides economic state. The
// coordinator does that, from what the rail proves, so a runner can be wrong
// or hostile and still can't make Algebra pay twice or claim a result it
// didn't get.
type StepRunner interface {
	// Type is the execution type this runner handles.
	Type() routing.ExecutionType
	// Rail names the payment rail a quote settles through.
	Rail(q routing.Quote) (string, error)
	// Quote prices a candidate for one specific input without paying. The
	// service fills the quote's identity and timestamps; the runner supplies
	// the terms.
	Quote(ctx context.Context, c routing.Candidate, input json.RawMessage) (routing.Quote, error)
	// Run makes the call for a reserved, begun attempt. It asks for payment
	// authority through call.Pay, once, and only after checking the provider's
	// current terms still match the quote.
	Run(ctx context.Context, call StepCall) StepObservation
}

// StepCall is what a runner is given for one attempt.
type StepCall struct {
	Quote routing.Quote
	// Input is the intent's own canonical input: the call must be made with
	// exactly this, since it is what the intent's identity commits to.
	Input       json.RawMessage
	Reservation econ.Reservation
	// Pay asks the coordinator for single-use payment authority bound to this
	// attempt. The requirements are the provider's, verbatim.
	Pay func(ctx context.Context, req PaymentRequest) (*PaymentAuthority, error)
}

// StepObservation is what a runner saw. Bodies stay in memory; only hashes
// are persisted.
type StepObservation struct {
	// AuthorityReleased: Pay succeeded. From then on money may have moved
	// unless the rail proves otherwise.
	AuthorityReleased bool
	// Delivered: the provider answered with a success response after payment
	// (or answered a free call).
	Delivered   bool
	HTTPStatus  int
	ContentType string
	Body        []byte
	RequestHash string
	// SettleTx is the transaction the provider's own settle header named.
	SettleTx            string
	ProviderOperationID string
	// Class and Message say why nothing was delivered.
	Class   routing.FailureClass
	Message string
}

// ExecuteRequest asks for one plan step to be run.
type ExecuteRequest struct {
	AgentID  string
	IntentID string
	Step     routing.PlanStep
	PlanID   string
	PlanHash string
	Mode     routing.Mode
}

// ExecutionReport is one attempt's outcome.
type ExecutionReport struct {
	Result  routing.ExecutionResult `json:"result"`
	Quality *routing.QualityResult  `json:"quality,omitempty"`
	Intent  *IntentView             `json:"intent"`
	// ContentType and Body are the provider's response, handed to the caller
	// once. Algebra stores its hash, never the body.
	ContentType string `json:"-"`
	Body        []byte `json:"-"`
}

// PlanReport is the outcome of running a plan.
type PlanReport struct {
	Plan     *routing.ExecutionPlan `json:"plan"`
	Attempts []ExecutionReport      `json:"attempts"`
	Rejected []routing.Rejection    `json:"rejected,omitempty"`
	Intent   *IntentView            `json:"intent"`
	// Delivered: a result was received and its payment is confirmed (or none
	// was needed).
	Delivered bool `json:"delivered"`
	// Pending: an attempt's outcome is ambiguous. Money may have moved;
	// reconciliation resolves it and no fallback is tried meanwhile.
	Pending bool `json:"pending_reconciliation"`
	// Stopped says why no further step was tried.
	Stopped string `json:"stopped"`
	// Replayed: nothing ran. This is the answer that was kept when the outcome
	// was paid for.
	Replayed bool `json:"replayed,omitempty"`
}

// Final is the last attempt, or nil when none was made.
func (r *PlanReport) Final() *ExecutionReport {
	if len(r.Attempts) == 0 {
		return nil
	}
	return &r.Attempts[len(r.Attempts)-1]
}

// ErrNoRoute: no candidate survived screening and quoting.
var ErrNoRoute = errors.New("app: no candidate can do this intent within its limits")

// NoRoute carries why each candidate was left out.
type NoRoute struct{ Rejected []routing.Rejection }

func (e *NoRoute) Error() string { return ErrNoRoute.Error() }
func (e *NoRoute) Unwrap() error { return ErrNoRoute }

// DefaultQuoteTTL is how long a quote stays payable unless the runner says
// otherwise. Short: a stale price is how an agent overpays.
const DefaultQuoteTTL = 60 * time.Second

// finishTimeout bounds the bookkeeping after a runner returns. It runs on a
// context detached from the caller's, because a client that hangs up after
// money moved must not leave the outcome unrecorded.
const finishTimeout = 30 * time.Second

// ExecutionService runs routed work. It drives a plan step through the
// economic coordinator (reserve, begin, pay, call, verify, complete) and,
// across a plan, falls back to the next step only when the coordinator shows
// that the previous attempt moved no money.
type ExecutionService struct {
	econ    *EconomicService
	store   ExecutionStore
	runners map[routing.ExecutionType]StepRunner
	evals   map[string]Evaluator
	caps    CapabilityCatalog
	log     *slog.Logger
	now     func() time.Time

	quoteTTL time.Duration
	// quoteTimeout bounds one provider's pricing (see quoteAll).
	quoteTimeout time.Duration
	// results keeps the answers to paid calls (see SetResults).
	results *ResultVault
}

// NewExecutionService builds the service with the generic evaluator
// registered.
func NewExecutionService(e *EconomicService, store ExecutionStore) *ExecutionService {
	s := &ExecutionService{
		econ: e, store: store,
		runners: map[routing.ExecutionType]StepRunner{}, evals: map[string]Evaluator{},
		caps: StaticCatalog{}, log: slog.Default(), now: time.Now, quoteTTL: DefaultQuoteTTL, quoteTimeout: quoteTimeout,
	}
	s.RegisterEvaluator(GenericEvaluatorName, GenericEvaluator{})
	// When reconciliation later settles an attempt this service ran, bring
	// its record up to date: a stale UNKNOWN would poison routing history.
	e.SetResolutionHook(s.refresh)
	if store != nil {
		e.attachExecutions(store)
	}
	return s
}

// refresh updates the stored record of an attempt after reconciliation
// resolved it (by the sweeper, or by anyone calling Reconcile).
func (s *ExecutionService) refresh(ctx context.Context, view *IntentView, reservationID string) {
	if s.store == nil {
		return
	}
	rec, err := s.store.ForReservation(ctx, reservationID)
	if err != nil {
		return // not an attempt this service ran
	}
	res := rec.Result
	obs := StepObservation{Delivered: res.Delivery == econ.FulfillmentFulfilled}
	if f := res.Failure; f != nil && f.Class != routing.FailAmbiguous {
		obs.Class, obs.Message = f.Class, f.Message
	}
	res.Failure = nil
	s.settle(&res, view, reservationID, obs)
	if err := res.Validate(); err != nil {
		s.log.Error("execution: inconsistent refreshed result", "result_id", res.ID, "err", err)
	}
	s.save(ctx, view.PrincipalID, res, rec.Quality)
	s.event(ctx, view.ID, "routing.attempt_resolved", map[string]any{
		"provider": res.Provider, "payment": string(res.Payment), "delivery": string(res.Delivery),
	})
}

// RegisterRunner makes an execution type available.
func (s *ExecutionService) RegisterRunner(r StepRunner) { s.runners[r.Type()] = r }

// RegisterEvaluator makes a capability evaluator available by name.
func (s *ExecutionService) RegisterEvaluator(name string, e Evaluator) { s.evals[name] = e }

// SetCapabilities attaches the capability catalog.
func (s *ExecutionService) SetCapabilities(c CapabilityCatalog) { s.caps = c }

// SetLogger replaces the structured logger.
func (s *ExecutionService) SetLogger(l *slog.Logger) { s.log = l }

// SetQuoteTTL overrides how long a quote stays payable.
func (s *ExecutionService) SetQuoteTTL(d time.Duration) {
	if d > 0 {
		s.quoteTTL = d
	}
}

// agentView returns the intent if it belongs to the agent's principal.
func (s *ExecutionService) agentView(ctx context.Context, agentID, intentID string) (*IntentView, error) {
	principal, err := s.econ.PrincipalOf(ctx, agentID)
	if err != nil {
		return nil, err
	}
	return s.econ.ViewFor(ctx, principal, intentID, false)
}

// Quote prices a candidate for an intent's exact input without paying.
func (s *ExecutionService) Quote(ctx context.Context, agentID, intentID string, c routing.Candidate) (routing.Quote, error) {
	view, err := s.agentView(ctx, agentID, intentID)
	if err != nil {
		return routing.Quote{}, err
	}
	c, err = c.Normalize()
	if err != nil {
		return routing.Quote{}, fmt.Errorf("%w: %s", shared.ErrConflict, err)
	}
	return s.quote(ctx, view, c)
}

func (s *ExecutionService) quote(ctx context.Context, view *IntentView, c routing.Candidate) (routing.Quote, error) {
	if c.Capability != view.Capability {
		return routing.Quote{}, fmt.Errorf("%w: the candidate does %q but the intent wants %q", shared.ErrConflict, c.Capability, view.Capability)
	}
	runner, ok := s.runners[c.ExecutionType]
	if !ok {
		return routing.Quote{}, fmt.Errorf("%w: nothing can run %s candidates yet", shared.ErrNotImplemented, c.ExecutionType)
	}
	q, err := runner.Quote(ctx, c, view.Input)
	if err != nil {
		return routing.Quote{}, err
	}
	now := s.now().UTC()
	q.ID, q.CandidateID, q.Capability, q.Provider = newID("quo"), c.ID, c.Capability, c.Provider
	q.ExecutionType, q.Endpoint, q.Method, q.QuotedAt = c.ExecutionType, c.Endpoint, c.Method, now
	if q.ValidUntil.IsZero() {
		q.ValidUntil = now.Add(s.quoteTTL)
	}
	q.Network = chain.NormalizeNetwork(q.Network)
	q.Test = q.Test || chain.IsTestNetwork(q.Network)
	if err := q.Validate(); err != nil {
		return routing.Quote{}, fmt.Errorf("%w: %s quoted something unusable: %s", shared.ErrConflict, c.Provider, err)
	}
	return q, nil
}

// screen checks a quoted candidate against the intent's own limits. It is
// the first, cheap filter; the Spend Pass and the person's guardrails are
// applied again, authoritatively, when the attempt is reserved and begun.
func screen(view *IntentView, c routing.Candidate, q routing.Quote) *routing.Rejection {
	reject := func(code, detail string) *routing.Rejection {
		return &routing.Rejection{CandidateID: c.ID, Provider: c.Provider, Code: code, Detail: detail}
	}
	if p := view.ProviderPolicy.Providers; len(p) > 0 && !slices.Contains(p, c.Provider) {
		return reject(routing.RejectExcluded, "the intent only allows "+strings.Join(p, ", "))
	}
	cons := view.Constraints
	if n := cons.AllowedNetworks; len(n) > 0 && !slices.Contains(n, chain.NormalizeNetwork(q.Network)) {
		return reject(routing.RejectNetwork, "pays on "+q.Network)
	}
	if q.Asset != "" && !strings.EqualFold(q.Asset, view.Currency) {
		return reject(routing.RejectAsset, "asks for "+q.Asset+", the intent pays in "+view.Currency)
	}
	if a := cons.AllowedAssets; len(a) > 0 && q.Asset != "" && !slices.Contains(a, q.Asset) {
		return reject(routing.RejectAsset, "asks for "+q.Asset)
	}
	if q.Cost.Total() > view.BudgetMaxMinor {
		return reject(routing.RejectOverBudget, fmt.Sprintf("costs %d, the intent's ceiling is %d", q.Cost.Total(), view.BudgetMaxMinor))
	}
	if cons.MaxLatencyMS > 0 && q.EstimatedLatencyMS > cons.MaxLatencyMS {
		return reject(routing.RejectLatency, fmt.Sprintf("expected %d ms, limit %d ms", q.EstimatedLatencyMS, cons.MaxLatencyMS))
	}
	if cons.MaxSlippageBps > 0 && q.SlippageBps > cons.MaxSlippageBps {
		return reject(routing.RejectSlippage, fmt.Sprintf("slippage %d bps, limit %d", q.SlippageBps, cons.MaxSlippageBps))
	}
	if cons.MaxPriceImpactBps > 0 && q.PriceImpactBps > cons.MaxPriceImpactBps {
		return reject(routing.RejectSlippage, fmt.Sprintf("price impact %d bps, limit %d", q.PriceImpactBps, cons.MaxPriceImpactBps))
	}
	// Reliability and quality come from Algebra's own record, so they apply
	// only to a provider it has enough history on.
	if h := c.History; h != nil && h.Calls >= routing.ObservedMinCalls {
		if cons.MinReliabilityPct > 0 && h.SuccessRate*100 < float64(cons.MinReliabilityPct) {
			return reject(routing.RejectReliability, fmt.Sprintf("%.0f%% success over %d calls", h.SuccessRate*100, h.Calls))
		}
		if cons.MinQuality > 0 && h.AvgQuality < float64(cons.MinQuality) {
			return reject(routing.RejectQuality, fmt.Sprintf("average quality %.0f over %d calls", h.AvgQuality, h.Calls))
		}
	}
	return nil
}

// CandidatesRequest asks for an intent to be done by the given candidates. The
// router quotes them, sets aside what the intent's limits and the person's
// Spend Pass rule out, ranks the rest for the intent's strategy (cheapest,
// fastest or auto) and runs the best, falling back down the ranking only when
// the coordinator shows an attempt moved no money.
type CandidatesRequest struct {
	AgentID    string
	IntentID   string
	Candidates []routing.Candidate
	// DiscardResult: the caller asked for the answer not to be kept.
	DiscardResult bool
}

const (
	// MaxQuotedCandidates bounds how many candidates are priced for one
	// request. Each is an unpaid request to a provider, so the router looks at
	// enough to have a real choice and no more.
	MaxQuotedCandidates = 12
	// quoteConcurrency is how many providers are asked for a price at once.
	quoteConcurrency = 4
	// quoteTimeout is how long one provider has to price a request. A slow
	// provider is skipped, not waited for: the others are priced meanwhile.
	quoteTimeout = 20 * time.Second
)

// RejectOutranked: a candidate that could have done the work but ranked below
// the plan's last step.
const RejectOutranked = "outranked"

// ExecuteCandidates quotes each candidate, screens it against the intent's
// limits, ranks the ones that pass, builds a plan from the best and runs it.
func (s *ExecutionService) ExecuteCandidates(ctx context.Context, req CandidatesRequest) (*PlanReport, error) {
	if req.DiscardResult {
		ctx = WithoutKeepingResult(ctx)
	}
	view, err := s.agentView(ctx, req.AgentID, req.IntentID)
	if err != nil {
		return nil, err
	}
	// No provider is contacted, not even to ask a price, for something the
	// person hasn't approved or that can't be attempted at all: the intent's
	// input would otherwise leave Algebra before anyone said yes.
	switch view.State {
	case econ.StateAwaitingApproval:
		return nil, &ReservationRejected{Reason: RejectApproval, State: view.State}
	case econ.StateCancelled, econ.StateExpired:
		return nil, &ReservationRejected{Reason: RejectClosed, State: view.State}
	}
	pass, err := s.econ.executorPass(ctx, req.AgentID)
	if err != nil {
		return nil, err
	}
	mode, err := routing.ParseMode(view.ProviderPolicy.Strategy)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", shared.ErrConflict, err)
	}

	// 1. Who could do it: normalised, de-duplicated, and allowed by the pass.
	// None of this asks a provider anything.
	var cands []routing.Candidate
	var rejected []routing.Rejection
	seen := map[string]bool{}
	for _, raw := range req.Candidates {
		c, err := raw.Normalize()
		if err != nil {
			rejected = append(rejected, routing.Rejection{Provider: raw.Provider, Code: routing.RejectUnquotable, Detail: err.Error()})
			continue
		}
		if seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		if len(cands) >= MaxQuotedCandidates {
			rejected = append(rejected, routing.Rejection{CandidateID: c.ID, Provider: c.Provider, Code: RejectOutranked, Detail: fmt.Sprintf("only %d candidates are priced per request", MaxQuotedCandidates)})
			continue
		}
		// A provider the Spend Pass doesn't allow is not even asked for a
		// price. The pass is applied again, with the amount, when the attempt
		// is reserved.
		if d := evaluate(pass, c.Provider, 0, 0, view.Currency, s.now()); d.Decision == policy.Deny {
			rejected = append(rejected, routing.Rejection{CandidateID: c.ID, Provider: c.Provider, Code: routing.RejectPolicy, Detail: strings.Join(d.ReasonCodes, ", ")})
			continue
		}
		cands = append(cands, c)
	}

	// 2. What Algebra already knows about them, then what each asks right now.
	s.attachHistory(ctx, cands)
	quotes := s.quoteAll(ctx, view, cands)

	// 3. Which of them the intent's own limits allow.
	var opts []routing.Option
	for i, c := range cands {
		if quotes[i].err != nil {
			rejected = append(rejected, routing.Rejection{CandidateID: c.ID, Provider: c.Provider, Code: routing.RejectUnquotable, Detail: briefly(quotes[i].err.Error())})
			continue
		}
		if r := screen(view, c, quotes[i].q); r != nil {
			rejected = append(rejected, *r)
			continue
		}
		opts = append(opts, routing.Option{Candidate: c, Quote: quotes[i].q})
	}
	if len(opts) == 0 {
		return nil, &NoRoute{Rejected: rejected}
	}

	// 4. Rank them for the intent's strategy; the best few become the plan.
	steps := routing.Rank(mode, opts)
	if len(steps) > routing.MaxPlanSteps {
		for _, st := range steps[routing.MaxPlanSteps:] {
			rejected = append(rejected, routing.Rejection{CandidateID: st.Quote.CandidateID, Provider: st.Quote.Provider, Code: RejectOutranked,
				Detail: fmt.Sprintf("ranked %d; a plan holds %d", st.Rank, routing.MaxPlanSteps)})
		}
		steps = steps[:routing.MaxPlanSteps]
	}
	plan, err := routing.NewPlan(newID("plan"), routing.PlanSpec{
		IntentID: view.ID, IntentHash: view.IntentHash, Capability: view.Capability, Mode: mode, Steps: steps, Rejected: rejected,
	}, s.now())
	if err != nil {
		return nil, fmt.Errorf("%w: %s", shared.ErrConflict, err)
	}
	rep, err := s.RunPlan(ctx, req.AgentID, req.IntentID, plan)
	if rep != nil {
		rep.Rejected = append(slices.Clone(rejected), rep.Rejected...)
	}
	return rep, err
}

// attachHistory gives each candidate Algebra's own record of it: how often it
// has delivered, how fast, how good. A store that can't be read leaves the
// candidates as they are, ranked on what they advertise; the record is an
// input to ranking, never a precondition for paying.
func (s *ExecutionService) attachHistory(ctx context.Context, cands []routing.Candidate) {
	if s.store == nil || len(cands) == 0 {
		return
	}
	ids := make([]string, len(cands))
	for i, c := range cands {
		ids[i] = c.ID
	}
	recent, err := s.store.Recent(ctx, ids, s.now().Add(-HistoryWindow), HistoryDepth)
	if err != nil {
		s.log.Warn("execution: reading provider history failed; ranking without it", "err", err)
		return
	}
	for i := range cands {
		recs := recent[cands[i].ID]
		if len(recs) == 0 {
			continue
		}
		h := SummarizeHistory(recs)
		if h.Calls == 0 || h.Validate() != nil {
			continue
		}
		// The candidate's own record (a source that brought one) stands if it
		// is longer than ours.
		if cands[i].History == nil || h.Calls > cands[i].History.Calls {
			cands[i].History = &h
		}
	}
}

type pricing struct {
	q   routing.Quote
	err error
}

// quoteAll prices every candidate for the intent's input, a few at a time, each
// with its own deadline. The answers keep the candidates' order, whichever
// provider answered first. A runner that panics fails its own quote and
// nothing else.
func (s *ExecutionService) quoteAll(ctx context.Context, view *IntentView, cands []routing.Candidate) []pricing {
	out := make([]pricing, len(cands))
	sem := make(chan struct{}, quoteConcurrency)
	var wg sync.WaitGroup
	for i, c := range cands {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if p := recover(); p != nil {
					s.log.Error("execution: runner panicked while pricing", "provider", c.Provider, "panic", fmt.Sprint(p))
					out[i] = pricing{err: errors.New("the runner crashed while pricing")}
				}
			}()
			qctx, cancel := context.WithTimeout(ctx, s.quoteTimeout)
			defer cancel()
			q, err := s.quote(qctx, view, c)
			out[i] = pricing{q: q, err: err}
		}()
	}
	wg.Wait()
	return out
}

// DoRequest is everything an agent says in one go: the outcome it wants, what
// it will pay for it, and who may do it.
type DoRequest struct {
	AgentID    string
	Spec       econ.Spec
	Candidates []routing.Candidate
	// DiscardResult: the caller asked for the answer not to be kept.
	DiscardResult bool
}

// DoResult is what Do returns.
type DoResult struct {
	// Created is false when the same outcome had already been asked for: the
	// existing intent is used, so asking twice never buys twice.
	Created bool        `json:"created"`
	Report  *PlanReport `json:"report"`
	// Intent is the intent the request created or found, even when running it
	// was refused (say it awaits the person's approval).
	Intent *IntentView `json:"intent,omitempty"`
	// Replayed: the outcome was already paid for, nothing ran, and the report
	// carries the answer that was kept.
	Replayed bool `json:"replayed,omitempty"`
}

// Do creates the intent for an outcome (or finds the one that already exists
// for it) and runs it. It is the one call an agent needs.
func (s *ExecutionService) Do(ctx context.Context, req DoRequest) (*DoResult, error) {
	view, created, err := s.econ.CreateIntent(ctx, req.AgentID, req.Spec)
	if err != nil {
		return nil, err
	}
	// Asking again for what was already paid for returns the answer that was
	// kept, when it was, and pays nothing.
	if !created && view.State == econ.StateCommitted {
		if rep := s.replay(ctx, req.AgentID, view); rep != nil {
			return &DoResult{Created: false, Report: rep, Intent: view, Replayed: true}, nil
		}
	}
	rep, err := s.ExecuteCandidates(ctx, CandidatesRequest{AgentID: req.AgentID, IntentID: view.ID, Candidates: req.Candidates, DiscardResult: req.DiscardResult})
	return &DoResult{Created: created, Report: rep, Intent: view}, err
}

// RunPlan runs a plan's steps in order. It stops at the first attempt that
// leaves the intent committed, and at the first whose outcome is ambiguous.
// It moves on to the next step only when the coordinator shows the intent
// open again: that is, when the attempt is proven to have moved no money.
func (s *ExecutionService) RunPlan(ctx context.Context, agentID, intentID string, plan *routing.ExecutionPlan) (*PlanReport, error) {
	rep := &PlanReport{Plan: plan}
	providers := make([]string, 0, len(plan.Steps))
	for _, st := range plan.Steps {
		providers = append(providers, st.Quote.Provider)
	}
	s.event(ctx, intentID, "routing.plan_selected", map[string]any{
		"plan_id": plan.ID, "plan_hash": plan.Hash, "mode": string(plan.Mode), "providers": providers, "rejected": len(plan.Rejected),
		"ranking": rankingEvent(plan),
	})
	var attempted []string
loop:
	for {
		step, ok := plan.Next(attempted, s.now())
		if !ok {
			rep.Stopped = "no_more_candidates"
			break
		}
		attempted = append(attempted, step.Quote.CandidateID)
		ar, err := s.Execute(ctx, ExecuteRequest{AgentID: agentID, IntentID: intentID, Step: step, PlanID: plan.ID, PlanHash: plan.Hash, Mode: plan.Mode})
		if err != nil {
			if code, ok := skippable(err); ok {
				// Authority refused this provider before anything happened;
				// another may be allowed.
				rep.Rejected = append(rep.Rejected, routing.Rejection{CandidateID: step.Quote.CandidateID, Provider: step.Quote.Provider, Code: code, Detail: briefly(err.Error())})
				continue
			}
			if ar != nil {
				// The attempt ran and money may have moved, but recording the
				// outcome failed. Keep what was received: the caller paid for it.
				rep.Attempts = append(rep.Attempts, *ar)
			}
			rep.Intent, _ = s.econ.View(ctx, intentID, false)
			rep.Stopped = "error"
			return rep, err
		}
		rep.Attempts = append(rep.Attempts, *ar)
		rep.Intent = ar.Intent
		switch ar.Intent.State {
		case econ.StateCommitted:
			rep.Stopped = "committed"
			break loop
		case econ.StateOpen:
			if _, more := plan.Next(attempted, s.now()); more {
				fail := ""
				if ar.Result.Failure != nil {
					fail = string(ar.Result.Failure.Class)
				}
				s.event(ctx, intentID, "routing.fallback", map[string]any{"from_provider": step.Quote.Provider, "after": fail, "plan_id": plan.ID})
			}
		default:
			rep.Pending, rep.Stopped = true, "outcome_unknown"
			break loop
		}
	}
	if rep.Intent == nil {
		rep.Intent, _ = s.econ.View(ctx, intentID, false)
	}
	if f := rep.Final(); f != nil {
		rep.Delivered = f.Result.Succeeded()
	}
	if rep.Stopped == "no_more_candidates" && len(rep.Attempts) == 0 {
		return rep, &NoRoute{Rejected: rep.Rejected}
	}
	return rep, nil
}

// skippable reports whether an Execute error is one where nothing happened
// and a different provider might still be allowed.
func skippable(err error) (string, bool) {
	var rej *ReservationRejected
	if errors.As(err, &rej) {
		switch rej.Reason {
		case RejectAuthorityDenied:
			return routing.RejectPolicy, true
		case RejectProviderExcluded:
			return routing.RejectExcluded, true
		case RejectQuoteOverBudget:
			return routing.RejectOverBudget, true
		}
		return "", false
	}
	var denied *AuthorityDenied
	if errors.As(err, &denied) {
		return routing.RejectPolicy, true
	}
	return "", false
}

// Execute runs one plan step: reserve the intent for this provider, begin
// (re-checking authority), have the runner make the call, verify and score the
// result, then report what was observed to the coordinator, which asks the
// rail what is proven and moves the intent only that far.
func (s *ExecutionService) Execute(ctx context.Context, req ExecuteRequest) (*ExecutionReport, error) {
	q := req.Step.Quote
	runner, ok := s.runners[q.ExecutionType]
	if !ok {
		return nil, fmt.Errorf("%w: nothing can run %s candidates yet", shared.ErrNotImplemented, q.ExecutionType)
	}
	rail, err := runner.Rail(q)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", shared.ErrConflict, err)
	}
	if q.Expired(s.now()) {
		return nil, fmt.Errorf("%w: the quote from %s has expired; ask for a fresh one", shared.ErrConflict, q.Provider)
	}
	view, err := s.agentView(ctx, req.AgentID, req.IntentID)
	if err != nil {
		return nil, err
	}
	if view.Capability != q.Capability {
		return nil, fmt.Errorf("%w: the quote is for %q but the intent wants %q", shared.ErrConflict, q.Capability, view.Capability)
	}

	rsv, err := s.econ.Reserve(ctx, req.AgentID, req.IntentID, ReserveRequest{
		ProviderID: q.Provider, Rail: rail, QuoteMinor: q.Cost.Total(), Semantics: q.Semantics,
	})
	if err != nil {
		return nil, err
	}
	begun, err := s.econ.Begin(ctx, req.AgentID, req.IntentID, rsv.ID)
	if err != nil {
		return nil, err
	}

	// Money may move from here on. Everything below runs on a context that
	// survives the caller hanging up.
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), finishTimeout)
	defer cancel()

	res := routing.ExecutionResult{
		ID: newID("xres"), IntentID: req.IntentID, ReservationID: rsv.ID, Attempt: rsv.Attempt, PlanID: req.PlanID, PlanRank: req.Step.Rank,
		Mode: req.Mode, PlanHash: req.PlanHash, QuoteHash: q.Hash(),
		CandidateID: q.CandidateID, QuoteID: q.ID, Provider: q.Provider, Capability: q.Capability, ExecutionType: q.ExecutionType,
		QuotedCostMinor: q.Cost.Total(), StartedAt: s.now().UTC(),
		Payment: routing.PaymentNotAttempted, Delivery: econ.FulfillmentNone, Network: q.Network, Asset: q.Asset, Test: q.Test,
	}
	obs := s.run(ctx, runner, StepCall{
		Quote: q, Input: view.Input, Reservation: *begun,
		Pay: func(ctx context.Context, pr PaymentRequest) (*PaymentAuthority, error) {
			return s.econ.AuthorizePayment(ctx, req.AgentID, req.IntentID, rsv.ID, pr)
		},
	})
	res.HTTPStatus, res.RequestHash = obs.HTTPStatus, obs.RequestHash
	if len(obs.Body) > 0 {
		res.ResponseHash = econ.HashBytes(obs.Body)
	}
	// Kept before the outcome is reported: if recording it fails and
	// reconciliation commits the attempt later, the answer is already there.
	s.keepResult(ctx, bg, view.PrincipalID, req.IntentID, rsv.ID, obs)

	// Judge the result before reporting it: the receipt is signed the moment
	// the intent commits, and it carries the verdict.
	capab := s.capability(view.Capability)
	quality := s.evaluate(bg, capab, obs)
	valid := quality != nil && (quality.SchemaValid == nil || *quality.SchemaValid)
	res.Payment, res.Delivery = routing.PaymentNotAttempted, econ.FulfillmentNone
	if obs.AuthorityReleased {
		res.Payment = routing.PaymentAuthorized
	}
	if obs.Delivered {
		res.Delivery = econ.FulfillmentFulfilled
		if !valid {
			res.Delivery = econ.FulfillmentNotFulfilled
		}
	}
	// Written before Complete, which may sign the receipt.
	s.save(bg, view.PrincipalID, res, quality)

	view2, err := s.econ.Complete(bg, req.AgentID, req.IntentID, rsv.ID, s.completion(obs, res, valid))
	if err != nil {
		// The attempt stays EXECUTING; the sweeper turns it UNKNOWN and
		// reconciles it against the rail. Hand back what was received.
		s.log.Error("execution: recording the outcome failed", "intent_id", req.IntentID, "reservation_id", rsv.ID, "err", err)
		res.Payment, res.Delivery = routing.PaymentUnknown, econ.FulfillmentUnknown
		if obs.Delivered {
			res.Delivery = econ.FulfillmentFulfilled
		}
		res.Failure = &routing.Failure{Class: routing.FailAmbiguous, Message: "the outcome could not be recorded; it will be reconciled"}
		res.Finish(s.now())
		return &ExecutionReport{Result: res, Quality: quality, ContentType: obs.ContentType, Body: obs.Body}, fmt.Errorf("recording the outcome: %w", err)
	}
	if view2.State == econ.StateUnknown {
		// One immediate attempt to resolve it: often the rail can already say.
		if _, err := s.econ.Reconcile(bg, req.IntentID); err != nil {
			s.log.Warn("execution: inline reconcile failed", "intent_id", req.IntentID, "err", err)
		}
		if v, err := s.econ.View(bg, req.IntentID, false); err == nil {
			view2 = v
		}
	}
	s.settle(&res, view2, rsv.ID, obs)
	res.Finish(s.now())
	if err := res.Validate(); err != nil {
		s.log.Error("execution: inconsistent result", "intent_id", req.IntentID, "err", err)
	}
	s.save(bg, view.PrincipalID, res, quality)
	s.event(bg, req.IntentID, "routing.attempt_result", map[string]any{
		"provider": res.Provider, "plan_rank": res.PlanRank, "payment": string(res.Payment), "delivery": string(res.Delivery),
		"latency_ms": res.LatencyMS, "quality": qualityOf(quality),
	})
	return &ExecutionReport{Result: res, Quality: quality, Intent: view2, ContentType: obs.ContentType, Body: obs.Body}, nil
}

// run calls the runner, turning a panic into an ambiguous observation: after
// a crash nobody knows whether authority was released, and the coordinator,
// which does know, decides what the attempt becomes.
func (s *ExecutionService) run(ctx context.Context, r StepRunner, call StepCall) (obs StepObservation) {
	defer func() {
		if p := recover(); p != nil {
			s.log.Error("execution: runner panicked", "provider", call.Quote.Provider, "panic", fmt.Sprint(p))
			obs = StepObservation{Class: routing.FailAmbiguous, Message: "the runner crashed"}
		}
	}()
	return r.Run(ctx, call)
}

func (s *ExecutionService) capability(id string) routing.Capability {
	if c, ok := s.caps.Capability(id); ok {
		return c
	}
	return routing.Capability{ID: id, Kind: routing.KindData}
}

// evaluate scores a response with the capability's evaluator. An evaluator
// that fails leaves the result unjudged, flagged, rather than failing the
// attempt: the money question is settled elsewhere.
func (s *ExecutionService) evaluate(ctx context.Context, c routing.Capability, obs StepObservation) *routing.QualityResult {
	name := c.Evaluator
	if name == "" {
		name = GenericEvaluatorName
	}
	ev, ok := s.evals[name]
	if !ok {
		ev = s.evals[GenericEvaluatorName]
	}
	q, err := ev.Evaluate(ctx, c, EvalInput{Response: obs.Body, ContentType: obs.ContentType, Delivered: obs.Delivered})
	if err != nil {
		s.log.Warn("execution: evaluator failed", "capability", c.ID, "evaluator", ev.Name(), "err", err)
		q = routing.QualityResult{Evaluator: ev.Name(), Flags: []string{"evaluator_error"}}
		if !obs.Delivered {
			_ = q.Finalize(false)
		}
	}
	q.EvaluatedAt = s.now().UTC()
	return &q
}

// completion turns what a runner saw into the report the coordinator takes.
// Success is only ever a claim here: the coordinator checks payment against
// the rail before committing anything.
func (s *ExecutionService) completion(obs StepObservation, res routing.ExecutionResult, valid bool) CompletionReport {
	ev := econ.Evidence{Transaction: obs.SettleTx, RequestHash: obs.RequestHash, ResultHash: res.ResponseHash, ProviderOperationID: obs.ProviderOperationID}
	switch {
	case obs.Delivered && obs.AuthorityReleased && valid:
		return CompletionReport{Outcome: econ.OutcomeFulfilled, Evidence: ev}
	case obs.Delivered && obs.AuthorityReleased:
		return CompletionReport{Outcome: econ.OutcomeSettled, Fulfillment: econ.FulfillmentNotFulfilled, Evidence: ev, Detail: "the response failed validation"}
	default:
		// Nothing was paid for or the call failed. This is a claim of "no
		// commitment": the coordinator accepts it outright only when payment
		// authority was never released, and otherwise asks the rail.
		return CompletionReport{Outcome: econ.OutcomeNoCommitment, Evidence: ev, Detail: obs.Message}
	}
}

// settle fills in the final payment and delivery statuses from what the
// coordinator now holds, never from the runner's word.
func (s *ExecutionService) settle(res *routing.ExecutionResult, view *IntentView, rsvID string, obs StepObservation) {
	var rv *econ.Reservation
	for i := range view.Reservations {
		if view.Reservations[i].ID == rsvID {
			rv = &view.Reservations[i]
		}
	}
	failure := func(class routing.FailureClass, msg string) *routing.Failure {
		if obs.Class != "" {
			class = obs.Class
		}
		if obs.Message != "" {
			msg = obs.Message
		}
		return &routing.Failure{Class: class, Message: msg}
	}
	switch view.State {
	case econ.StateCommitted:
		res.Payment, res.ActualCostMinor, res.Delivery = routing.PaymentSettled, view.CommittedMinor, view.Fulfillment
		if rv != nil {
			res.Transaction, res.Network, res.Asset = rv.Evidence.Transaction, rv.Evidence.Network, rv.Evidence.Asset
			res.Test = res.Test || rv.Evidence.Test
		}
		switch view.Fulfillment {
		case econ.FulfillmentNotFulfilled:
			res.Failure = &routing.Failure{Class: routing.FailInvalid, Message: "payment confirmed; the result was not usable"}
		case econ.FulfillmentFulfilled:
		default:
			res.Failure = failure(routing.FailProvider, "payment confirmed; no usable result was received")
		}
	case econ.StateOpen:
		// Released: no money moved.
		res.ActualCostMinor = 0
		res.Payment = routing.PaymentNotAttempted
		if rv != nil && rv.Evidence.AuthorityIssued {
			res.Payment = routing.PaymentNotSettled
		}
		if obs.Delivered {
			res.Delivery = econ.FulfillmentFulfilled
		} else {
			res.Delivery = econ.FulfillmentNotFulfilled
			res.Failure = failure(routing.FailProvider, "the provider did not deliver; no money moved")
		}
	default:
		// UNKNOWN, RECONCILING: money may have moved.
		res.Payment, res.Delivery = routing.PaymentUnknown, econ.FulfillmentUnknown
		if obs.Delivered {
			res.Delivery = econ.FulfillmentFulfilled
		} else {
			res.Failure = failure(routing.FailAmbiguous, "money may have moved; the outcome is being reconciled")
		}
	}
}

func (s *ExecutionService) save(ctx context.Context, principalID string, res routing.ExecutionResult, q *routing.QualityResult) {
	if s.store == nil {
		return
	}
	if err := s.store.SaveResult(ctx, principalID, StoredExecution{Result: res, Quality: q}); err != nil {
		// Telemetry never blocks money bookkeeping.
		s.log.Error("execution: saving the result failed", "result_id", res.ID, "err", err)
	}
}

// Executions lists an intent's attempts with their quality verdicts.
func (s *ExecutionService) Executions(ctx context.Context, principalID, intentID string) ([]StoredExecution, error) {
	if _, err := s.econ.ViewFor(ctx, principalID, intentID, false); err != nil {
		return nil, err
	}
	if s.store == nil {
		return nil, nil
	}
	return s.store.ForIntent(ctx, intentID)
}

func (s *ExecutionService) event(ctx context.Context, intentID, name string, data map[string]any) {
	s.econ.event(ctx, EconEvent{IntentID: intentID, Event: name, Data: data})
}

func qualityOf(q *routing.QualityResult) any {
	if q == nil || q.FinalQuality == nil {
		return nil
	}
	return *q.FinalQuality
}

// briefly keeps an error message short enough for a telemetry field.
func briefly(s string) string { return trunc(s, 200) }
