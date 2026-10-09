package paymentintent

import "fmt"

// transitions is the authoritative allow-list of legal state changes — the
// same single-source-of-truth pattern internal/domain/intent uses. Nothing
// outside this package may set State directly; every change goes through
// Transition.
var transitions = map[State][]State{
	StateDraft: {
		StatePolicyEvaluating, StateCancelled, StateExpired,
	},
	StatePolicyEvaluating: {
		StateDenied, StateApprovalRequired, StateAuthorized, StateCancelled,
	},
	StateApprovalRequired: {
		StateAuthorized, StateCancelled, StateExpired, StateRevoked,
	},
	StateAuthorized: {
		StateCredentialPreparing, StateCancelled, StateRevoked,
	},
	StateCredentialPreparing: {
		StateReadyToExecute, StateProviderUnavailable, StateFailed,
	},
	StateReadyToExecute: {
		StateProcessing, StateCancelled, StateRevoked,
	},
	StateProcessing: {
		StateSucceeded, StateFailed, StateAuthenticationRequired,
		StateMerchantActionRequired, StateProviderUnavailable,
	},
	StateAuthenticationRequired: {
		StateProcessing, StateFailed, StateExpired,
	},
	StateMerchantActionRequired: {
		StateProcessing, StateFailed, StateCancelled,
	},
	StateProviderUnavailable: {
		StateProcessing, StateFailed, StateCancelled,
	},

	// Terminal states: no outbound edges.
	StateDenied:    {},
	StateSucceeded: {},
	StateFailed:    {},
	StateExpired:   {},
	StateRevoked:   {},
	StateCancelled: {},
}

// ErrIllegalTransition is returned by Transition when from -> to is not in
// the allow-list.
type ErrIllegalTransition struct {
	From State
	To   State
}

func (e *ErrIllegalTransition) Error() string {
	return fmt.Sprintf("paymentintent: illegal state transition %s -> %s", e.From, e.To)
}

// CanTransition reports whether moving from `from` to `to` is legal.
func CanTransition(from, to State) bool {
	allowed, ok := transitions[from]
	if !ok {
		return false
	}
	for _, s := range allowed {
		if s == to {
			return true
		}
	}
	return false
}

// Transition validates and returns the new state, or an *ErrIllegalTransition.
// It performs no I/O and has no side effects — callers (internal/app) are
// responsible for persisting the result and emitting the corresponding
// audit event atomically.
func Transition(from, to State) (State, error) {
	if !from.Valid() {
		return from, fmt.Errorf("paymentintent: unknown source state %q", from)
	}
	if !to.Valid() {
		return from, fmt.Errorf("paymentintent: unknown target state %q", to)
	}
	if !CanTransition(from, to) {
		return from, &ErrIllegalTransition{From: from, To: to}
	}
	return to, nil
}
