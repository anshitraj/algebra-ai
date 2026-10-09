package routing

import (
	"errors"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
)

// PaymentStatus is what is known about the money for one attempt. It is
// reported by the rail from its own records or chain state, never taken from
// the provider's word or an agent's.
type PaymentStatus string

const (
	PaymentNotAttempted PaymentStatus = "NOT_ATTEMPTED"
	// PaymentAuthorized: Algebra released payment authority; settlement
	// hasn't been observed yet.
	PaymentAuthorized PaymentStatus = "AUTHORIZED"
	PaymentSettled    PaymentStatus = "SETTLED"
	// PaymentNotSettled: proven that no money moved.
	PaymentNotSettled PaymentStatus = "NOT_SETTLED"
	// PaymentUnknown: money may have moved. Never treated as failure.
	PaymentUnknown PaymentStatus = "UNKNOWN"
)

// Valid reports whether s is a known status.
func (s PaymentStatus) Valid() bool {
	switch s {
	case PaymentNotAttempted, PaymentAuthorized, PaymentSettled, PaymentNotSettled, PaymentUnknown:
		return true
	}
	return false
}

// FailureClass says where an attempt went wrong, in terms that decide what
// may happen next. The first four mean no money moved; the rest don't.
type FailureClass string

const (
	// FailNoRoute: no candidate survived the router.
	FailNoRoute FailureClass = "no_route"
	// FailPolicy: authority refused the attempt before any payment.
	FailPolicy FailureClass = "policy_denied"
	// FailQuote: the provider couldn't be priced before paying.
	FailQuote FailureClass = "quote_unavailable"
	// FailPayment: the payment couldn't be made or was rejected, with the
	// rail proving nothing settled.
	FailPayment FailureClass = "payment_failed"
	// FailProvider: the provider erred after payment authority was released.
	FailProvider FailureClass = "provider_error"
	// FailTimeout: no answer in time after payment authority was released.
	FailTimeout FailureClass = "timeout"
	// FailInvalid: a result arrived but failed validation.
	FailInvalid FailureClass = "invalid_response"
	// FailAmbiguous: the outcome can't be established yet; reconciliation
	// owns it.
	FailAmbiguous FailureClass = "outcome_unknown"
)

// Failure is why an attempt didn't deliver.
type Failure struct {
	Class   FailureClass `json:"class"`
	Message string       `json:"message,omitempty"`
}

// ExecutionResult is what happened to one attempt: the record routing learns
// from. It keeps hashes, identifiers and statuses, never the response body or
// anything secret.
type ExecutionResult struct {
	ID            string `json:"id"`
	IntentID      string `json:"intent_id"`
	ReservationID string `json:"reservation_id,omitempty"`
	Attempt       int    `json:"attempt,omitempty"`
	PlanID        string `json:"plan_id,omitempty"`
	// PlanRank is the step this attempt came from: 1 is the primary choice,
	// anything higher is a fallback.
	PlanRank int `json:"plan_rank,omitempty"`
	// Mode, PlanHash and QuoteHash say how the attempt was chosen and what it
	// was priced at, so a receipt can commit to them.
	Mode      Mode   `json:"mode,omitempty"`
	PlanHash  string `json:"plan_hash,omitempty"`
	QuoteHash string `json:"quote_hash,omitempty"`

	CandidateID   string        `json:"candidate_id"`
	QuoteID       string        `json:"quote_id,omitempty"`
	Provider      string        `json:"provider"`
	Capability    string        `json:"capability"`
	ExecutionType ExecutionType `json:"execution_type"`

	QuotedCostMinor int64 `json:"quoted_cost_minor"`
	ActualCostMinor int64 `json:"actual_cost_minor"`

	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at,omitzero"`
	// LatencyMS is wall-clock time from start to completion.
	LatencyMS  int64 `json:"latency_ms"`
	HTTPStatus int   `json:"http_status,omitempty"`

	Payment PaymentStatus `json:"payment"`
	// Delivery reuses the coordinator's fulfilment vocabulary.
	Delivery    econ.Fulfillment `json:"delivery"`
	Transaction string           `json:"transaction,omitempty"`
	Network     string           `json:"network,omitempty"`
	Asset       string           `json:"asset,omitempty"`

	RequestHash  string `json:"request_hash,omitempty"`
	ResponseHash string `json:"response_hash,omitempty"`

	Failure *Failure `json:"failure,omitempty"`
	// Test marks sandbox or test-network evidence, never presented as real
	// spend.
	Test bool `json:"test,omitempty"`
}

// Finish stamps the completion time and latency.
func (r *ExecutionResult) Finish(now time.Time) {
	r.CompletedAt = now.UTC()
	if !r.StartedAt.IsZero() && now.After(r.StartedAt) {
		r.LatencyMS = now.Sub(r.StartedAt).Milliseconds()
	}
}

// Succeeded reports a verified delivery: the work was delivered and nothing
// went wrong on the way. It is the success the router's history counts.
func (r ExecutionResult) Succeeded() bool {
	return r.Failure == nil && r.Delivery == econ.FulfillmentFulfilled &&
		(r.Payment == PaymentSettled || r.Payment == PaymentNotAttempted)
}

// Validate refuses records that contradict themselves, so a bug upstream
// can't write a cost next to "no payment was attempted", or "fulfilled" next
// to a failure. A delivered result whose payment isn't confirmed yet is a real
// state (the response arrived before the chain showed the transaction), so
// that combination is allowed; it just doesn't count as a success until the
// payment settles.
func (r ExecutionResult) Validate() error {
	switch {
	case r.IntentID == "" || r.CandidateID == "" || r.Provider == "":
		return errors.New("a result names its intent, candidate and provider")
	case !r.ExecutionType.Valid():
		return errors.New("a result needs a known execution type")
	case r.StartedAt.IsZero():
		return errors.New("a result needs its start time")
	case !r.CompletedAt.IsZero() && r.CompletedAt.Before(r.StartedAt):
		return errors.New("a result can't complete before it started")
	case !r.Payment.Valid():
		return errors.New("a result needs a known payment status")
	case r.Delivery != econ.FulfillmentNone && r.Delivery != econ.FulfillmentFulfilled &&
		r.Delivery != econ.FulfillmentUnknown && r.Delivery != econ.FulfillmentNotFulfilled:
		return errors.New("a result needs a known delivery status")
	case r.QuotedCostMinor < 0 || r.ActualCostMinor < 0 || r.LatencyMS < 0:
		return errors.New("costs and latency can't be negative")
	case r.Payment == PaymentNotAttempted && r.ActualCostMinor != 0:
		return errors.New("a result can't have a cost when no payment was attempted")
	case r.Failure != nil && r.Delivery == econ.FulfillmentFulfilled:
		return errors.New("a result can't be both fulfilled and failed")
	}
	return nil
}
