package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
	"github.com/project-algebra/algebra/policy"
)

// CategoryDigitalServices is the Spend Pass category economic intents are
// evaluated under: APIs, data, inference, compute and other machine-payable
// services.
const CategoryDigitalServices = "digital_services"

// Default timings.
const (
	// DefaultLeaseTTL: how long a reservation may wait to begin. Short:
	// nothing irreversible has happened, so losing it is safe.
	DefaultLeaseTTL = 30 * time.Second
	// DefaultExecutionTimeout: how long an executing attempt may go without
	// evidence before it becomes UNKNOWN.
	DefaultExecutionTimeout = 2 * time.Minute
)

// Rejection reasons a reservation request can get.
const (
	RejectHeld             = "held_by_another_executor"
	RejectExecuting        = "attempt_in_progress"
	RejectUnknown          = "frozen_unknown_outcome"
	RejectCommitted        = "already_committed"
	RejectApproval         = "approval_required"
	RejectClosed           = "intent_closed"
	RejectAuthorityDenied  = "authority_denied"
	RejectQuoteOverBudget  = "quote_over_budget"
	RejectProviderExcluded = "provider_not_allowed"
)

// ReservationRejected is returned when an executor can't have the intent.
// Duplicate is true when the refusal prevented a possible duplicate
// commitment (someone else holds it, it may already have committed, or it
// did) — the number the product exists to drive.
type ReservationRejected struct {
	Reason      string     `json:"reason"`
	State       econ.State `json:"state"`
	Duplicate   bool       `json:"duplicate_prevented"`
	ReasonCodes []string   `json:"reason_codes,omitempty"`
	// HolderAgent is set when another executor holds the intent.
	HolderAgent string `json:"holder_agent,omitempty"`
}

func (e *ReservationRejected) Error() string {
	return fmt.Sprintf("reservation rejected: %s (intent %s)", e.Reason, e.State)
}

// ErrExecutionFrozen: the intent may already have committed; nothing new
// may start until reconciliation proves otherwise.
var ErrExecutionFrozen = errors.New("app: intent outcome is unknown; new commitments are blocked until reconciliation")

// EconomicService coordinates economic intents: one outcome, many possible
// executors, at most one commitment, and conservative handling of every
// ambiguous outcome.
type EconomicService struct {
	store    EconStore
	agents   AgentStore
	passes   SpendPassStore
	ledger   PassSpendLedger
	rails    map[string]Rail
	recovery map[string]ProviderRecovery
	receipts *IntentReceiptService
	log      *slog.Logger
	now      func() time.Time

	leaseTTL    time.Duration
	execTimeout time.Duration

	onResolved ResolutionHook
	executions ExecutionStore
}

// ResolutionHook is told when reconciliation settles an ambiguous attempt:
// the intent either committed or reopened, so any record kept about the
// attempt (the executor's own telemetry) can be brought up to date. It runs
// after the economic state is final and can't change it.
type ResolutionHook func(ctx context.Context, v *IntentView, reservationID string)

// SetResolutionHook registers the hook.
func (s *EconomicService) SetResolutionHook(h ResolutionHook) { s.onResolved = h }

func NewEconomicService(store EconStore, agents AgentStore, passes SpendPassStore, ledger PassSpendLedger) *EconomicService {
	return &EconomicService{
		store: store, agents: agents, passes: passes, ledger: ledger,
		rails: map[string]Rail{}, recovery: map[string]ProviderRecovery{},
		log: slog.Default(), now: time.Now, leaseTTL: DefaultLeaseTTL, execTimeout: DefaultExecutionTimeout,
	}
}

// RegisterRail makes a payment rail available by its Name.
func (s *EconomicService) RegisterRail(r Rail) { s.rails[r.Name()] = r }

// RegisterRecovery attaches a provider's recovery adapter.
func (s *EconomicService) RegisterRecovery(providerID string, r ProviderRecovery) {
	s.recovery[providerID] = r
}

// SetReceipts attaches Intent Receipt signing.
func (s *EconomicService) SetReceipts(r *IntentReceiptService) {
	s.receipts = r
	if r != nil && s.executions != nil {
		r.SetExecutions(s.executions)
	}
}

// attachExecutions lets receipts carry the executor's records, whichever of
// the two is wired first.
func (s *EconomicService) attachExecutions(e ExecutionStore) {
	s.executions = e
	if s.receipts != nil {
		s.receipts.SetExecutions(e)
	}
}

// SetLogger replaces the structured logger lifecycle events go to.
func (s *EconomicService) SetLogger(l *slog.Logger) { s.log = l }

// SetTimings overrides the lease and execution timeout (tests, config).
func (s *EconomicService) SetTimings(lease, exec time.Duration) {
	if lease > 0 {
		s.leaseTTL = lease
	}
	if exec > 0 {
		s.execTimeout = exec
	}
}

// IntentView is an intent with its attempts and history.
type IntentView struct {
	econ.Intent
	Reservations []econ.Reservation `json:"reservations"`
	Events       []EconEvent        `json:"events,omitempty"`
	Receipt      string             `json:"receipt,omitempty"`
	// Summary is the plain-language final economic state.
	Summary string `json:"summary"`
}

// executorPass is the executor's Spend Pass: its authority. Agents without
// a pass can't coordinate economic intents.
func (s *EconomicService) executorPass(ctx context.Context, agentID string) (*spendpass.Pass, error) {
	p, err := s.passes.GetByAgent(ctx, agentID)
	if errors.Is(err, shared.ErrNotFound) {
		return nil, fmt.Errorf("%w: this agent has no Spend Pass — economic intents need bounded authority", shared.ErrUnauthorized)
	}
	if err != nil {
		return nil, err
	}
	if !p.Active(s.now()) {
		return nil, fmt.Errorf("%w: the agent's Spend Pass is revoked or expired", shared.ErrUnauthorized)
	}
	if p.FrozenAt != nil {
		return nil, fmt.Errorf("%w: the agent's Spend Pass is frozen by its owner's kill switch", shared.ErrUnauthorized)
	}
	return p, nil
}

// evaluate is the pass's verdict on an amount for a provider. An empty
// provider (not chosen yet) defers the merchant rule to reservation time.
func evaluate(p *spendpass.Pass, provider string, amount, spent int64, currency string, now time.Time) *policy.PolicyDecision {
	pp := *p
	if provider == "" {
		pp.AllowedMerchants = nil
	}
	return pp.Evaluate(now, spendpass.Purchase{
		Merchant: provider, Category: CategoryDigitalServices, AmountMinor: amount, Currency: currency, SpentInWindow: spent,
	})
}

