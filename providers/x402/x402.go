// Package x402 holds the wire shapes of the x402 payment protocol that
// Algebra's rails need to read and write: a provider's 402 challenge
// (payment requirements), the X-PAYMENT header a payer sends back, and the
// X-PAYMENT-RESPONSE a provider answers with. It is deliberately not an x402
// implementation — no facilitator, no server — only parsing and encoding,
// so rails can consume the existing protocol instead of reinventing it.
//
// Both the v1 field names (maxAmountRequired, X-PAYMENT) and the v2 ones
// (amount, PAYMENT-SIGNATURE) are read.
package x402

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Requirements is one acceptable way to pay, from a 402 response's
// "accepts" list.
type Requirements struct {
	Scheme            string          `json:"scheme"`
	Network           string          `json:"network"`
	MaxAmountRequired string          `json:"maxAmountRequired,omitempty"`
	Amount            string          `json:"amount,omitempty"`
	Resource          string          `json:"resource,omitempty"`
	Description       string          `json:"description,omitempty"`
	MimeType          string          `json:"mimeType,omitempty"`
	PayTo             string          `json:"payTo"`
	MaxTimeoutSeconds int             `json:"maxTimeoutSeconds,omitempty"`
	Asset             string          `json:"asset"`
	Extra             json.RawMessage `json:"extra,omitempty"`
}

// AmountMinor is the price in the asset's smallest unit.
func (r Requirements) AmountMinor() (int64, error) {
	s := strings.TrimSpace(r.MaxAmountRequired)
	if s == "" {
		s = strings.TrimSpace(r.Amount)
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("x402: amount %q is not a positive integer", s)
	}
	return n, nil
}

// Challenge is the body of a 402 Payment Required response.
type Challenge struct {
	Version int            `json:"x402Version"`
	Error   string         `json:"error,omitempty"`
	Accepts []Requirements `json:"accepts"`
}

// ErrNoMatch: the provider offers no payment option this rail can use.
var ErrNoMatch = errors.New("x402: the provider accepts no payment option this rail supports")

// Select parses raw — a full challenge or a single requirements object —
// and returns the first option matching scheme and one of networks.
func Select(raw json.RawMessage, scheme string, networks ...string) (Requirements, error) {
	var c Challenge
	if err := json.Unmarshal(raw, &c); err == nil && len(c.Accepts) > 0 {
		for _, r := range c.Accepts {
			if matches(r, scheme, networks) {
				return r, nil
			}
		}
		return Requirements{}, ErrNoMatch
	}
	var one Requirements
	if err := json.Unmarshal(raw, &one); err != nil || one.Scheme == "" {
		return Requirements{}, errors.New("x402: payment requirements are not a 402 challenge or a requirements object")
	}
	if !matches(one, scheme, networks) {
		return Requirements{}, ErrNoMatch
	}
	return one, nil
}

func matches(r Requirements, scheme string, networks []string) bool {
	if !strings.EqualFold(r.Scheme, scheme) {
		return false
	}
	for _, n := range networks {
		if strings.EqualFold(r.Network, n) {
			return true
		}
	}
	return false
}

// PaymentPayload is what the X-PAYMENT header carries, base64-encoded.
type PaymentPayload struct {
	Version int             `json:"x402Version"`
	Scheme  string          `json:"scheme"`
	Network string          `json:"network"`
	Payload json.RawMessage `json:"payload"`
}

// Encode renders the payload as an X-PAYMENT header value.
func (p PaymentPayload) Encode() (string, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// DecodePayment parses an X-PAYMENT header value.
func DecodePayment(header string) (PaymentPayload, error) {
	var p PaymentPayload
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(header))
	if err != nil {
		return p, errors.New("x402: payment header is not base64")
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return p, errors.New("x402: payment header is not a payment payload")
	}
	return p, nil
}

// SettleResponse is what X-PAYMENT-RESPONSE carries, base64-encoded.
type SettleResponse struct {
	Success     bool   `json:"success"`
	Transaction string `json:"transaction"`
	Network     string `json:"network"`
	Payer       string `json:"payer,omitempty"`
	ErrorReason string `json:"errorReason,omitempty"`
}

func (s SettleResponse) Encode() string {
	b, _ := json.Marshal(s)
	return base64.StdEncoding.EncodeToString(b)
}

// DecodeSettle parses an X-PAYMENT-RESPONSE header value.
func DecodeSettle(header string) (SettleResponse, error) {
	var s SettleResponse
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(header))
	if err != nil {
		return s, errors.New("x402: payment response header is not base64")
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, errors.New("x402: payment response header is not a settle response")
	}
	return s, nil
}

// Header names, v1 and v2.
const (
	HeaderPayment         = "X-PAYMENT"
	HeaderPaymentResponse = "X-PAYMENT-RESPONSE"
	HeaderPaymentV2       = "PAYMENT-SIGNATURE"
	HeaderResponseV2      = "PAYMENT-RESPONSE"
)
