package econ

import (
	"fmt"
	"time"
)

// Reservation is one executor's exclusive right to attempt an intent's
// irreversible commitment. At most one live reservation exists per intent.
type Reservation struct {
	ID              string `json:"id"`
	IntentID        string `json:"intent_id"`
	ExecutorAgentID string `json:"executor_agent_id"`
	// ExecutorPassID is the Spend Pass whose authority this attempt uses.
	ExecutorPassID string `json:"executor_pass_id,omitempty"`
	Attempt        int    `json:"attempt"`

	State ReservationState `json:"state"`
	// HoldMinor is what's counted against the executor's pass until the
	// attempt settles or is released.
	HoldMinor  int64     `json:"hold_minor"`
	ProviderID string    `json:"provider_id,omitempty"`
	Rail       string    `json:"rail,omitempty"`
	QuoteMinor int64     `json:"quote_minor,omitempty"`
	Semantics  Semantics `json:"settlement_semantics,omitempty"`
	// IdempotencyKey is what the executor sends the provider, so a retry of
	// this attempt is recognisable by the provider too.
	IdempotencyKey string `json:"idempotency_key"`
	PolicyVersion  string `json:"policy_version,omitempty"`

	// LeaseExpiresAt bounds RESERVED: an executor that never begins loses
	// the reservation safely, since nothing irreversible was released.
	LeaseExpiresAt time.Time `json:"lease_expires_at"`
	// ExecutionDeadline bounds EXECUTING: past it without evidence the
	// attempt becomes UNKNOWN (never FAILED).
	ExecutionDeadline *time.Time `json:"execution_deadline,omitempty"`

	Evidence      Evidence `json:"evidence"`
	Outcome       Outcome  `json:"outcome,omitempty"`
	ReleaseReason string   `json:"release_reason,omitempty"`

	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// Evidence is what's observed about an attempt. Nothing here is a secret:
// never a key, token or credential — only identifiers and hashes.
type Evidence struct {
	Rail     string `json:"rail,omitempty"` // "solana-mainnet-usdc", "sandbox"
	Protocol string `json:"protocol,omitempty"`
	Scheme   string `json:"scheme,omitempty"`
	Network  string `json:"network,omitempty"`
	Asset    string `json:"asset,omitempty"`
	// PaymentID is a rail- or provider-level payment identifier.
	PaymentID string `json:"payment_id,omitempty"`
	// Transaction is an on-chain transaction signature, when there is one.
	Transaction string `json:"transaction,omitempty"`
	AmountMinor int64  `json:"amount_minor,omitempty"`
	Payer       string `json:"payer,omitempty"`
	PayTo       string `json:"pay_to,omitempty"`

	ProviderOperationID string `json:"provider_operation_id,omitempty"`
	// Channel is the payment channel a usage-based ("upto") payment escrowed
	// its ceiling in; the settled amount and the refund are read from it.
	Channel string `json:"channel,omitempty"`
	RequestHash         string `json:"request_hash,omitempty"`
	ResultHash          string `json:"result_hash,omitempty"`

	// AuthorityIssued: Algebra released payment authority for this attempt
	// (signed a payment). After that, "nothing happened" needs proof.
	AuthorityIssued   bool       `json:"authority_issued,omitempty"`
	AuthorityIssuedAt *time.Time `json:"authority_issued_at,omitempty"`
	// ValidUntilHeight is the chain height after which a signed but unseen
	// payment can provably never land (Solana: lastValidBlockHeight).
	ValidUntilHeight uint64 `json:"valid_until_height,omitempty"`
	// Test marks sandbox evidence: never presented as a real payment.
	Test bool `json:"test,omitempty"`
}

// Transition moves the reservation, or refuses an illegal move.
func (r *Reservation) Transition(to ReservationState, now time.Time) error {
	if !CanReservationTransition(r.State, to) {
		return &ErrIllegalTransition{Kind: "reservation", From: string(r.State), To: string(to)}
	}
	r.State = to
	r.UpdatedAt = now.UTC()
	if !to.Live() {
		t := now.UTC()
		r.FinishedAt = &t
	}
	return nil
}

// IdempotencyKeyFor derives the provider-facing key for an attempt.
func IdempotencyKeyFor(intentID string, attempt int) string {
	return fmt.Sprintf("%s-a%d", intentID, attempt)
}