// ordersSpent is the pass's spend through the shopping flow in its window.
func (s *EconomicService) ordersSpent(ctx context.Context, p *spendpass.Pass) (int64, error) {
	if s.ledger == nil {
		return 0, nil
	}
	return s.ledger.SpentByAgentSince(ctx, p.AgentID, p.Currency, p.WindowStart(s.now()))
}

// CreateIntent creates an economic intent under the agent's Spend Pass, or
// returns the existing intent for the same outcome (created=false) —
// whatever state it's in, so an agent that asks for something already
// bought sees COMMITTED instead of buying it again.
func (s *EconomicService) CreateIntent(ctx context.Context, agentID string, spec econ.Spec) (*IntentView, bool, error) {
	p, err := s.executorPass(ctx, agentID)
	if err != nil {
		return nil, false, err
	}
	return s.create(ctx, p, agentID, spec)
}

// CreateIntentForPass is the person's own route: create an intent under
// one of their passes from the console.
func (s *EconomicService) CreateIntentForPass(ctx context.Context, userID, passID string, spec econ.Spec) (*IntentView, bool, error) {
	p, err := s.passes.Get(ctx, passID)
	if err != nil || p.UserID != userID {
		return nil, false, fmt.Errorf("%w: pass %s", shared.ErrNotFound, passID)
	}
	if !p.Active(s.now()) {
		return nil, false, fmt.Errorf("%w: the pass is revoked or expired", shared.ErrUnauthorized)
	}
	return s.create(ctx, p, "", spec)
}

func (s *EconomicService) create(ctx context.Context, p *spendpass.Pass, agentID string, spec econ.Spec) (*IntentView, bool, error) {
	now := s.now()
	if spec.Currency == "" {
		spec.Currency = p.Currency
	}
	in, err := econ.New(newID("eint"), p.UserID, p.ID, agentID, spec, now)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %s", shared.ErrConflict, err.Error())
	}
	spent, err := s.ordersSpent(ctx, p)
	if err != nil {
		return nil, false, err
	}
	d := evaluate(p, "", in.BudgetMaxMinor, spent, in.Currency, now)
	if d.Decision == policy.Deny {
		return nil, false, &AuthorityDenied{ReasonCodes: d.ReasonCodes}
	}
	if d.Decision == policy.RequireApproval {
		in.RequiresApproval = true
		in.State = econ.StateAwaitingApproval
	}
	stored, created, err := s.store.CreateIntent(ctx, in)
	if err != nil {
		return nil, false, err
	}
	if created {
		s.event(ctx, EconEvent{IntentID: stored.ID, AgentID: agentID, Event: "intent.created", Data: map[string]any{
			"capability": stored.Capability, "effect_key": stored.EffectKey, "intent_hash": stored.IntentHash,
			"budget_max_minor": stored.BudgetMaxMinor, "currency": stored.Currency, "spend_pass_id": p.ID,
		}})
		s.event(ctx, EconEvent{IntentID: stored.ID, AgentID: agentID, Event: "authority.evaluated", Data: map[string]any{
			"decision": string(d.Decision), "reason_codes": d.ReasonCodes, "policy_version": d.PolicyVersion,
		}})
	}
	v, err := s.View(ctx, stored.ID, false)
	return v, created, err
}

// AuthorityDenied is a Spend Pass refusal.
type AuthorityDenied struct{ ReasonCodes []string }

func (e *AuthorityDenied) Error() string {
	return "authority denied: " + strings.Join(e.ReasonCodes, ", ")
}

// Approve is the human yes an intent needs when its authority requires
// approval. Session only — an agent can never approve its own intent.
func (s *EconomicService) Approve(ctx context.Context, userID, intentID string) (*IntentView, error) {
	err := s.store.Atomically(ctx, intentID, "", func(u EconUnit) error {
		in := u.Intent()
		if in.PrincipalID != userID {
			return fmt.Errorf("%w: intent %s", shared.ErrNotFound, intentID)
		}
		if in.State != econ.StateAwaitingApproval {
			return fmt.Errorf("%w: intent is %s, not awaiting approval", shared.ErrConflict, in.State)
		}
		now := s.now()
		if err := in.Transition(econ.StateOpen, now); err != nil {
			return err
		}
		t := now.UTC()
		in.ApprovedAt = &t
		if err := u.SaveIntent(in); err != nil {
			return err
		}
		return u.AppendEvent(s.newEvent(EconEvent{IntentID: in.ID, Event: "authority.approved", Data: map[string]any{"approved_by": "human"}}))
	})
	if err != nil {
		return nil, err
	}
	return s.View(ctx, intentID, false)
}

// Cancel withdraws an intent that has no attempt in flight. An intent whose
// money may be moving can't be cancelled: that would paper over an
// ambiguous outcome.
func (s *EconomicService) Cancel(ctx context.Context, userID, intentID string) (*IntentView, error) {
	err := s.store.Atomically(ctx, intentID, "", func(u EconUnit) error {
		in := u.Intent()
		if in.PrincipalID != userID {
			return fmt.Errorf("%w: intent %s", shared.ErrNotFound, intentID)
		}
		if in.State.Blocked() {
			return fmt.Errorf("%w: an attempt may have committed money; wait for it to finish or reconcile", ErrExecutionFrozen)
		}
		now := s.now()
		if in.State == econ.StateReserved {
			if err := s.releaseActive(u, in, "cancelled_by_principal", now); err != nil {
				return err
			}
		}
		if err := in.Transition(econ.StateCancelled, now); err != nil {
			return fmt.Errorf("%w: %s", shared.ErrConflict, err.Error())
		}
		if err := u.SaveIntent(in); err != nil {
			return err
		}
		return u.AppendEvent(s.newEvent(EconEvent{IntentID: in.ID, Event: "intent.cancelled"}))
	})
	if err != nil {
		return nil, err
	}
	return s.View(ctx, intentID, false)
}

// ReserveRequest is an executor asking for the intent.
type ReserveRequest struct {
	ProviderID string         `json:"provider_id"`
	Rail       string         `json:"rail"`
	QuoteMinor int64          `json:"quote_minor,omitempty"`
	Semantics  econ.Semantics `json:"settlement_semantics,omitempty"`
}

