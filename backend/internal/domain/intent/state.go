package intent

// State is a PurchaseIntent lifecycle state. Transitions between states are
// validated server-side by CanTransition/Transition — an agent (or any
// caller) cannot jump states arbitrarily; see state_machine.go.
type State string

const (
	StateDraft       State = "DRAFT"
	StateDiscovering State = "DISCOVERING"
	StateQuoted      State = "QUOTED"
	StatePolicyCheck State = "POLICY_CHECK"

	StatePolicyRejected     State = "POLICY_REJECTED"
	StateApprovalRequired   State = "APPROVAL_REQUIRED"
	StateApproved           State = "APPROVED"
	StateReapprovalRequired State = "REAPPROVAL_REQUIRED"

	StateExecuting              State = "EXECUTING"
	StateAuthenticationRequired State = "AUTHENTICATION_REQUIRED"
	StateSucceeded              State = "SUCCEEDED"

	StateFailed                       State = "FAILED"
	StateCancelled                    State = "CANCELLED"
	StateExpired                      State = "EXPIRED"
	StatePartiallyCompleted           State = "PARTIALLY_COMPLETED"
	StateMerchantInterventionRequired State = "MERCHANT_INTERVENTION_REQUIRED"
	StateUserInterventionRequired     State = "USER_INTERVENTION_REQUIRED"
)

// Terminal reports whether no further transition is ever valid from this
// state. A new PurchaseIntent must be created instead of resurrecting one.
func (s State) Terminal() bool {
	switch s {
	case StatePolicyRejected, StateSucceeded, StateFailed, StateCancelled,
		StateExpired, StatePartiallyCompleted:
		return true
	default:
		return false
	}
}

// Valid reports whether s is a known state.
func (s State) Valid() bool {
	_, ok := transitions[s]
	return ok
}
