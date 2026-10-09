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
	"net/http"
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
	Version int    `json:"x402Version"`
	Error   string `json:"error,omitempty"`
	// Resource is v2's description of what is being paid for; v1 puts it in
	// each option instead.
	Resource json.RawMessage `json:"resource,omitempty"`
	Accepts  []Requirements  `json:"accepts"`
	// Raw holds each accepts entry exactly as the provider sent it, set by
	// ParseChallenge, so an option can be hashed and paid verbatim rather than
	// re-encoded from the fields this package happens to know.
	Raw []json.RawMessage `json:"-"`
}

// HeaderPaymentRequiredV2 is where x402 v2 carries the challenge, as base64
// JSON; a v2 body may be empty or a human-readable page.
const HeaderPaymentRequiredV2 = "PAYMENT-REQUIRED"

// ParseChallenge reads the challenge out of a 402 response. Whichever of the
// v2 header and the v1 JSON body carries at least one option wins, the header
// first. A provider that sends neither is not speaking x402.
func ParseChallenge(header http.Header, body []byte) (Challenge, error) {
	if h := strings.TrimSpace(header.Get(HeaderPaymentRequiredV2)); h != "" {
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			b, err := enc.DecodeString(h)
			if err != nil {
				continue
			}
			if c, ok := decodeChallenge(b); ok {
				return c, nil
			}
		}
	}
	if c, ok := decodeChallenge(body); ok {
		return c, nil
	}
	return Challenge{}, errors.New("x402: the 402 response carries no payment requirements")
}

func decodeChallenge(b []byte) (Challenge, bool) {
	var c Challenge
	if json.Unmarshal(b, &c) != nil || len(c.Accepts) == 0 {
		return Challenge{}, false
	}
	var raw struct {
		Accepts []json.RawMessage `json:"accepts"`
	}
	if json.Unmarshal(b, &raw) == nil && len(raw.Accepts) == len(c.Accepts) {
		c.Raw = raw.Accepts
	}
	return c, true
}

// Single returns a challenge holding only option i, keeping the protocol
// version, so a rail can pay exactly that option and still know which
// version of the protocol is in play. The option is the provider's own bytes
// when they are known.
func (c Challenge) Single(i int) (json.RawMessage, error) {
	if i < 0 || i >= len(c.Accepts) {
		return nil, errors.New("x402: no such payment option")
	}
	var opt json.RawMessage
	if i < len(c.Raw) {
		opt = c.Raw[i]
	} else {
		b, err := json.Marshal(c.Accepts[i])
		if err != nil {
			return nil, err
		}
		opt = b
	}
	return json.Marshal(struct {
		Version  int               `json:"x402Version"`
		Resource json.RawMessage   `json:"resource,omitempty"`
		Accepts  []json.RawMessage `json:"accepts"`
	}{c.Version, c.Resource, []json.RawMessage{opt}})
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

// ExtraField reads one string from a requirement's "extra" object, such as
// the sponsor's feePayer on Solana.
func (r Requirements) ExtraField(name string) string {
	var m map[string]any
	if json.Unmarshal(r.Extra, &m) != nil {
		return ""
	}
	v, _ := m[name].(string)
	return v
}

// Selected is the option chosen from a challenge, with what a payer needs
// alongside it: the provider's own bytes for it, the protocol version and the
// resource being paid for.
type Selected struct {
	Requirements Requirements
	// Raw is the option exactly as the provider sent it, which v2 echoes back
	// in the payment as "accepted".
	Raw      json.RawMessage
	Version  int
	Resource json.RawMessage
}

// SelectRaw is Select that also returns the option's raw bytes, the version
// and the resource. raw may be a full challenge (as ParseChallenge's Single
// produces) or one requirements object, which is taken to be version 1.
func SelectRaw(raw json.RawMessage, scheme string, networks ...string) (Selected, error) {
	var c Challenge
	if err := json.Unmarshal(raw, &c); err == nil && len(c.Accepts) > 0 {
		var rawAccepts struct {
			Accepts []json.RawMessage `json:"accepts"`
		}
		_ = json.Unmarshal(raw, &rawAccepts)
		for i, r := range c.Accepts {
			if !matches(r, scheme, networks) {
				continue
			}
			sel := Selected{Requirements: r, Version: c.Version, Resource: c.Resource}
			if i < len(rawAccepts.Accepts) {
				sel.Raw = rawAccepts.Accepts[i]
			}
			if sel.Version == 0 {
				sel.Version = 1
			}
			return sel, nil
		}
		return Selected{}, ErrNoMatch
	}
	var one Requirements
	if err := json.Unmarshal(raw, &one); err != nil || one.Scheme == "" {
		return Selected{}, errors.New("x402: payment requirements are not a 402 challenge or a requirements object")
	}
	if !matches(one, scheme, networks) {
		return Selected{}, ErrNoMatch
	}
	return Selected{Requirements: one, Raw: raw, Version: 1}, nil
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
	Version int `json:"x402Version"`
	// Scheme and Network name the option being paid in v1.
	Scheme  string `json:"scheme,omitempty"`
	Network string `json:"network,omitempty"`
	// Resource and Accepted are v2's: what is being paid for, and the exact
	// option from the provider's challenge that this payment satisfies.
	Resource json.RawMessage `json:"resource,omitempty"`
	Accepted json.RawMessage `json:"accepted,omitempty"`
	Payload  json.RawMessage `json:"payload"`
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