// Reserve grants the executor the intent's single live reservation, or
// explains why not. Concurrent callers are serialized by the database:
// exactly one wins; the rest are refused, and every refusal that stopped a
// possible duplicate commitment is counted.
func (s *EconomicService) Reserve(ctx context.Context, agentID, intentID string, req ReserveRequest) (*econ.Reservation, error) {
	p, err := s.executorPass(ctx, agentID)
	if err != nil {
		return nil, err
	}
	req.ProviderID = strings.ToLower(strings.TrimSpace(req.ProviderID))
	req.Rail = strings.TrimSpace(req.Rail)
	if req.ProviderID == "" || req.Rail == "" {
		return nil, fmt.Errorf("%w: provider_id and rail are required", shared.ErrConflict)
	}
	if _, ok := s.rails[req.Rail]; !ok {
		return nil, fmt.Errorf("%w: unknown rail %q", shared.ErrConflict, req.Rail)
	}
	if req.Semantics == "" {
		req.Semantics = econ.SemanticsPrepaidExact
	}
	if !req.Semantics.Valid() {
		return nil, fmt.Errorf("%w: unknown settlement semantics %q", shared.ErrConflict, req.Semantics)
	}
	spent, err := s.ordersSpent(ctx, p)
	if err != nil {
		return nil, err
	}

	var granted *econ.Reservation
	var rejected *ReservationRejected
	err = s.store.Atomically(ctx, intentID, p.ID, func(u EconUnit) error {
		in := u.Intent()
		if in.PrincipalID != p.UserID {
			return fmt.Errorf("%w: intent %s", shared.ErrNotFound, intentID)
		}
		now := s.now()
		reject := func(reason string, duplicate bool, codes []string, holder string) error {
			rejected = &ReservationRejected{Reason: reason, State: in.State, Duplicate: duplicate, ReasonCodes: codes, HolderAgent: holder}
			if duplicate {
				in.BlockedAttempts++
				if err := u.SaveIntent(in); err != nil {
					return err
				}
			}
			return u.AppendEvent(s.newEvent(EconEvent{IntentID: in.ID, AgentID: agentID, Event: "reservation.rejected", Data: map[string]any{
				"reason": reason, "intent_state": string(in.State), "duplicate_commit_blocked": duplicate, "provider_id": req.ProviderID,
			}}))
		}

		// An executor retrying its own live reservation gets it back.
		if in.ActiveReservationID != "" {
			if cur, err := u.Reservation(in.ActiveReservationID); err == nil && cur.ExecutorAgentID == agentID && cur.State == econ.ReservationReserved {
				granted = cur
				return nil
			}
		}
		switch in.State {
		case econ.StateOpen:
		case econ.StateReserved:
			holder := ""
			if cur, err := u.Reservation(in.ActiveReservationID); err == nil {
				holder = cur.ExecutorAgentID
			}
			return reject(RejectHeld, true, nil, holder)
		case econ.StateExecuting:
			return reject(RejectExecuting, true, nil, "")
		case econ.StateUnknown, econ.StateReconciling:
			return reject(RejectUnknown, true, nil, "")
		case econ.StateCommitted:
			return reject(RejectCommitted, true, nil, "")
		case econ.StateAwaitingApproval:
			return reject(RejectApproval, false, nil, "")
		default:
			return reject(RejectClosed, false, nil, "")
		}
		if in.Expired(now) {
			if err := in.Transition(econ.StateExpired, now); err != nil {
				return err
			}
			if err := u.SaveIntent(in); err != nil {
				return err
			}
			if err := u.AppendEvent(s.newEvent(EconEvent{IntentID: in.ID, Event: "intent.expired"})); err != nil {
				return err
			}
			return reject(RejectClosed, false, nil, "")
		}
		if req.QuoteMinor > in.BudgetMaxMinor {
			return reject(RejectQuoteOverBudget, false, nil, "")
		}
		if len(in.ProviderPolicy.Providers) > 0 && !slices.Contains(in.ProviderPolicy.Providers, req.ProviderID) {
			return reject(RejectProviderExcluded, false, nil, "")
		}
		hold := in.BudgetMaxMinor
		if req.QuoteMinor > 0 && req.QuoteMinor < hold {
			hold = req.QuoteMinor
		}
		// The pass's controls: how fast its agent may spend, and how it may
		// treat a provider its person has never paid (policy_controls.go).
		if reason, codes, err := s.passControls(u, p, in, req.ProviderID, hold, now); err != nil {
			return err
		} else if reason != "" {
			return reject(reason, false, codes, "")
		}
		exposure, err := u.PassExposure(p.ID, p.WindowStart(now))
		if err != nil {
			return err
		}
		d := evaluate(p, req.ProviderID, hold, spent+exposure, in.Currency, now)
		if err := u.AppendEvent(s.newEvent(EconEvent{IntentID: in.ID, AgentID: agentID, Event: "authority.evaluated", Data: map[string]any{
			"decision": string(d.Decision), "reason_codes": d.ReasonCodes, "policy_version": d.PolicyVersion,
			"spend_pass_id": p.ID, "hold_minor": hold, "exposure_minor": spent + exposure,
		}})); err != nil {
			return err
		}
		switch {
		case d.Decision == policy.Deny:
			_ = u.AppendEvent(s.newEvent(EconEvent{IntentID: in.ID, AgentID: agentID, Event: "authority.denied", Data: map[string]any{"reason_codes": d.ReasonCodes}}))
			return reject(RejectAuthorityDenied, false, d.ReasonCodes, "")
		case d.Decision == policy.RequireApproval && in.ApprovedAt == nil:
			return reject(RejectApproval, false, d.ReasonCodes, "")
		}

		r := &econ.Reservation{
			ID: newID("rsv"), IntentID: in.ID, ExecutorAgentID: agentID, ExecutorPassID: p.ID,
			Attempt: in.Attempts + 1, State: econ.ReservationReserved, HoldMinor: hold,
			ProviderID: req.ProviderID, Rail: req.Rail, QuoteMinor: req.QuoteMinor, Semantics: req.Semantics,
			IdempotencyKey: econ.IdempotencyKeyFor(in.ID, in.Attempts+1), PolicyVersion: d.PolicyVersion,
			LeaseExpiresAt: now.Add(s.leaseTTL).UTC(), CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
		}
		if err := u.InsertReservation(r); err != nil {
			if errors.Is(err, ErrLiveReservationExists) {
				return reject(RejectHeld, true, nil, "")
			}
			return err
		}
		if err := in.Transition(econ.StateReserved, now); err != nil {
			return err
		}
		in.ActiveReservationID, in.Commitment = r.ID, econ.CommitmentReserved
		in.Attempts++
		if err := u.SaveIntent(in); err != nil {
			return err
		}
		granted = r
		return u.AppendEvent(s.newEvent(EconEvent{IntentID: in.ID, ReservationID: r.ID, AgentID: agentID, Attempt: r.Attempt, Event: "reservation.acquired", Data: map[string]any{
			"provider_id": r.ProviderID, "rail": r.Rail, "hold_minor": r.HoldMinor, "lease_expires_at": r.LeaseExpiresAt, "settlement_semantics": string(r.Semantics),
		}}))
	})
	if err != nil {
		return nil, err
	}
	if rejected != nil {
		return nil, rejected
	}
	return granted, nil
}

