// Package receipt issues and verifies Algebra spend receipts: signed proof
// that a person authorized exactly this purchase — this store, these items,
// this amount — through their guardrails, and whether a rule or the person
// themselves approved it.
//
// A receipt is a compact JWS (RFC 7515) signed with Ed25519 ("EdDSA",
// RFC 8037). Anyone can verify it against Algebra's published key set
// (/.well-known/jwks.json) without calling Algebra, so a store, a payment
// company or a dispute team can check an agent's purchase on its own. It
// carries no personal data: the person appears only as a pseudonym that is
// stable per person, so receipts from the same person can be linked
// without revealing who they are.
package receipt

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/hkdf"
)

// Type is the JWS "typ" header of every receipt.
const Type = "algebra-spend-receipt+jwt"

// Claims is what a receipt asserts.
type Claims struct {
	Issuer   string `json:"iss"`
	ID       string `json:"jti"`
	IssuedAt int64  `json:"iat"`
	// Subject is the person's pseudonym (Signer.Pseudonym), never an ID or
	// email.
	Subject string `json:"sub"`

	Agent Agent `json:"agent"`
	// Pass is the Spend Pass the agent spent under, if any.
	Pass string `json:"pass,omitempty"`

	Merchant        string `json:"merchant"`
	MerchantOrderID string `json:"merchant_order_id"`
	Amount          Money  `json:"amount"`
	Items           []Item `json:"items"`
	// ItemsHash is the approval's canonical hash of merchant, items, unit
	// prices, currency and payment alias — what the person approved.
	ItemsHash string `json:"items_hash"`

	Authorization Authorization `json:"authorization"`
	// Test marks a simulated order (demo checkout, test store): the receipt
	// is genuine, the purchase moved no money.
	Test bool `json:"test,omitempty"`
}

type Agent struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Client is who the agent runs as: "algebra-console", "spend-pass:claude", …
	Client string `json:"client"`
}

type Money struct {
	MinorUnits int64  `json:"minor_units"`
	Currency   string `json:"currency"`
}

type Item struct {
	Name           string `json:"name"`
	Quantity       int    `json:"quantity"`
	UnitMinorUnits int64  `json:"unit_minor_units"`
}

// Method is who said yes.
type Method string

const (
	// MethodPolicy: under the person's own limits, so policy approved it
	// without asking.
	MethodPolicy Method = "policy"
	// MethodHuman: the person approved this specific purchase themselves.
	MethodHuman Method = "human"
)

type Authorization struct {
	Method        Method   `json:"method"`
	ApprovedAt    int64    `json:"approved_at"`
	PolicyVersion string   `json:"policy_version,omitempty"`
	ReasonCodes   []string `json:"reason_codes,omitempty"`
}

// JWK is one public key in RFC 7517 form (OKP / Ed25519, RFC 8037).
type JWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
}

// JWKS is the key set published at /.well-known/jwks.json.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// Signer holds Algebra's receipt signing key.
type Signer struct {
	priv      ed25519.PrivateKey
	pub       ed25519.PublicKey
	kid       string
	pseudoKey []byte
}

// NewSigner derives the signing key from the deployment's master key, so
// no new secret needs storing: the same master key always yields the same
// receipt key (and so keeps old receipts verifiable). Bump the version in
// the info string to rotate.
func NewSigner(masterKey []byte) (*Signer, error) {
	if len(masterKey) < 32 {
		return nil, errors.New("receipt: master key too short")
	}
	seed := make([]byte, ed25519.SeedSize)
	if _, err := io.ReadFull(hkdf.New(sha256.New, masterKey, nil, []byte("algebra/spend-receipts/ed25519/v1")), seed); err != nil {
		return nil, fmt.Errorf("receipt: deriving signing key: %w", err)
	}
	pseudoKey := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, masterKey, nil, []byte("algebra/spend-receipts/pseudonym/v1")), pseudoKey); err != nil {
		return nil, fmt.Errorf("receipt: deriving pseudonym key: %w", err)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)
	sum := sha256.Sum256(pub)
	return &Signer{priv: priv, pub: pub, kid: "algebra-receipts-" + hex.EncodeToString(sum[:6]), pseudoKey: pseudoKey}, nil
}

// Pseudonym is a person's stable, non-reversible receipt subject.
func (s *Signer) Pseudonym(userID string) string {
	mac := hmac.New(sha256.New, s.pseudoKey)
	mac.Write([]byte(userID))
	return "person_" + hex.EncodeToString(mac.Sum(nil)[:12])
}

// JWKS is the public key set to publish.
func (s *Signer) JWKS() JWKS {
	return JWKS{Keys: []JWK{{Kty: "OKP", Crv: "Ed25519", X: b64(s.pub), Kid: s.kid, Use: "sig", Alg: "EdDSA"}}}
}

type header struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
	Kid string `json:"kid"`
}

// Sign returns the compact JWS for claims.
func (s *Signer) Sign(c Claims) (string, error) {
	h, err := json.Marshal(header{Alg: "EdDSA", Typ: Type, Kid: s.kid})
	if err != nil {
		return "", err
	}
	p, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	input := b64(h) + "." + b64(p)
	return input + "." + b64(ed25519.Sign(s.priv, []byte(input))), nil
}

// ErrInvalid means the text isn't a receipt this key set signed.
var ErrInvalid = errors.New("not a valid Algebra receipt")

// Verify checks a compact JWS against a key set and returns its claims.
func Verify(jws string, keys JWKS) (*Claims, error) {
	parts := strings.Split(strings.TrimSpace(jws), ".")
	if len(parts) != 3 {
		return nil, ErrInvalid
	}
	hb, err := unb64(parts[0])
	if err != nil {
		return nil, ErrInvalid
	}
	var h header
	if err := json.Unmarshal(hb, &h); err != nil || h.Alg != "EdDSA" || h.Typ != Type {
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
	var c Claims
	if err := json.Unmarshal(pb, &c); err != nil {
		return nil, ErrInvalid
	}
	return &c, nil
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func unb64(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }
