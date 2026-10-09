package intent

import "fmt"

// transitions is the authoritative allow-list of legal state changes. This
// is the ONLY place that decides whether a PurchaseIntent may move from one
// state to another — application services must call Transition rather than
// setting State directly, and nothing else in the codebase is allowed to
// bypass it. That is what makes "an agent cannot arbitrarily jump states" an
// enforced property rather than a convention.
var transitions = map[State][]State{
	StateDraft: {
		StateDiscovering, StateCancelled, StateExpired,
	},
	StateDiscovering: {
		StateQuoted, StateFailed, StateCancelled, StateExpired,
	},
	StateQuoted: {
		StatePolicyCheck, StateCancelled, StateExpired,
	},
	StatePolicyCheck: {
		StatePolicyRejected, StateApprovalRequired, StateApproved, StateCancelled,
	},
	StateApprovalRequired: {
		StateApproved, StateCancelled, StateExpired,
	},
	StateApproved: {
		StateExecuting, StateReapprovalRequired, StateCancelled,
	},
	StateReapprovalRequired: {
		StateApproved, StateCancelled, StateExpired,
	},
	StateExecuting: {
		StateAuthenticationRequired, StateSucceeded, StateFailed,
		StateMerchantInterventionRequired, StateUserInterventionRequired,
		StatePartiallyCompleted,
	},
	StateAuthenticationRequired: {
		StateSucceeded, StateFailed, StateExpired,
	},
	StateMerchantInterventionRequired: {
		StateExecuting, StateFailed, StateCancelled,
	},
	StateUserInterventionRequired: {
		StateExecuting, StateFailed, StateCancelled,
	},

	// Terminal states: no outbound edges.
	StatePolicyRejected:     {},
	StateSucceeded:          {},
	StateFailed:             {},
	StateCancelled:          {},
	StateExpired:            {},
	StatePartiallyCompleted: {},
}

// ErrIllegalTransition is returned by Transition when from -> to is not in
// the allow-list.
type ErrIllegalTransition struct {
	From State
	To   State
}

func (e *ErrIllegalTransition) Error() string {
	return fmt.Sprintf("intent: illegal state transition %s -> %s", e.From, e.To)
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
		return from, fmt.Errorf("intent: unknown source state %q", from)
	}
	if !to.Valid() {
		return from, fmt.Errorf("intent: unknown target state %q", to)
	}
	if !CanTransition(from, to) {
		return from, &ErrIllegalTransition{From: from, To: to}
	}
	return to, nil
}