// liveFor finds the agent's live reservation on a locked intent.
func liveFor(u EconUnit, in *econ.Intent, agentID, reservationID string) (*econ.Reservation, error) {
	r, err := u.Reservation(reservationID)
	if err != nil || r.IntentID != in.ID || r.ExecutorAgentID != agentID {
		return nil, fmt.Errorf("%w: reservation %s", shared.ErrNotFound, reservationID)
	}
	return r, nil
}

// Begin crosses the irreversible boundary for a reservation: the executor's
// authority is re-evaluated immediately beforehand (revoked, expired, over
// budget), and from here a failure without evidence is UNKNOWN, never FAILED.
func (s *EconomicService) Begin(ctx context.Context, agentID, intentID, reservationID string) (*econ.Reservation, error) {
	p, err := s.executorPass(ctx, agentID)
	if err != nil {
		return nil, err
	}
	spent, err := s.ordersSpent(ctx, p)
	if err != nil {
		return nil, err
	}
	var out *econ.Reservation
	var denied error
	err = s.store.Atomically(ctx, intentID, p.ID, func(u EconUnit) error {
		in := u.Intent()
		r, err := liveFor(u, in, agentID, reservationID)
		if err != nil {
			return err
		}
		if r.State == econ.ReservationExecuting {
			out = r // retrying begin is harmless
			return nil
		}
		if r.State != econ.ReservationReserved {
			return fmt.Errorf("%w: reservation is %s", shared.ErrConflict, r.State)
		}
		now := s.now()
		if !now.Before(r.LeaseExpiresAt) {
			if err := s.expireReservation(u, in, r, now); err != nil {
				return err
			}
			denied = fmt.Errorf("%w: the reservation's lease expired before execution began; reserve again", shared.ErrConflict)
			return nil
		}
		exposure, err := u.PassExposure(p.ID, p.WindowStart(now))
		if err != nil {
			return err
		}
		// Exposure already counts this attempt's hold.
		d := evaluate(p, r.ProviderID, r.HoldMinor, spent+exposure-r.HoldMinor, in.Currency, now)
		if d.Decision == policy.Deny || (d.Decision == policy.RequireApproval && in.ApprovedAt == nil) {
			if err := s.releaseActive(u, in, "policy_recheck_denied", now); err != nil {
				return err
			}
			if err := in.Transition(econ.StateOpen, now); err != nil {
				return err
			}
			in.Commitment = econ.CommitmentNone
			if err := u.SaveIntent(in); err != nil {
				return err
			}
			_ = u.AppendEvent(s.newEvent(EconEvent{IntentID: in.ID, ReservationID: r.ID, AgentID: agentID, Event: "authority.denied", Data: map[string]any{"reason_codes": d.ReasonCodes, "at": "pre_execution_recheck"}}))
			denied = &AuthorityDenied{ReasonCodes: d.ReasonCodes}
			return nil
		}
		if err := u.AppendEvent(s.newEvent(EconEvent{IntentID: in.ID, ReservationID: r.ID, AgentID: agentID, Attempt: r.Attempt, Event: "authority.rechecked", Data: map[string]any{
			"decision": string(d.Decision), "reason_codes": d.ReasonCodes, "at": "pre_execution_recheck",
		}})); err != nil {
			return err
		}
		if err := r.Transition(econ.ReservationExecuting, now); err != nil {
			return err
		}
		deadline := now.Add(s.execTimeout).UTC()
		r.ExecutionDeadline = &deadline
		if err := u.SaveReservation(r); err != nil {
			return err
		}
		if err := in.Transition(econ.StateExecuting, now); err != nil {
			return err
		}
		if err := u.SaveIntent(in); err != nil {
			return err
		}
		out = r
		return u.AppendEvent(s.newEvent(EconEvent{IntentID: in.ID, ReservationID: r.ID, AgentID: agentID, Attempt: r.Attempt, Event: "execution.started", Data: map[string]any{
			"provider_id": r.ProviderID, "rail": r.Rail, "execution_deadline": deadline,
		}}))
	})
	if err != nil {
		return nil, err
	}
	if denied != nil {
		return nil, denied
	}
	return out, nil
}

// AuthorizePayment has Algebra sign a single-use payment for an executing
// attempt through the reservation's rail, so the agent pays without ever
// holding a key. Recorded before it's returned: once released, "nothing
// happened" needs proof from the rail.
func (s *EconomicService) AuthorizePayment(ctx context.Context, agentID, intentID, reservationID string, req PaymentRequest) (*PaymentAuthority, error) {
	// The last moment before money can move: a pass frozen by the kill switch
	// (or revoked) since the attempt began releases nothing.
	if _, err := s.executorPass(ctx, agentID); err != nil {
		return nil, err
	}
	var snapshot econ.Reservation
	err := s.store.Atomically(ctx, intentID, "", func(u EconUnit) error {
		in := u.Intent()
		r, err := liveFor(u, in, agentID, reservationID)
		if err != nil {
			return err
		}
		if r.State != econ.ReservationExecuting {
			return fmt.Errorf("%w: payment authority is released only for an executing attempt (this one is %s)", shared.ErrConflict, r.State)
		}
		if r.Evidence.AuthorityIssued {
			return fmt.Errorf("%w: this attempt already has its payment authority; a second payment needs a new attempt", shared.ErrConflict)
		}
		snapshot = *r
		return nil
	})
	if err != nil {
		return nil, err
	}
	authorizer, ok := s.rails[snapshot.Rail].(PaymentAuthorizer)
	if !ok {
		return nil, fmt.Errorf("%w: rail %s doesn't release payment authority through Algebra", shared.ErrNotImplemented, snapshot.Rail)
	}
	auth, err := authorizer.Authorize(ctx, &snapshot, req)
	if err != nil {
		return nil, err
	}
	if auth.AmountMinor > snapshot.HoldMinor {
		return nil, fmt.Errorf("%w: the provider asked for %d, more than this attempt's hold of %d", shared.ErrConflict, auth.AmountMinor, snapshot.HoldMinor)
	}
	err = s.store.Atomically(ctx, intentID, "", func(u EconUnit) error {
		r, err := u.Reservation(reservationID)
		if err != nil {
			return err
		}
		if r.State != econ.ReservationExecuting || r.Evidence.AuthorityIssued {
			return fmt.Errorf("%w: the attempt changed while authorizing payment", shared.ErrConflict)
		}
		now := s.now().UTC()
		ev := auth.Evidence
		ev.AuthorityIssued, ev.AuthorityIssuedAt = true, &now
		r.Evidence = mergeEvidence(r.Evidence, ev)
		r.UpdatedAt = now
		if err := u.SaveReservation(r); err != nil {
			return err
		}
		return u.AppendEvent(s.newEvent(EconEvent{IntentID: intentID, ReservationID: r.ID, AgentID: agentID, Attempt: r.Attempt, Event: "payment.authorized", Data: map[string]any{
			"rail": r.Rail, "protocol": ev.Protocol, "scheme": ev.Scheme, "network": ev.Network, "amount_minor": auth.AmountMinor,
			"payment_identifier": ev.PaymentID, "transaction_signature": ev.Transaction, "pay_to": ev.PayTo,
		}}))
	})
	if err != nil {
		return nil, err
	}
	return auth, nil
}

