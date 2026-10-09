package paymentintent

// State is an AgenticPaymentIntent lifecycle state. Transitions are
// validated server-side by CanTransition/Transition, exactly like
// internal/domain/intent's PurchaseIntent — see state_machine.go.
type State string

const (
	StateDraft            State = "DRAFT"
	StatePolicyEvaluating State = "POLICY_EVALUATING"
	StateDenied           State = "DENIED"
	StateApprovalRequired State = "APPROVAL_REQUIRED"
	StateAuthorized       State = "AUTHORIZED"

	StateCredentialPreparing State = "CREDENTIAL_PREPARING"
	StateReadyToExecute      State = "READY_TO_EXECUTE"
	StateProcessing          State = "PROCESSING"
	StateSucceeded           State = "SUCCEEDED"

	StateFailed                 State = "FAILED"
	StateExpired                State = "EXPIRED"
	StateRevoked                State = "REVOKED"
	StateCancelled              State = "CANCELLED"
	StateAuthenticationRequired State = "AUTHENTICATION_REQUIRED"
	StateMerchantActionRequired State = "MERCHANT_ACTION_REQUIRED"
	StateProviderUnavailable    State = "PROVIDER_UNAVAILABLE"
)

// Terminal reports whether no further transition is ever valid from this
// state. A new AgenticPaymentIntent must be created instead of resurrecting
// one.
func (s State) Terminal() bool {
	switch s {
	case StateDenied, StateSucceeded, StateFailed, StateExpired, StateRevoked, StateCancelled:
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
