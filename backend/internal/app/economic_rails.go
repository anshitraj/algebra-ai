package app

import (
	"context"
	"encoding/json"

	"github.com/project-algebra/algebra/internal/domain/econ"
)

// SettlementStatus is what a payment rail can prove about a payment.
type SettlementStatus string

const (
	// SettlementSettled: the money moved; Amount and Transaction say how
	// much and where.
	SettlementSettled SettlementStatus = "SETTLED"
	// SettlementNotSettled: provably final — the payment can never land
	// (e.g. a Solana transaction whose blockhash expired unseen).
	SettlementNotSettled SettlementStatus = "NOT_SETTLED"
	// SettlementPending: submitted, not final yet.
	SettlementPending SettlementStatus = "PENDING"
	// SettlementUnknown: the rail can't say.
	SettlementUnknown SettlementStatus = "UNKNOWN"
)

// Settlement is a rail's answer, from its own records or chain state —
// never from what an executor claims.
type Settlement struct {
	Status      SettlementStatus `json:"status"`
	AmountMinor int64            `json:"amount_minor,omitempty"`
	Transaction string           `json:"transaction,omitempty"`
	Network     string           `json:"network,omitempty"`
	Asset       string           `json:"asset,omitempty"`
	Payer       string           `json:"payer,omitempty"`
	PayTo       string           `json:"pay_to,omitempty"`
	Detail      string           `json:"detail,omitempty"`
	// Test marks a sandbox settlement: never shown as a real payment.
	Test bool `json:"test,omitempty"`
}

// Rail is a payment rail Algebra coordinates above: x402 on Solana,
// later channels, cards or UPI. It proves settlement; it never decides
// economic state — the coordinator does, from what the rail proves.
type Rail interface {
	Name() string
	Settlement(ctx context.Context, ev econ.Evidence) (Settlement, error)
}

// PaymentAuthorizer is a rail through which Algebra itself releases
// payment authority for one reserved attempt — so the agent never holds a
// wallet key or payment credential. The returned payment is single-use
// and bound to the attempt; evidence records what was authorized.
type PaymentAuthorizer interface {
	Authorize(ctx context.Context, r *econ.Reservation, req PaymentRequest) (*PaymentAuthority, error)
}

// PaymentRequest is what a provider asked to be paid (for x402, the
// 402 response's payment requirements, passed through verbatim).
type PaymentRequest struct {
	Requirements json.RawMessage `json:"requirements"`
	// Resource is the URL being paid for, bound into the request hash.
	Resource string `json:"resource,omitempty"`
}

// PaymentAuthority is a signed, single-use payment the executor attaches
// to its request (for x402, the X-PAYMENT header value).
type PaymentAuthority struct {
	Header   string        `json:"header,omitempty"`
	Value    string        `json:"value"`
	Evidence econ.Evidence `json:"-"`
	// AmountMinor is exactly what was authorized.
	AmountMinor int64 `json:"amount_minor"`
}

// RecoveryStatus is what a provider can say about an operation.
type RecoveryStatus string

const (
	RecoveryFulfilled    RecoveryStatus = "FULFILLED"
	RecoveryNotFulfilled RecoveryStatus = "NOT_FULFILLED"
	RecoveryUnknown      RecoveryStatus = "UNKNOWN"
)

// Recovery is a provider's answer about one attempt.
type Recovery struct {
	Status      RecoveryStatus `json:"status"`
	ResultHash  string         `json:"result_hash,omitempty"`
	OperationID string         `json:"operation_id,omitempty"`
	Detail      string         `json:"detail,omitempty"`
}

// ProviderRecovery asks a provider what happened to an attempt: its status
// endpoint, idempotency key or result replay. Providers without any of
// these can't be recovered from — and the coordinator says so.
type ProviderRecovery interface {
	Recover(ctx context.Context, r econ.Reservation) (Recovery, error)
}

// ProviderCapability records what a provider supports for recovery — the
// properties that decide how safely an ambiguous attempt can be resolved.
type ProviderCapability struct {
	ID                string         `json:"id"`
	Capabilities      []string       `json:"capabilities"`
	Rail              string         `json:"rail"`
	Semantics         econ.Semantics `json:"settlement_semantics"`
	IdempotencyHeader string         `json:"idempotency_header,omitempty"`
	StatusEndpoint    string         `json:"status_endpoint,omitempty"`
	ReplayResult      bool           `json:"replay_result"`
	Refundable        bool           `json:"refundable"`
}