// CompletionReport is what an executor observed. An executor can never
// commit an intent on its word: FULFILLED is checked against the rail.
type CompletionReport struct {
	Outcome  econ.Outcome  `json:"outcome"`
	Evidence econ.Evidence `json:"evidence"`
	Detail   string        `json:"detail,omitempty"`
	// Fulfillment lets an executor say that money moved but the result was
	// unusable (econ.FulfillmentNotFulfilled, with outcome SETTLED). It is
	// the one fulfilment claim taken on an executor's word, because it can
	// only make an attempt look worse, never commit more money; a claim of
	// success still needs outcome FULFILLED and a rail that proves payment.
	Fulfillment econ.Fulfillment `json:"fulfillment,omitempty"`
}

// Complete records an executor's report for its executing attempt and
// moves the intent only as far as evidence allows.
func (s *EconomicService) Complete(ctx context.Context, agentID, intentID, reservationID string, rep CompletionReport) (*IntentView, error) {
	var snapshot econ.Reservation
	var done bool
	err := s.store.Atomically(ctx, intentID, "", func(u EconUnit) error {
		in := u.Intent()
		r, err := liveFor(u, in, agentID, reservationID)
		if err != nil {
			return err
		}
		if !r.State.Live() {
			done = true // already final: a retried report changes nothing
			return nil
		}
		if r.State != econ.ReservationExecuting {
			return fmt.Errorf("%w: reservation is %s; begin it before reporting an outcome", shared.ErrConflict, r.State)
		}
		// An executor may add what it observed, never overwrite what
		// Algebra itself recorded when it released payment authority.
		if r.Evidence.Transaction != "" && rep.Evidence.Transaction != "" && rep.Evidence.Transaction != r.Evidence.Transaction {
			return fmt.Errorf("%w: reported transaction doesn't match the payment Algebra authorized", shared.ErrConflict)
		}
		r.Evidence = mergeEvidence(r.Evidence, rep.Evidence)
		now := s.now()
		switch rep.Outcome {
		case econ.OutcomeUnknown:
			return s.toUnknown(u, in, r, "executor_reported_unknown: "+trunc(rep.Detail, 120), now)
		case econ.OutcomeNoCommitment:
			if !r.Evidence.AuthorityIssued {
				// Algebra never released payment authority, so no money
				// could have moved through it.
				return s.releaseToOpen(u, in, r, "no_commitment_before_payment_authority", econ.OutcomeNoCommitment, now)
			}
		case econ.OutcomeFulfilled, econ.OutcomeSettled:
		default:
			return fmt.Errorf("%w: outcome must be FULFILLED, SETTLED, NO_COMMITMENT or UNKNOWN", shared.ErrConflict)
		}
		if err := u.SaveReservation(r); err != nil {
			return err
		}
		snapshot = *r
		return nil
	})
	if err != nil {
		return nil, err
	}
	if done || snapshot.ID == "" {
		return s.View(ctx, intentID, false)
	}
	// Money may have moved: ask the rail, outside the lock.
	st, rerr := s.settlement(ctx, snapshot)
	claim := econ.FulfillmentUnknown
	switch {
	case rep.Outcome == econ.OutcomeFulfilled:
		claim = econ.FulfillmentFulfilled
	case rep.Outcome == econ.OutcomeSettled && rep.Fulfillment == econ.FulfillmentNotFulfilled:
		claim = econ.FulfillmentNotFulfilled
	}
	err = s.store.Atomically(ctx, intentID, "", func(u EconUnit) error {
		in := u.Intent()
		r, err := u.Reservation(reservationID)
		if err != nil {
			return err
		}
		if r.State != econ.ReservationExecuting {
			return nil // something else resolved it meanwhile
		}
		now := s.now()
		if rerr != nil {
			return s.toUnknown(u, in, r, "rail_unavailable: "+trunc(rerr.Error(), 120), now)
		}
		return s.applySettlement(u, in, r, st, claim, rep.Evidence.ResultHash, now)
	})
	if err != nil {
		return nil, err
	}
	s.maybeSign(ctx, intentID)
	return s.View(ctx, intentID, false)
}

// applySettlement moves an executing or reconciling attempt as far as the
// rail's proof allows.
func (s *EconomicService) applySettlement(u EconUnit, in *econ.Intent, r *econ.Reservation, st Settlement, f econ.Fulfillment, resultHash string, now time.Time) error {
	switch st.Status {
	case SettlementSettled:
		outcome := econ.OutcomeSettled
		if f == econ.FulfillmentFulfilled {
			outcome = econ.OutcomeFulfilled
		}
		return s.commit(u, in, r, st, f, outcome, resultHash, now)
	case SettlementNotSettled:
		return s.releaseToOpen(u, in, r, "rail_proved_no_settlement: "+trunc(st.Detail, 120), econ.OutcomeNoCommitment, now)
	default:
		if r.State == econ.ReservationReconciling {
			return u.AppendEvent(s.newEvent(EconEvent{IntentID: in.ID, ReservationID: r.ID, Attempt: r.Attempt, Event: "reconciliation.unresolved", Data: map[string]any{
				"settlement": string(st.Status), "detail": trunc(st.Detail, 160),
			}}))
		}
		return s.toUnknown(u, in, r, "settlement_"+strings.ToLower(string(st.Status)), now)
	}
}

func (s *EconomicService) settlement(ctx context.Context, r econ.Reservation) (Settlement, error) {
	rail, ok := s.rails[r.Rail]
	if !ok {
		return Settlement{}, fmt.Errorf("rail %s not configured", r.Rail)
	}
	return rail.Settlement(ctx, r.Evidence)
}

