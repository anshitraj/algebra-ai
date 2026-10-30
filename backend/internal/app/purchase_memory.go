package app

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
)

// Purchase memory: an answer already paid for is handed back instead of being
// bought again, while it is fresh, and the same thing bought over and over
// waits for the person.
//
// A reuse is the same person, in the same project, asking for the same work
// with the same input on the same networks, within how long that work stays
// fresh (routing.FreshFor, or the request's own max_age_seconds). The answer
// comes from the result vault, checked against the hash the paid attempt
// recorded, and the asking agent's pass must allow the provider that gave it,
// as it would for a payment. Nothing is paid and no new intent is created. A
// request can always ask for a fresh answer instead.

const (
	// RepeatWindow and RepeatLimit: buying the same work with the same input
	// RepeatLimit times within RepeatWindow is what a loop looks like, so the
	// next purchase waits for the person's yes.
	RepeatWindow = 10 * time.Minute
	RepeatLimit  = 5
	// ReasonRepeatPurchase is the approval reason for that.
	ReasonRepeatPurchase = "REPEAT_PURCHASE"
	// memoryCandidates bounds how many earlier purchases are looked at.
	memoryCandidates = 5
)

// RememberedPurchase is an earlier purchase of the same work and input that
// committed and delivered.
type RememberedPurchase struct {
	IntentID       string
	ReservationID  string
	ProviderID     string
	ProjectID      string
	CommittedMinor int64
	// Networks are the networks the earlier intent was limited to.
	Networks []string
	// At is when its payment committed.
	At time.Time
}

// PurchaseMemory is what a store offers for purchase memory: the principal's
// delivered purchases of capability with the same input since a time, newest
// first.
type PurchaseMemory interface {
	RecentPurchases(ctx context.Context, principalID, capability, inputHash string, since time.Time, limit int) ([]RememberedPurchase, error)
}

// ReusedPurchase tells the caller which earlier purchase answered it.
type ReusedPurchase struct {
	IntentID        string    `json:"intent_id"`
	Provider        string    `json:"provider"`
	PaidMinor       int64     `json:"paid_minor"`
	PaidAt          time.Time `json:"paid_at"`
	AgeSeconds      int       `json:"age_seconds"`
	FreshForSeconds int       `json:"fresh_for_seconds"`
}

// freshFor is how long an earlier answer to in may be reused.
func freshFor(in *econ.Intent) time.Duration {
	if in.Constraints.MaxAgeSeconds > 0 {
		return time.Duration(in.Constraints.MaxAgeSeconds) * time.Second
	}
	return routing.FreshFor(in.Capability)
}

func (s *EconomicService) memory() PurchaseMemory {
	m, _ := s.store.(PurchaseMemory)
	return m
}

// repeated reports whether in's work and input were bought RepeatLimit times
// in the last RepeatWindow. A store without memory never says so.
func (s *EconomicService) repeated(ctx context.Context, in *econ.Intent) bool {
	m := s.memory()
	if m == nil {
		return false
	}
	recent, err := m.RecentPurchases(ctx, in.PrincipalID, in.Capability, in.InputHash, s.now().Add(-RepeatWindow), RepeatLimit)
	return err == nil && len(recent) >= RepeatLimit
}

// recall is the earlier purchases in may reuse, newest first, before their
// answers are checked. A request that named providers (named) is answered only by
// one of them: asking beta for what alpha was paid for is a different purchase,
// and it is the one that makes two answers worth comparing.
func (s *EconomicService) recall(ctx context.Context, in *econ.Intent, named []string) ([]RememberedPurchase, time.Duration) {
	fresh := freshFor(in)
	m := s.memory()
	if m == nil || fresh <= 0 {
		return nil, 0
	}
	recent, err := m.RecentPurchases(ctx, in.PrincipalID, in.Capability, in.InputHash, s.now().Add(-fresh), memoryCandidates)
	if err != nil {
		return nil, 0
	}
	out := recent[:0]
	for _, r := range recent {
		if r.ProjectID != in.ProjectID || !slices.Equal(sortedNetworks(r.Networks), sortedNetworks(in.Constraints.AllowedNetworks)) {
			continue
		}
		if len(in.ProviderPolicy.Providers) > 0 && !slices.Contains(in.ProviderPolicy.Providers, r.ProviderID) {
			continue
		}
		if len(named) > 0 && !slices.ContainsFunc(named, func(n string) bool { return strings.EqualFold(strings.TrimSpace(n), r.ProviderID) }) {
			continue
		}
		out = append(out, r)
	}
	return out, fresh
}

