// Package paymentprovider defines the interface every payment rail plugs
// into Algebra through — a card network's agentic-commerce program, a
// processor's agent-wallet product, a stablecoin rail, or (in this build)
// the deterministic DemoProvider. This is the piece that did not exist
// anywhere in Algebra before: internal/domain/merchant.Connector's
// ExecuteCheckout never receives a credential, only a cart handle and an
// address — actual money movement had no abstraction of its own.
//
// A Provider must never advertise a Capabilities flag it doesn't actually
// implement — the same discipline merchant.Connector's doc comment states
// for commerce connectors, applied here to payment execution. No method on
// this interface ever receives or returns a raw PAN, CVV, wallet private
// key, or seed phrase; ScopedCredential.Value is the one place a real
// secret may flow, and it is wrapped in shared.SensitiveValue specifically
// so it cannot accidentally serialize into a log or audit event.
package paymentprovider

import (
	"time"

	"github.com/project-algebra/algebra/internal/domain/payment"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// Mode labels which kind of payment rail a Provider backs.
type Mode string

const (
	ModeIssuerNative     Mode = "ISSUER_NATIVE"     // a card/wallet company's own issuer/processor API
	ModeNetworkToken     Mode = "NETWORK_TOKEN"     // Visa Intelligent Commerce, Mastercard Agent Pay
	ModeProcessorAgentic Mode = "PROCESSOR_AGENTIC" // Stripe Shared Payment Tokens, Razorpay/Cashfree agentic payments
	ModeStablecoin       Mode = "STABLECOIN"        // non-custodial wallet rail
	ModeMachinePayment   Mode = "MACHINE_PAYMENT"   // x402 and similar
	ModeMerchantManaged  Mode = "MERCHANT_MANAGED"  // merchant checkout owns payment
	ModeHandoff          Mode = "HANDOFF"           // Algebra cannot safely execute; hands off to the user
	ModeDemo             Mode = "DEMO"              // deterministic, in-memory — see providers/paymentdemo
)

// Capabilities describes what a Provider can actually do, so callers
// (internal/app's capability resolver, tenant-facing status endpoints)
// never have to guess or silently pick an unavailable rail.
type Capabilities struct {
	Mode Mode `json:"mode"`

	CanDelegate              bool `json:"can_delegate"`
	CanIssueScopedCredential bool `json:"can_issue_scoped_credential"`
	CanExecute               bool `json:"can_execute"`
	CanRefund                bool `json:"can_refund"`

	RequiresUserPresence bool `json:"requires_user_presence"`
	SupportsPasskey      bool `json:"supports_passkey"`

	SupportsCard            bool `json:"supports_card"`
	SupportsUPI             bool `json:"supports_upi"`
	SupportsStablecoin      bool `json:"supports_stablecoin"`
	SupportsRecurring       bool `json:"supports_recurring"`
	SupportsMerchantBinding bool `json:"supports_merchant_binding"`
	SupportsAmountBinding   bool `json:"supports_amount_binding"`
}

// RegisterSourceRequest tells a Provider about an existing
// internal/domain/payment.PaymentSource so it can later be used in an
// agentic payment. This is not a second place payment sources are created —
// the source itself still comes from the existing POST /api/v1/payment-sources
// flow (payment.CardVaultProvider); this only lets a specific execution rail
// acknowledge and attach its own opaque reference to it.
type RegisterSourceRequest struct {
	UserID string
	Alias  string
	Type   payment.SourceType
}

// SourceRegistration is a Provider's own opaque handle for a payment source —
// never the credential itself.
type SourceRegistration struct {
	ProviderRef  string
	Capabilities Capabilities
}

// DelegatedAuthorizationRequest asks a Provider to bind an agent's spending
// authority to a specific merchant, maximum amount, and expiry — the
// mandate's "give agents spending authority, not payment credentials" made
// concrete.
type DelegatedAuthorizationRequest struct {
	UserID, AgentID     string
	SourceRef           string // from SourceRegistration.ProviderRef
	Merchant            string
	MerchantDomain      string
	MaxAmountMinorUnits int64
	Currency            string
	ExpiresAt           time.Time
}

type DelegatedAuthorization struct {
	AuthorizationRef string
	ExpiresAt        time.Time
}

// AuthenticationRequest asks a Provider to perform (or report the need for)
// strong customer authentication — a distinct trust boundary from Algebra's
// own approval workflow, see internal/domain/payment's ChallengeMechanism
// doc comment.
type AuthenticationRequest struct {
	AuthorizationRef string
	Mechanism        payment.ChallengeMechanism // preferred; the provider may require a different one
}

type AuthenticationResult struct {
	Challenge *payment.AuthorizationChallenge
	Satisfied bool // true if no further user action is needed
}

// ScopedCredentialRequest asks a Provider to mint a credential scoped to
// exactly one authorized spend — merchant-bound, amount-bound, single-use
// where the rail supports it.
type ScopedCredentialRequest struct {
	AuthorizationRef string
	AmountMinorUnits int64
	Currency         string
	IdempotencyKey   string
}

// ScopedCredential's Value is the one place a real credential value may
// flow through this package. shared.SensitiveValue makes it structurally
// unable to appear in a log line, an audit event, or an MCP/REST response
// built the ordinary way — see shared.SensitiveValue's doc comment.
type ScopedCredential struct {
	CredentialRef string
	Value         shared.SensitiveValue
	ExpiresAt     time.Time
}

// PaymentStatus is the outcome of an execution attempt.
type PaymentStatus string

const (
	PaymentSucceeded              PaymentStatus = "SUCCEEDED"
	PaymentFailed                 PaymentStatus = "FAILED"
	PaymentDeclined               PaymentStatus = "DECLINED"
	PaymentAuthenticationRequired PaymentStatus = "AUTHENTICATION_REQUIRED"
	PaymentProviderUnavailable    PaymentStatus = "PROVIDER_UNAVAILABLE"
)

// ExecutePaymentRequest carries a credential REFERENCE, never the credential
// value itself — CredentialRef is ScopedCredential.CredentialRef, an opaque
// handle the provider resolves internally.
type ExecutePaymentRequest struct {
	CredentialRef    string
	Merchant         string
	MerchantDomain   string
	AmountMinorUnits int64
	Currency         string
	IdempotencyKey   string

	// Metadata carries small, non-sensitive extra context. providers/paymentdemo
	// reads an optional "demo_scenario" key here to select a deterministic
	// success/decline/outage/etc. test scenario — see its doc comment. Real
	// providers ignore keys they don't recognize.
	Metadata map[string]string
}

// PaymentResult is the authoritative outcome of an execution or status
// check — never fabricated locally; Algebra reports exactly what a provider
// confirmed, the same discipline internal/app/order_service.go already
// applies to merchant checkout results.
type PaymentResult struct {
	Status                PaymentStatus
	ProviderTransactionID string
	FinalAmountMinorUnits int64
	FinalCurrency         string
	Reason                string
	Challenge             *payment.AuthorizationChallenge
	OccurredAt            time.Time
}

type RefundRequest struct {
	ProviderTransactionID string
	AmountMinorUnits      int64
	Currency              string
	Reason                string
}