// commit records proof that money moved for this intent: terminal.
func (s *EconomicService) commit(u EconUnit, in *econ.Intent, r *econ.Reservation, st Settlement, f econ.Fulfillment, outcome econ.Outcome, resultHash string, now time.Time) error {
	if st.AmountMinor > r.HoldMinor {
		// Settled for more than authorized: still committed (the money
		// moved), flagged loudly rather than hidden.
		_ = u.AppendEvent(s.newEvent(EconEvent{IntentID: in.ID, ReservationID: r.ID, Event: "payment.over_hold", Data: map[string]any{"amount_minor": st.AmountMinor, "hold_minor": r.HoldMinor}}))
	}
	r.Evidence = mergeEvidence(r.Evidence, econ.Evidence{
		Transaction: st.Transaction, AmountMinor: st.AmountMinor, Network: st.Network, Asset: st.Asset, Payer: st.Payer, PayTo: st.PayTo, ResultHash: resultHash, Test: st.Test,
	})
	if st.AmountMinor > 0 {
		r.HoldMinor = st.AmountMinor // unused authority is released
	}
	r.Outcome = outcome
	if err := r.Transition(econ.ReservationCommitted, now); err != nil {
		return err
	}
	if err := u.SaveReservation(r); err != nil {
		return err
	}
	if err := in.Transition(econ.StateCommitted, now); err != nil {
		return err
	}
	in.Commitment, in.Fulfillment, in.CommittedMinor, in.ActiveReservationID = econ.CommitmentSettled, f, r.Evidence.AmountMinor, ""
	if err := u.SaveIntent(in); err != nil {
		return err
	}
	if err := u.AppendEvent(s.newEvent(EconEvent{IntentID: in.ID, ReservationID: r.ID, AgentID: r.ExecutorAgentID, Attempt: r.Attempt, Event: "payment.confirmed", Data: map[string]any{
		"rail": r.Rail, "transaction_signature": st.Transaction, "amount_minor": st.AmountMinor, "network": st.Network, "test": st.Test,
	}})); err != nil {
		return err
	}
	return u.AppendEvent(s.newEvent(EconEvent{IntentID: in.ID, ReservationID: r.ID, AgentID: r.ExecutorAgentID, Attempt: r.Attempt, Event: "intent.committed", Data: map[string]any{
		"fulfillment": string(f), "result_hash": resultHash, "committed_minor": in.CommittedMinor,
	}}))
}

// toUnknown freezes the intent: an attempt may have committed.
func (s *EconomicService) toUnknown(u EconUnit, in *econ.Intent, r *econ.Reservation, reason string, now time.Time) error {
	if err := r.Transition(econ.ReservationUnknown, now); err != nil {
		return err
	}
	r.Outcome = econ.OutcomeUnknown
	if err := u.SaveReservation(r); err != nil {
		return err
	}
	if err := in.Transition(econ.StateUnknown, now); err != nil {
		return err
	}
	in.Commitment = econ.CommitmentUnknown
	if err := u.SaveIntent(in); err != nil {
		return err
	}
	return u.AppendEvent(s.newEvent(EconEvent{IntentID: in.ID, ReservationID: r.ID, AgentID: r.ExecutorAgentID, Attempt: r.Attempt, Event: "execution.timeout", Data: map[string]any{
		"reason": reason, "authority_issued": r.Evidence.AuthorityIssued, "new_commitments": "blocked",
	}}))
}

// releaseToOpen ends an attempt proven to have moved no money; the intent
// is wanted again.
func (s *EconomicService) releaseToOpen(u EconUnit, in *econ.Intent, r *econ.Reservation, reason string, outcome econ.Outcome, now time.Time) error {
	r.Outcome, r.ReleaseReason = outcome, reason
	if err := r.Transition(econ.ReservationReleased, now); err != nil {
		return err
	}
	if err := u.SaveReservation(r); err != nil {
		return err
	}
	if err := in.Transition(econ.StateOpen, now); err != nil {
		return err
	}
	in.Commitment, in.ActiveReservationID = econ.CommitmentNone, ""
	if err := u.SaveIntent(in); err != nil {
		return err
	}
	return u.AppendEvent(s.newEvent(EconEvent{IntentID: in.ID, ReservationID: r.ID, AgentID: r.ExecutorAgentID, Attempt: r.Attempt, Event: "reservation.released", Data: map[string]any{"reason": reason}}))
}

// releaseActive releases a not-yet-executing reservation (cancel, policy
// recheck) without touching the intent's state.
func (s *EconomicService) releaseActive(u EconUnit, in *econ.Intent, reason string, now time.Time) error {
	r, err := u.Reservation(in.ActiveReservationID)
	if err != nil {
		return err
	}
	r.ReleaseReason = reason
	if err := r.Transition(econ.ReservationReleased, now); err != nil {
		return err
	}
	if err := u.SaveReservation(r); err != nil {
		return err
	}
	in.ActiveReservationID = ""
	return u.AppendEvent(s.newEvent(EconEvent{IntentID: in.ID, ReservationID: r.ID, AgentID: r.ExecutorAgentID, Attempt: r.Attempt, Event: "reservation.released", Data: map[string]any{"reason": reason}}))
}

func (s *EconomicService) expireReservation(u EconUnit, in *econ.Intent, r *econ.Reservation, now time.Time) error {
	r.ReleaseReason = "lease_expired_before_execution"
	if err := r.Transition(econ.ReservationExpired, now); err != nil {
		return err
	}
	if err := u.SaveReservation(r); err != nil {
		return err
	}
	if in.ActiveReservationID == r.ID && in.State == econ.StateReserved {
		if err := in.Transition(econ.StateOpen, now); err != nil {
			return err
		}
		in.ActiveReservationID, in.Commitment = "", econ.CommitmentNone
		if err := u.SaveIntent(in); err != nil {
			return err
		}
	}
	return u.AppendEvent(s.newEvent(EconEvent{IntentID: in.ID, ReservationID: r.ID, AgentID: r.ExecutorAgentID, Attempt: r.Attempt, Event: "reservation.released", Data: map[string]any{"reason": r.ReleaseReason}}))
}

// Release gives up a reservation that hasn't begun: nothing irreversible
// happened, so the intent is open again at once.
func (s *EconomicService) Release(ctx context.Context, agentID, intentID, reservationID string) error {
	return s.store.Atomically(ctx, intentID, "", func(u EconUnit) error {
		in := u.Intent()
		r, err := liveFor(u, in, agentID, reservationID)
		if err != nil {
			return err
		}
		if r.State != econ.ReservationReserved {
			return fmt.Errorf("%w: only a reservation that hasn't begun can be released (this one is %s); report its outcome instead", shared.ErrConflict, r.State)
		}
		now := s.now()
		if err := s.releaseActive(u, in, "released_by_executor", now); err != nil {
			return err
		}
		if err := in.Transition(econ.StateOpen, now); err != nil {
			return err
		}
		in.Commitment = econ.CommitmentNone
		return u.SaveIntent(in)
	})
}

// ReconcileResult is what reconciliation established.
type ReconcileResult struct {
	State       econ.State       `json:"state"`
	Settlement  SettlementStatus `json:"settlement,omitempty"`
	Recovery    RecoveryStatus   `json:"recovery,omitempty"`
	Summary     string           `json:"summary"`
	Unresolved  bool             `json:"unresolved"`
	Reservation string           `json:"reservation_id,omitempty"`
}

