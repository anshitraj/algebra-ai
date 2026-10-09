// Package econ defines Algebra's economic coordination primitives: the
// Economic Intent (one outcome a principal wants paid for), the Reservation
// (one executor's exclusive right to attempt the irreversible commitment),
// and the evidence that settles what happened.
//
// The rule everything here serves: a timeout is not proof that money moved
// or didn't. So lifecycle, money (commitment) and result (fulfilment) are
// three separate states, and an attempt that may have committed becomes
// UNKNOWN — never FAILED — until evidence says otherwise.
package econ

import "fmt"

// State is an Economic Intent's lifecycle state.
type State string

const (
	// StateAwaitingApproval: the executor's authority requires a human yes
	// before anyone may reserve it.
	StateAwaitingApproval State = "AWAITING_APPROVAL"
	// StateOpen: wanted, not being attempted.
	StateOpen State = "OPEN"
	// StateReserved: one executor holds the reservation; nothing
	// irreversible has been released yet.
	StateReserved State = "RESERVED"
	// StateExecuting: payment authority was released; from here, a failure
	// without evidence is UNKNOWN.
	StateExecuting State = "EXECUTING"
	// StateUnknown: an attempt may have committed; no new commitment may
	// start.
	StateUnknown State = "UNKNOWN"
	// StateReconciling: evidence is being gathered for an UNKNOWN attempt;
	// still blocked.
	StateReconciling State = "RECONCILING"
	// StateCommitted: money was committed for this outcome. Terminal: no
	// further commitment is ever released for it.
	StateCommitted State = "COMMITTED"
	StateCancelled State = "CANCELLED"
	StateExpired   State = "EXPIRED"
)

var intentTransitions = map[State][]State{
	StateAwaitingApproval: {StateOpen, StateCancelled, StateExpired},
	// OPEN -> AWAITING_APPROVAL: every provider that could do it needs a
	// person's yes the intent didn't need when it was made (a provider never
	// paid before, say).
	StateOpen: {StateReserved, StateCancelled, StateExpired, StateAwaitingApproval},
	// A reservation that never began execution can be released, cancelled
	// or outlived by the intent's expiry: nothing irreversible happened.
	StateReserved:  {StateExecuting, StateOpen, StateCancelled, StateExpired},
	StateExecuting: {StateCommitted, StateOpen, StateUnknown},
	StateUnknown:   {StateReconciling},
	// Reconciliation ends only in proof: a commitment (COMMITTED) or its
	// absence (OPEN). Unresolved means it stays RECONCILING.
	StateReconciling: {StateCommitted, StateOpen},
	StateCommitted:   {},
	StateCancelled:   {},
	StateExpired:     {},
}

// Terminal reports whether no further transition is valid.
func (s State) Terminal() bool {
	next, ok := intentTransitions[s]
	return ok && len(next) == 0
}

// Blocked reports whether a new commitment is forbidden because an attempt
// may already have committed.
func (s State) Blocked() bool {
	return s == StateExecuting || s == StateUnknown || s == StateReconciling
}

// CanTransition reports whether from → to is legal.
func CanTransition(from, to State) bool {
	for _, s := range intentTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// Commitment is what's known about money for an intent, separately from
// its lifecycle.
type Commitment string

const (
	CommitmentNone     Commitment = "NONE"
	CommitmentReserved Commitment = "RESERVED" // a hold is counted against authority
	CommitmentSettled  Commitment = "SETTLED"
	CommitmentUnknown  Commitment = "UNKNOWN"
	CommitmentReversed Commitment = "REVERSED"
)

// Fulfillment is what's known about the paid-for result.
type Fulfillment string

const (
	FulfillmentNone         Fulfillment = "NONE"
	FulfillmentFulfilled    Fulfillment = "FULFILLED"
	FulfillmentUnknown      Fulfillment = "UNKNOWN"
	FulfillmentNotFulfilled Fulfillment = "NOT_FULFILLED"
)

// ReservationState is one attempt's state.
type ReservationState string

const (
	ReservationReserved    ReservationState = "RESERVED"
	ReservationExecuting   ReservationState = "EXECUTING"
	ReservationUnknown     ReservationState = "UNKNOWN"
	ReservationReconciling ReservationState = "RECONCILING"
	ReservationCommitted   ReservationState = "COMMITTED"
	// ReservationReleased: ended with no commitment — before execution, or
	// proven by evidence afterwards.
	ReservationReleased ReservationState = "RELEASED"
	// ReservationExpired: its lease ran out before execution began.
	ReservationExpired ReservationState = "EXPIRED"
)

var reservationTransitions = map[ReservationState][]ReservationState{
	ReservationReserved:    {ReservationExecuting, ReservationReleased, ReservationExpired},
	ReservationExecuting:   {ReservationCommitted, ReservationReleased, ReservationUnknown},
	ReservationUnknown:     {ReservationReconciling},
	ReservationReconciling: {ReservationCommitted, ReservationReleased},
	ReservationCommitted:   {},
	ReservationReleased:    {},
	ReservationExpired:     {},
}

// Live reports whether a reservation still holds the intent: only one live
// reservation may exist per intent (enforced by a partial unique index).
func (s ReservationState) Live() bool {
	switch s {
	case ReservationReserved, ReservationExecuting, ReservationUnknown, ReservationReconciling:
		return true
	}
	return false
}

// LiveReservationStates are the states the uniqueness index covers.
var LiveReservationStates = []ReservationState{ReservationReserved, ReservationExecuting, ReservationUnknown, ReservationReconciling}

// CanReservationTransition reports whether from → to is legal.
func CanReservationTransition(from, to ReservationState) bool {
	for _, s := range reservationTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// ErrIllegalTransition is returned for a move the tables don't allow.
type ErrIllegalTransition struct {
	Kind     string
	From, To string
}

func (e *ErrIllegalTransition) Error() string {
	return fmt.Sprintf("econ: illegal %s transition %s -> %s", e.Kind, e.From, e.To)
}

// Outcome is what an executor or reconciliation established about an
// attempt.
type Outcome string

const (
	// OutcomeNoCommitment: proven that no money moved.
	OutcomeNoCommitment Outcome = "NO_COMMITMENT"
	// OutcomeSettled: money moved; the result isn't established.
	OutcomeSettled Outcome = "SETTLED"
	// OutcomeFulfilled: money moved and the result was received.
	OutcomeFulfilled Outcome = "FULFILLED"
	OutcomeReversed  Outcome = "REVERSED"
	// OutcomeUnknown: not enough evidence either way.
	OutcomeUnknown Outcome = "UNKNOWN"
)

// Semantics is when money moves relative to the work, per rail and
// provider. Settlement ordering is not universal.
type Semantics string

const (
	SemanticsPrepaidExact        Semantics = "PREPAID_EXACT"
	SemanticsMeteredCapture      Semantics = "METERED_CAPTURE"
	SemanticsDeferredChannel     Semantics = "DEFERRED_CHANNEL"
	SemanticsEscrowedConditional Semantics = "ESCROWED_CONDITIONAL"
)

// Valid reports whether s is a known settlement semantics.
func (s Semantics) Valid() bool {
	switch s {
	case SemanticsPrepaidExact, SemanticsMeteredCapture, SemanticsDeferredChannel, SemanticsEscrowedConditional:
		return true
	}
	return false
}
