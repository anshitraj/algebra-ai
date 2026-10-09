package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
)

// Reservation rejections the pass controls add (see spendpass.Controls).
const (
	// RejectRateLimited: the pass has made as many paid attempts this minute
	// as it may. Nothing else is tried: a looping agent stops here.
	RejectRateLimited = "rate_limited"
	// RejectProviderRateLimited: as many attempts to this provider this minute
	// as the pass allows; another provider may still be tried.
	RejectProviderRateLimited = "provider_rate_limited"
	// RejectNewProvider: the person has never paid this provider and the
	// pass's new-provider rule holds this call back; another provider, one
	// they have paid, may still be tried, or the person may approve.
	RejectNewProvider = "new_provider"
)

// RejectNewProviderGate is the plan-level code for a provider held back by
// the new-provider rule.
const RejectNewProviderGate = "new_provider_gate"

// passControls applies a pass's controls to a reservation request, under the
// pass's lock: how many attempts it made in the last minute, overall and to
// this provider, and whether its person has ever paid this provider. It
// returns the rejection reason and reason codes, or "" to go ahead.
func (s *EconomicService) passControls(u EconUnit, p *spendpass.Pass, in *econ.Intent, provider string, hold int64, now time.Time) (string, []string, error) {
	c := p.Controls.Effective()
	since := now.Add(-time.Minute)
	all, err := u.PassAttemptsSince(p.ID, "", since)
	if err != nil {
		return "", nil, err
	}
	one, err := u.PassAttemptsSince(p.ID, provider, since)
	if err != nil {
		return "", nil, err
	}
	switch c.Velocity(all, one) {
	case spendpass.ReasonRateLimited:
		return RejectRateLimited, []string{spendpass.ReasonRateLimited}, nil
	case spendpass.ReasonProviderRateLimited:
		return RejectProviderRateLimited, []string{spendpass.ReasonProviderRateLimited}, nil
	}
	paid, err := u.ProviderPaid(in.PrincipalID, provider)
	if err != nil {
		return "", nil, err
	}
	if code, _ := c.NewProvider(hold, paid, in.ApprovedAt != nil); code != "" {
		return RejectNewProvider, []string{code}, nil
	}
	return "", nil, nil
}

// Escalate puts an open intent back in front of its person: the router found
// providers that could do it, but every one of them is held back by a rule a
// person's yes would lift (a provider never paid before). After approval the
// intent runs again with that yes on record.
func (s *EconomicService) Escalate(ctx context.Context, intentID string, codes []string) (*IntentView, error) {
	err := s.store.Atomically(ctx, intentID, "", func(u EconUnit) error {
		in := u.Intent()
		if in.State != econ.StateOpen {
			return fmt.Errorf("%w: intent is %s, not open", shared.ErrConflict, in.State)
		}
		now := s.now()
		if err := in.Transition(econ.StateAwaitingApproval, now); err != nil {
			return err
		}
		in.RequiresApproval = true
		if err := u.SaveIntent(in); err != nil {
			return err
		}
		return u.AppendEvent(s.newEvent(EconEvent{IntentID: in.ID, Event: "authority.escalated", Data: map[string]any{"reason_codes": codes}}))
	})
	if err != nil {
		return nil, err
	}
	return s.View(ctx, intentID, false)
}

// escalateIfGated turns "nothing could be tried" into "waiting for you" when
// the only thing between the intent and a provider is the new-provider rule.
// Any other outcome is returned as it was.
func (s *ExecutionService) escalateIfGated(ctx context.Context, view *IntentView, rep *PlanReport, err error) error {
	var nr *NoRoute
	if !errors.As(err, &nr) || rep == nil || len(rep.Attempts) > 0 || view.ApprovedAt != nil {
		return err
	}
	gated := slices.ContainsFunc(rep.Rejected, func(r routing.Rejection) bool { return r.Code == RejectNewProviderGate })
	if !gated {
		return err
	}
	v, eerr := s.econ.Escalate(ctx, view.ID, []string{spendpass.ReasonNewProviderApproval})
	if eerr != nil {
		s.log.Warn("execution: escalating a gated intent failed", "intent_id", view.ID, "err", eerr)
		return err
	}
	rep.Intent = v
	return &ReservationRejected{Reason: RejectApproval, State: v.State, ReasonCodes: []string{spendpass.ReasonNewProviderApproval}}
}

// --- The kill switch and the controls, from the person's side ---

// PassControlStore is what the pass store must also do for the kill switch
// and the controls. The Postgres store does; a store that doesn't makes the
// calls below fail as not implemented.
type PassControlStore interface {
	SetFrozen(ctx context.Context, id string, at *time.Time) error
	SetFrozenAll(ctx context.Context, userID string, at *time.Time) (int, error)
	SetControls(ctx context.Context, id string, c spendpass.Controls) error
}

func (s *SpendPassService) controlStore() (PassControlStore, error) {
	cs, ok := s.store.(PassControlStore)
	if !ok {
		return nil, fmt.Errorf("%w: this pass store has no kill switch", shared.ErrNotImplemented)
	}
	return cs, nil
}

// KillSwitch freezes (on) or thaws every live pass of a person at once and
// says how many changed. While frozen, no reservation is granted and no
// payment authority is released, including for an attempt already running.
func (s *SpendPassService) KillSwitch(ctx context.Context, userID string, on bool) (int, error) {
	cs, err := s.controlStore()
	if err != nil {
		return 0, err
	}
	var at *time.Time
	if on {
		t := s.now().UTC()
		at = &t
	}
	return cs.SetFrozenAll(ctx, userID, at)
}

// KillSwitchState reports how many of a person's live passes are frozen.
func (s *SpendPassService) KillSwitchState(ctx context.Context, userID string) (frozen, live int, err error) {
	passes, err := s.store.ListByUser(ctx, userID)
	if err != nil {
		return 0, 0, err
	}
	now := s.now()
	for _, p := range passes {
		if !p.Active(now) {
			continue
		}
		live++
		if p.FrozenAt != nil {
			frozen++
		}
	}
	return frozen, live, nil
}

// Freeze freezes or thaws one of the person's passes.
func (s *SpendPassService) Freeze(ctx context.Context, userID, passID string, on bool) (*spendpass.Pass, error) {
	cs, err := s.controlStore()
	if err != nil {
		return nil, err
	}
	p, err := s.store.Get(ctx, passID)
	if err != nil {
		return nil, err
	}
	if p.UserID != userID {
		return nil, fmt.Errorf("%w: pass %s", shared.ErrNotFound, passID)
	}
	var at *time.Time
	if on {
		t := s.now().UTC()
		at = &t
	}
	if err := cs.SetFrozen(ctx, passID, at); err != nil {
		return nil, err
	}
	return s.store.Get(ctx, passID)
}

// UpdateControls replaces one of the person's passes' controls.
func (s *SpendPassService) UpdateControls(ctx context.Context, userID, passID string, c spendpass.Controls) (*spendpass.Pass, error) {
	cs, err := s.controlStore()
	if err != nil {
		return nil, err
	}
	p, err := s.store.Get(ctx, passID)
	if err != nil {
		return nil, err
	}
	if p.UserID != userID {
		return nil, fmt.Errorf("%w: pass %s", shared.ErrNotFound, passID)
	}
	if c, err = c.Normalize(p.Currency); err != nil {
		return nil, fmt.Errorf("%w: %s", shared.ErrConflict, err)
	}
	if err := cs.SetControls(ctx, passID, c); err != nil {
		return nil, err
	}
	return s.store.Get(ctx, passID)
}