// Reconcile gathers evidence for an intent whose outcome is unknown: the
// rail for settlement, then the provider for the result. It reports
// exactly what was proven and never invents certainty.
func (s *EconomicService) Reconcile(ctx context.Context, intentID string) (*ReconcileResult, error) {
	var snapshot econ.Reservation
	var state econ.State
	err := s.store.Atomically(ctx, intentID, "", func(u EconUnit) error {
		in := u.Intent()
		state = in.State
		if in.State != econ.StateUnknown && in.State != econ.StateReconciling {
			return nil
		}
		r, err := u.Reservation(in.ActiveReservationID)
		if err != nil {
			return err
		}
		now := s.now()
		if in.State == econ.StateUnknown {
			if err := r.Transition(econ.ReservationReconciling, now); err != nil {
				return err
			}
			if err := u.SaveReservation(r); err != nil {
				return err
			}
			if err := in.Transition(econ.StateReconciling, now); err != nil {
				return err
			}
			if err := u.SaveIntent(in); err != nil {
				return err
			}
			if err := u.AppendEvent(s.newEvent(EconEvent{IntentID: in.ID, ReservationID: r.ID, Attempt: r.Attempt, Event: "reconciliation.started"})); err != nil {
				return err
			}
		}
		snapshot = *r
		return nil
	})
	if err != nil {
		return nil, err
	}
	if snapshot.ID == "" {
		return &ReconcileResult{State: state, Summary: "Nothing to reconcile: the intent's outcome is known."}, nil
	}

	var st Settlement
	if !snapshot.Evidence.AuthorityIssued {
		// Algebra never released payment authority for this attempt (the
		// executor stopped before asking for it), and a payment value is only
		// ever returned after it is recorded. No money could have moved, and
		// no rail needs to say so: leaving this RECONCILING forever would
		// block the intent on an answer no rail can give.
		st = Settlement{Status: SettlementNotSettled, Detail: "Algebra never released payment authority for this attempt"}
	} else {
		var rerr error
		if st, rerr = s.settlement(ctx, snapshot); rerr != nil {
			st = Settlement{Status: SettlementUnknown, Detail: rerr.Error()}
		}
	}
	rec := Recovery{Status: RecoveryUnknown, Detail: "no recovery adapter for this provider"}
	if st.Status == SettlementSettled {
		if pr, ok := s.recovery[snapshot.ProviderID]; ok {
			if got, err := pr.Recover(ctx, snapshot); err == nil {
				rec = got
			} else {
				rec.Detail = err.Error()
			}
		}
	}

	res := &ReconcileResult{Settlement: st.Status, Recovery: rec.Status, Reservation: snapshot.ID}
	err = s.store.Atomically(ctx, intentID, "", func(u EconUnit) error {
		in := u.Intent()
		r, err := u.Reservation(snapshot.ID)
		if err != nil {
			return err
		}
		if r.State != econ.ReservationReconciling {
			res.State = in.State
			return nil
		}
		now := s.now()
		if st.Status == SettlementSettled {
			_ = u.AppendEvent(s.newEvent(EconEvent{IntentID: in.ID, ReservationID: r.ID, Attempt: r.Attempt, Event: "reconciliation.settlement_found", Data: map[string]any{"transaction_signature": st.Transaction, "amount_minor": st.AmountMinor}}))
			if rec.Status == RecoveryFulfilled {
				_ = u.AppendEvent(s.newEvent(EconEvent{IntentID: in.ID, ReservationID: r.ID, Attempt: r.Attempt, Event: "reconciliation.provider_found", Data: map[string]any{"result_hash": rec.ResultHash, "provider_operation_id": rec.OperationID}}))
				r.Evidence.ProviderOperationID = firstNonEmpty(r.Evidence.ProviderOperationID, rec.OperationID)
			}
		}
		f := econ.FulfillmentUnknown
		switch rec.Status {
		case RecoveryFulfilled:
			f = econ.FulfillmentFulfilled
		case RecoveryNotFulfilled:
			f = econ.FulfillmentNotFulfilled
		}
		if err := s.applySettlement(u, in, r, st, f, firstNonEmpty(rec.ResultHash, r.Evidence.ResultHash), now); err != nil {
			return err
		}
		res.State = in.State
		return nil
	})
	if err != nil {
		return nil, err
	}
	res.Unresolved = res.State == econ.StateReconciling
	res.Summary = reconcileSummary(res)
	s.maybeSign(ctx, intentID)
	if s.onResolved != nil && (res.State == econ.StateCommitted || res.State == econ.StateOpen) {
		if v, err := s.View(ctx, intentID, false); err == nil {
			s.onResolved(ctx, v, snapshot.ID)
		}
	}
	return res, nil
}

func reconcileSummary(r *ReconcileResult) string {
	switch {
	case r.State == econ.StateCommitted && r.Recovery == RecoveryFulfilled:
		return "Settlement found and the result recovered: the original attempt committed. No new payment was allowed."
	case r.State == econ.StateCommitted:
		return "Payment confirmed; the resource outcome is unknown. A duplicate payment was blocked — provider or manual recovery is required for the result."
	case r.State == econ.StateOpen:
		return "Proven that no money moved: the intent is open again and can be attempted safely."
	default:
		return "Still unresolved: not enough evidence either way. New commitments stay blocked."
	}
}

// Sweep does the periodic work: expired leases free their intents,
// overdue executions become UNKNOWN (never FAILED), unknown outcomes are
// reconciled, and intents past their deadline with nothing in flight expire.
func (s *EconomicService) Sweep(ctx context.Context) (int, error) {
	now := s.now()
	n := 0
	leases, err := s.store.ExpiredLeases(ctx, now, 100)
	if err != nil {
		return n, err
	}
	for _, r := range leases {
		err := s.store.Atomically(ctx, r.IntentID, "", func(u EconUnit) error {
			cur, err := u.Reservation(r.ID)
			if err != nil || cur.State != econ.ReservationReserved || now.Before(cur.LeaseExpiresAt) {
				return err
			}
			return s.expireReservation(u, u.Intent(), cur, now)
		})
		if err == nil {
			n++
		}
	}
	overdue, err := s.store.OverdueExecutions(ctx, now, 100)
	if err != nil {
		return n, err
	}
	for _, r := range overdue {
		err := s.store.Atomically(ctx, r.IntentID, "", func(u EconUnit) error {
			cur, err := u.Reservation(r.ID)
			if err != nil || cur.State != econ.ReservationExecuting || cur.ExecutionDeadline == nil || now.Before(*cur.ExecutionDeadline) {
				return err
			}
			return s.toUnknown(u, u.Intent(), cur, "no_evidence_before_execution_deadline", now)
		})
		if err == nil {
			n++
		}
	}
	unresolved, err := s.store.Unresolved(ctx, 50)
	if err != nil {
		return n, err
	}
	for _, r := range unresolved {
		if _, err := s.Reconcile(ctx, r.IntentID); err == nil {
			n++
		}
	}
	expired, err := s.store.ExpiredIntents(ctx, now, 100)
	if err != nil {
		return n, err
	}
	for _, id := range expired {
		err := s.store.Atomically(ctx, id, "", func(u EconUnit) error {
			in := u.Intent()
			if !in.Expired(now) || (in.State != econ.StateOpen && in.State != econ.StateAwaitingApproval) {
				return nil
			}
			if err := in.Transition(econ.StateExpired, now); err != nil {
				return err
			}
			if err := u.SaveIntent(in); err != nil {
				return err
			}
			return u.AppendEvent(s.newEvent(EconEvent{IntentID: in.ID, Event: "intent.expired"}))
		})
		if err == nil {
			n++
		}
	}
	return n, nil
}

// View returns an intent with its attempts (and, when asked, history).
func (s *EconomicService) View(ctx context.Context, intentID string, withEvents bool) (*IntentView, error) {
	in, err := s.store.GetIntent(ctx, intentID)
	if err != nil {
		return nil, err
	}
	rs, err := s.store.Reservations(ctx, intentID)
	if err != nil {
		return nil, err
	}
	v := &IntentView{Intent: *in, Reservations: rs, Summary: Summarize(in)}
	if withEvents {
		if v.Events, err = s.store.Events(ctx, intentID); err != nil {
			return nil, err
		}
	}
	if in.State == econ.StateCommitted {
		v.Receipt, _ = s.store.GetReceipt(ctx, intentID)
	}
	return v, nil
}

// ViewFor is View for an agent or person: only intents of their own
// principal are visible.
func (s *EconomicService) ViewFor(ctx context.Context, principalID, intentID string, withEvents bool) (*IntentView, error) {
	v, err := s.View(ctx, intentID, withEvents)
	if err != nil {
		return nil, err
	}
	if v.PrincipalID != principalID {
		return nil, fmt.Errorf("%w: intent %s", shared.ErrNotFound, intentID)
	}
	return v, nil
}

// PrincipalOf is the person an agent acts for.
func (s *EconomicService) PrincipalOf(ctx context.Context, agentID string) (string, error) {
	ag, err := s.agents.Get(ctx, agentID)
	if err != nil {
		return "", err
	}
	return ag.UserID, nil
}

// List is a principal's recent intents.
func (s *EconomicService) List(ctx context.Context, principalID string, limit int) ([]econ.Intent, error) {
	return s.store.ListIntents(ctx, principalID, limit)
}

// Stats are a principal's coordination numbers since a time.
func (s *EconomicService) Stats(ctx context.Context, principalID string, since time.Time) (*EconStats, error) {
	return s.store.Stats(ctx, principalID, since)
}

// Summarize is the plain-language final economic state — what's proven,
// nothing more.
func Summarize(in *econ.Intent) string {
	switch in.State {
	case econ.StateCommitted:
		switch in.Fulfillment {
		case econ.FulfillmentFulfilled:
			return "Committed: paid once and the result was received."
		case econ.FulfillmentNotFulfilled:
			return "Payment confirmed, but the provider didn't deliver a usable result. Duplicate payment blocked — ask the provider for a refund or a redo."
		}
		return "Payment confirmed; resource outcome unknown. Duplicate payment blocked — provider or manual recovery required."
	case econ.StateUnknown, econ.StateReconciling:
		return "Outcome unknown: an attempt may have committed money. New commitments are blocked until reconciliation proves what happened."
	case econ.StateExecuting:
		return "An executor is attempting the commitment."
	case econ.StateReserved:
		return "Reserved by one executor; nothing irreversible yet."
	case econ.StateAwaitingApproval:
		return "Waiting for the person's approval."
	case econ.StateOpen:
		return "Open: no money committed."
	case econ.StateCancelled:
		return "Cancelled with no money committed."
	case econ.StateExpired:
		return "Expired with no money committed."
	}
	return string(in.State)
}

func (s *EconomicService) maybeSign(ctx context.Context, intentID string) {
	if s.receipts == nil {
		return
	}
	if _, err := s.store.GetReceipt(ctx, intentID); err == nil {
		return
	}
	v, err := s.View(ctx, intentID, true)
	if err != nil || v.State != econ.StateCommitted {
		return
	}
	jws, id, err := s.receipts.Sign(ctx, v)
	if err != nil {
		s.log.Error("economic receipt signing failed", "intent_id", intentID, "err", err)
		return
	}
	if err := s.store.SaveReceipt(ctx, intentID, id, jws, s.now()); err != nil {
		s.log.Error("economic receipt store failed", "intent_id", intentID, "err", err)
		return
	}
	s.event(ctx, EconEvent{IntentID: intentID, Event: "receipt.signed", Data: map[string]any{"receipt_id": id}})
}

func (s *EconomicService) newEvent(e EconEvent) EconEvent {
	e.ID = newID("eev")
	e.CreatedAt = s.now().UTC()
	s.log.Info("economic."+e.Event, "intent_id", e.IntentID, "reservation_id", e.ReservationID, "agent_id", e.AgentID, "attempt", e.Attempt)
	return e
}

// event appends outside a unit (best-effort telemetry for create/sign).
func (s *EconomicService) event(ctx context.Context, e EconEvent) {
	_ = s.store.Atomically(ctx, e.IntentID, "", func(u EconUnit) error { return u.AppendEvent(s.newEvent(e)) })
}

// mergeEvidence fills empty fields of base from add; never clears or
// overwrites what's already recorded.
func mergeEvidence(base, add econ.Evidence) econ.Evidence {
	set := func(dst *string, v string) {
		if *dst == "" {
			*dst = v
		}
	}
	set(&base.Rail, add.Rail)
	set(&base.Protocol, add.Protocol)
	set(&base.Scheme, add.Scheme)
	set(&base.Network, add.Network)
	set(&base.Asset, add.Asset)
	set(&base.PaymentID, add.PaymentID)
	set(&base.Transaction, add.Transaction)
	set(&base.Payer, add.Payer)
	set(&base.PayTo, add.PayTo)
	set(&base.ProviderOperationID, add.ProviderOperationID)
	set(&base.RequestHash, add.RequestHash)
	set(&base.ResultHash, add.ResultHash)
	if base.AmountMinor == 0 {
		base.AmountMinor = add.AmountMinor
	}
	if base.ValidUntilHeight == 0 {
		base.ValidUntilHeight = add.ValidUntilHeight
	}
	if add.AuthorityIssued && !base.AuthorityIssued {
		base.AuthorityIssued, base.AuthorityIssuedAt = true, add.AuthorityIssuedAt
	}
	base.Test = base.Test || add.Test
	return base
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
