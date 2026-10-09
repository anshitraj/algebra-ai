package app

import (
	"context"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/policy"
)

// Paid-for answers are kept, sealed and for a limited time (see ResultVault),
// so that asking again for an outcome already paid for returns the answer
// instead of "already committed" and nothing to show for it. A repeat request
// never pays: the intent is committed, and the answer comes from the vault.

// SetResults attaches the vault that keeps answers. Without one nothing is kept
// and a repeat request is refused as it always was.
func (s *ExecutionService) SetResults(v *ResultVault) { s.results = v }

// PurgeResults removes the answers whose time is up.
func (s *ExecutionService) PurgeResults(ctx context.Context) (int, error) {
	return s.results.Purge(ctx)
}

// discardKey carries a request's decision not to have its answer kept down to
// the attempt that receives it. It is a per-request option, not a dependency.
type discardKey struct{}

// WithoutKeepingResult returns a context under which the answers to attempts
// are not kept.
func WithoutKeepingResult(ctx context.Context) context.Context {
	return context.WithValue(ctx, discardKey{}, true)
}

func resultDiscarded(ctx context.Context) bool {
	v, _ := ctx.Value(discardKey{}).(bool)
	return v
}

// keepResult seals the answer of an attempt that delivered one. It runs before
// the attempt is reported to the coordinator: if recording the outcome fails and
// reconciliation later commits the attempt, the answer is already kept. A
// failure here is logged and never stops the call: the caller has the answer.
func (s *ExecutionService) keepResult(ctx, bg context.Context, principalID, intentID, reservationID string, obs StepObservation) {
	if !s.results.Enabled() || !obs.Delivered || len(obs.Body) == 0 || resultDiscarded(ctx) {
		return
	}
	k, err := s.results.Keep(bg, principalID, intentID, reservationID, obs.ContentType, obs.HTTPStatus, obs.Body)
	switch {
	case err != nil:
		s.log.Error("execution: keeping the result failed", "intent_id", intentID, "err", err)
	case k.Stored:
		s.event(bg, intentID, "result.stored", map[string]any{"size_bytes": k.Size, "expires_at": k.ExpiresAt})
	case k.Reason == "too_large":
		s.event(bg, intentID, "result.not_stored", map[string]any{"reason": k.Reason, "size_bytes": k.Size})
	}
}

// replay answers a repeat request for an outcome that has already committed
// with the answer that was kept, or returns nil when it can't: nothing kept, the
// answer expired, it isn't the committed attempt's, it doesn't match the hash
// the attempt recorded, or the agent asking isn't allowed to deal with that
// provider. In every nil case the request goes on as before and is refused as
// already committed.
func (s *ExecutionService) replay(ctx context.Context, agentID string, view *IntentView) *PlanReport {
	if !s.results.Enabled() || view.State != econ.StateCommitted {
		return nil
	}
	var rsv *econ.Reservation
	for i := range view.Reservations {
		if view.Reservations[i].State == econ.ReservationCommitted {
			rsv = &view.Reservations[i]
		}
	}
	if rsv == nil {
		return nil
	}
	// The pass of the agent asking decides, as it would for a payment: an agent
	// whose pass doesn't allow this provider doesn't get its answer either.
	pass, err := s.econ.executorPass(ctx, agentID)
	if err != nil {
		return nil
	}
	if d := evaluate(pass, rsv.ProviderID, 0, 0, view.Currency, s.now()); d.Decision == policy.Deny {
		return nil
	}
	kept, err := s.results.Get(ctx, view.PrincipalID, view.ID)
	if err != nil {
		return nil
	}
	if kept.ReservationID != rsv.ID || (rsv.Evidence.ResultHash != "" && rsv.Evidence.ResultHash != kept.SHA256) {
		s.log.Warn("execution: a kept result isn't the committed attempt's; not replaying it", "intent_id", view.ID)
		return nil
	}
	var res routing.ExecutionResult
	var quality *routing.QualityResult
	if s.store != nil {
		if rec, err := s.store.ForReservation(ctx, rsv.ID); err == nil {
			res, quality = rec.Result, rec.Quality
		}
	}
	if res.ID == "" {
		// No attempt record (it is telemetry and can be missing): what the
		// coordinator holds is enough to say what was paid.
		res = routing.ExecutionResult{
			IntentID: view.ID, ReservationID: rsv.ID, Attempt: rsv.Attempt, Provider: rsv.ProviderID, Capability: view.Capability,
			ExecutionType: routing.ExecX402, ActualCostMinor: view.CommittedMinor, Payment: routing.PaymentSettled, Delivery: view.Fulfillment,
			Network: rsv.Evidence.Network, Asset: rsv.Evidence.Asset, Transaction: rsv.Evidence.Transaction, Test: rsv.Evidence.Test,
			ResponseHash: kept.SHA256, HTTPStatus: kept.HTTPStatus,
		}
	}
	s.event(ctx, view.ID, "result.replayed", map[string]any{"agent_id": agentID, "size_bytes": len(kept.Body)})
	return &PlanReport{
		Intent: view, Delivered: res.Succeeded(), Stopped: "replayed", Replayed: true,
		Attempts: []ExecutionReport{{Result: res, Quality: quality, Intent: view, ContentType: kept.ContentType, Body: kept.Body}},
	}
}

// ResultView is a kept answer with the facts needed to read it.
type ResultView struct {
	IntentID    string
	ContentType string
	HTTPStatus  int
	Body        []byte
	// SHA256 is the hash the receipt and the committed attempt carry.
	SHA256    string
	StoredAt  time.Time
	ExpiresAt time.Time
}

// Result returns the answer kept for one of the person's committed intents:
// shared.ErrNotFound when it was never kept, was declined, or has expired.
func (s *ExecutionService) Result(ctx context.Context, principalID, intentID string) (*ResultView, error) {
	view, err := s.econ.ViewFor(ctx, principalID, intentID, false)
	if err != nil {
		return nil, err
	}
	if view.State != econ.StateCommitted {
		return nil, shared.ErrNotFound
	}
	kept, err := s.results.Get(ctx, principalID, view.ID)
	if err != nil {
		return nil, err
	}
	for i := range view.Reservations {
		if r := view.Reservations[i]; r.State == econ.ReservationCommitted && (r.ID != kept.ReservationID || (r.Evidence.ResultHash != "" && r.Evidence.ResultHash != kept.SHA256)) {
			return nil, shared.ErrNotFound
		}
	}
	return &ResultView{
		IntentID: view.ID, ContentType: kept.ContentType, HTTPStatus: kept.HTTPStatus, Body: kept.Body, SHA256: kept.SHA256,
		StoredAt: kept.StoredAt, ExpiresAt: kept.ExpiresAt,
	}, nil
}