func sortedNetworks(n []string) []string {
	out := slices.Clone(n)
	slices.Sort(out)
	return out
}

// Draft is the intent an agent's request would create, without storing it:
// the same pass, the same demo and environment limits, the same input hash.
func (s *EconomicService) Draft(ctx context.Context, agentID string, spec econ.Spec) (*econ.Intent, *spendpass.Pass, error) {
	p, err := s.executorPass(ctx, agentID)
	if err != nil {
		return nil, nil, err
	}
	if err := s.restrictDemo(ctx, p.UserID, &spec); err != nil {
		return nil, nil, err
	}
	if err := s.restrictEnvironment(p, &spec); err != nil {
		return nil, nil, err
	}
	if spec.Currency == "" {
		spec.Currency = p.Currency
	}
	in, err := econ.New("draft", p.UserID, p.ID, agentID, spec, s.now())
	if err != nil {
		return nil, nil, err
	}
	in.ProjectID = p.ProjectID
	return in, p, nil
}

// reuse answers a request with a fresh answer already paid for, or returns nil
// and the request goes on to be paid for as usual.
func (s *ExecutionService) reuse(ctx context.Context, agentID, jobID string, spec econ.Spec, named []string) *PlanReport {
	if !s.results.Enabled() {
		return nil
	}
	in, pass, err := s.econ.Draft(ctx, agentID, spec)
	if err != nil {
		return nil
	}
	earlier, fresh := s.econ.recall(ctx, in, named)
	for _, e := range earlier {
		view, err := s.econ.View(ctx, e.IntentID, false)
		// The very same request (same window) is the intent itself: Do replays it.
		if err != nil || view.EffectKey == in.EffectKey {
			continue
		}
		rep := s.keptReport(ctx, agentID, view)
		if rep == nil {
			continue
		}
		age := s.now().Sub(e.At)
		rep.Stopped = "reused"
		rep.Reused = &ReusedPurchase{
			IntentID: e.IntentID, Provider: e.ProviderID, PaidMinor: e.CommittedMinor, PaidAt: e.At.UTC(),
			AgeSeconds: int(age.Seconds()), FreshForSeconds: int(fresh.Seconds()),
		}
		// The pass and the task that asked are on the record, with what the
		// reuse saved: the task's and the agent's P&L count it.
		data := map[string]any{"agent_id": agentID, "age_seconds": int(age.Seconds()), "fresh_for_seconds": int(fresh.Seconds()),
			"spend_pass_id": pass.ID, "saved_minor": e.CommittedMinor}
		if jobID != "" {
			data["job_pass_id"] = jobID
		}
		s.event(ctx, e.IntentID, "result.reused", data)
		return rep
	}
	return nil
}

// reusable is what a dry run says about reuse: the earlier purchase whose
// answer would be handed back, or nil.
func (s *ExecutionService) reusable(ctx context.Context, agentID string, in *econ.Intent, named []string) *ReusedPurchase {
	if !s.results.Enabled() {
		return nil
	}
	earlier, fresh := s.econ.recall(ctx, in, named)
	for _, e := range earlier {
		view, err := s.econ.View(ctx, e.IntentID, false)
		if err != nil || s.keptReport(ctx, agentID, view) == nil {
			continue
		}
		age := s.now().Sub(e.At)
		return &ReusedPurchase{
			IntentID: e.IntentID, Provider: e.ProviderID, PaidMinor: e.CommittedMinor, PaidAt: e.At.UTC(),
			AgeSeconds: int(age.Seconds()), FreshForSeconds: int(fresh.Seconds()),
		}
	}
	return nil
}
