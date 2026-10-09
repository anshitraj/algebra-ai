package shared

import "encoding/json"

// SensitiveValue wraps a real secret — a scoped payment credential returned
// by a internal/domain/paymentprovider.Provider, for example — so it cannot
// accidentally serialize into a log line, an audit event, or an MCP/REST
// response built the ordinary way. String() and MarshalJSON() always
// redact; audit.Event's own forbidden-metadata-key deny-list is the
// belt-and-suspenders layer underneath this, in case a caller stores the
// revealed string under an unlabeled key.
type SensitiveValue struct {
	value string
}

// NewSensitiveValue wraps v. Construct this at the one point a real secret
// value first exists in the process — never pass a plain string further
// than that point.
func NewSensitiveValue(v string) SensitiveValue {
	return SensitiveValue{value: v}
}

func (s SensitiveValue) String() string { return "[redacted]" }

func (s SensitiveValue) MarshalJSON() ([]byte, error) {
	return json.Marshal("[redacted]")
}

// Reveal returns the real underlying value. Call this only at the single
// call site that must act on the secret — handing it to a payment
// provider's own execution call — never to log it, audit it, or include it
// in any response.
func (s SensitiveValue) Reveal() string { return s.value }

func (s SensitiveValue) IsZero() bool { return s.value == "" }
