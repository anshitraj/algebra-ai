package receipt

import (
	"crypto/ed25519"
	"encoding/json"
	"strings"
)

// IntentType is the JWS "typ" of an Intent Receipt (v2): signed evidence
// of what Algebra observed while coordinating one economic intent. It is
// signed with the same key, and verified with the same JWKS, as spend
// receipts.
//
// An Intent Receipt proves what Algebra authorized and observed. It does
// not prove the provider's data is true, that a recommendation was right,
// or that anything happened that Algebra couldn't observe.
const IntentType = "algebra-intent-receipt+jwt"

// IntentClaims are what an Intent Receipt asserts, bound end to end:
// principal → intent → authority → reservation → executor → provider →
// execution → payment → result → reconciliation → final state.
type IntentClaims struct {
	Issuer   string `json:"iss"`
	ID       string `json:"jti"`
	IssuedAt int64  `json:"iat"`
	// Subject is the principal's pseudonym, never an ID or email.
	Subject string `json:"sub"`
	Version int    `json:"v"`

	Intent       IntentRef       `json:"intent"`
	Authority    AuthorityRef    `json:"authority"`
	Reservation  ReservationRef  `json:"reservation"`
	Provider     ProviderRef     `json:"provider"`
	Execution    ExecutionRef    `json:"execution"`
	Settlement   *SettlementRef  `json:"settlement,omitempty"`
	Coordination CoordinationRef `json:"coordination"`
	Final        FinalState      `json:"final_state"`
	// Test marks sandbox evidence: the receipt is genuine, but no real
	// money moved.
	Test bool `json:"test,omitempty"`
}

type IntentRef struct {
	ID         string `json:"id"`
	Hash       string `json:"hash"`
	Capability string `json:"capability"`
	EffectKey  string `json:"effect_key"`
	Quantity   int    `json:"quantity"`
	Window     string `json:"window"`
	BudgetMax  Money  `json:"budget_max"`
}

type AuthorityRef struct {
	PassID        string `json:"spend_pass_id,omitempty"`
	PolicyVersion string `json:"policy_version,omitempty"`
	// Method: "policy" (within limits) or "human" (the person approved).
	Method Method `json:"method"`
}

type ReservationRef struct {
	ID       string `json:"id"`
	Executor Agent  `json:"executor"`
	Attempt  int    `json:"attempt"`
}

type ProviderRef struct {
	ID        string `json:"id"`
	Quote     *Money `json:"quote,omitempty"`
	Semantics string `json:"settlement_semantics,omitempty"`
}

type ExecutionRef struct {
	Protocol            string `json:"protocol,omitempty"`
	Scheme              string `json:"scheme,omitempty"`
	RequestHash         string `json:"request_hash,omitempty"`
	ResultHash          string `json:"result_hash,omitempty"`
	ProviderOperationID string `json:"provider_operation_id,omitempty"`
	// Status is what's proven about the result: "fulfilled" or
	// "result_unknown".
	Status string `json:"status"`
}

type SettlementRef struct {
	Rail        string `json:"rail"`
	Network     string `json:"network,omitempty"`
	Asset       string `json:"asset,omitempty"`
	Amount      Money  `json:"amount"`
	Transaction string `json:"transaction,omitempty"`
	PaymentID   string `json:"payment_id,omitempty"`
	Payer       string `json:"payer,omitempty"`
	PayTo       string `json:"pay_to,omitempty"`
}

type CoordinationRef struct {
	Attempts                       int  `json:"attempts"`
	DuplicateCommitAttemptsBlocked int  `json:"duplicate_commit_attempts_blocked"`
	ReconciliationRequired         bool `json:"reconciliation_required"`
}

type FinalState struct {
	Lifecycle   string `json:"lifecycle"`
	Commitment  string `json:"commitment"`
	Fulfillment string `json:"fulfillment"`
}

// SignIntent returns the compact JWS for an Intent Receipt.
func (s *Signer) SignIntent(c IntentClaims) (string, error) {
	c.Version = 2
	return s.signTyped(IntentType, c)
}

func (s *Signer) signTyped(typ string, claims any) (string, error) {
	h, err := json.Marshal(header{Alg: "EdDSA", Typ: typ, Kid: s.kid})
	if err != nil {
		return "", err
	}
	p, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	input := b64(h) + "." + b64(p)
	return input + "." + b64(ed25519.Sign(s.priv, []byte(input))), nil
}

// VerifyIntent checks an Intent Receipt against a key set.
func VerifyIntent(jws string, keys JWKS) (*IntentClaims, error) {
	payload, err := verifyTyped(jws, keys, IntentType)
	if err != nil {
		return nil, err
	}
	var c IntentClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, ErrInvalid
	}
	return &c, nil
}

// Kind reports which receipt type a JWS claims to be, without verifying.
func Kind(jws string) string {
	parts := strings.Split(strings.TrimSpace(jws), ".")
	if len(parts) != 3 {
		return ""
	}
	hb, err := unb64(parts[0])
	if err != nil {
		return ""
	}
	var h header
	if json.Unmarshal(hb, &h) != nil {
		return ""
	}
	return h.Typ
}

func verifyTyped(jws string, keys JWKS, typ string) ([]byte, error) {
	parts := strings.Split(strings.TrimSpace(jws), ".")
	if len(parts) != 3 {
		return nil, ErrInvalid
	}
	hb, err := unb64(parts[0])
	if err != nil {
		return nil, ErrInvalid
	}
	var h header
	if err := json.Unmarshal(hb, &h); err != nil || h.Alg != "EdDSA" || h.Typ != typ {
		return nil, ErrInvalid
	}
	sig, err := unb64(parts[2])
	if err != nil {
		return nil, ErrInvalid
	}
	var pub ed25519.PublicKey
	for _, k := range keys.Keys {
		if k.Kid == h.Kid && k.Kty == "OKP" && k.Crv == "Ed25519" {
			if raw, err := unb64(k.X); err == nil && len(raw) == ed25519.PublicKeySize {
				pub = raw
			}
		}
	}
	if pub == nil || !ed25519.Verify(pub, []byte(parts[0]+"."+parts[1]), sig) {
		return nil, ErrInvalid
	}
	pb, err := unb64(parts[1])
	if err != nil {
		return nil, ErrInvalid
	}
	return pb, nil
}
