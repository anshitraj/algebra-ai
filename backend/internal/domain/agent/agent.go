// Package agent models AgentIdentity: the scoped, revocable credential an AI
// agent uses to call Algebra. An agent is never handed a payment credential
// or a user's session — only a token that maps to a bounded permission set.
package agent

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"
)

// Permission is a scoped capability an AgentIdentity may hold. The mandate
// is explicit: "do not assume every connected agent deserves every
// capability" — every mutation path in internal/app checks one of these
// before doing anything.
type Permission string

const (
	PermShoppingRead         Permission = "shopping.read"
	PermShoppingCreateIntent Permission = "shopping.create_intent"
	PermShoppingExecute      Permission = "shopping.execute"
	PermPaymentsRequest      Permission = "payments.request"
	PermOrdersRead           Permission = "orders.read"
	PermProfilesRead         Permission = "profiles.read"
	PermPolicyRead           Permission = "policy.read"

	// PermPaymentsCreateIntent/PermPaymentsExecute scope the
	// AgenticPaymentIntent flow (internal/domain/paymentintent) — kept
	// distinct from the Shopping.* permissions above (which scope the
	// commerce/discovery flow, internal/domain/intent) so a tenant's agent
	// can be granted one without implicitly getting the other.
	PermPaymentsCreateIntent Permission = "payments.create_intent"
	PermPaymentsExecute      Permission = "payments.execute"

	// PermProfilesWrite scopes writing to a user's CommerceProfile
	// (internal/domain/commerceprofile) — a genuinely new agent capability,
	// kept separate from the read-only PermProfilesRead above. The
	// existing ShippingProfile/BillingProfile write path (StoreShipping/
	// StoreBilling) stays human-only regardless of this permission; it has
	// no MCP/agent path at all.
	PermProfilesWrite Permission = "profiles.write"
)

// AllPermissions is used for validation (rejecting unknown scopes on grant).
var AllPermissions = []Permission{
	PermShoppingRead, PermShoppingCreateIntent, PermShoppingExecute,
	PermPaymentsRequest, PermOrdersRead, PermProfilesRead, PermPolicyRead,
	PermPaymentsCreateIntent, PermPaymentsExecute, PermProfilesWrite,
}

func (p Permission) Valid() bool {
	for _, known := range AllPermissions {
		if p == known {
			return true
		}
	}
	return false
}

// Identity is an agent's identity and grant set. Permissions is the
// exhaustive list of what this agent may do — nothing is implied or
// inherited.
type Identity struct {
	ID          string       `json:"agent_id"`
	UserID      string       `json:"user_id"`
	ClientID    string       `json:"client_id"`
	Name        string       `json:"name"`
	Permissions []Permission `json:"permissions"`

	// TokenHash is the SHA-256 hex digest of the bearer token. The raw token
	// is shown to the caller exactly once, at creation time, and is never
	// persisted or logged anywhere.
	TokenHash string `json:"-"`

	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// HasPermission reports whether the identity is both un-revoked and holds p.
func (a *Identity) HasPermission(p Permission) bool {
	if a.IsRevoked() {
		return false
	}
	for _, granted := range a.Permissions {
		if granted == p {
			return true
		}
	}
	return false
}

// IsRevoked reports whether the identity has been revoked.
func (a *Identity) IsRevoked() bool {
	return a.RevokedAt != nil
}

const tokenPrefix = "alg_agent_"

// GenerateToken returns a new opaque bearer token (shown to the caller once)
// and its SHA-256 hex digest (what gets persisted). 32 bytes of CSPRNG
// output, base64url-encoded, prefixed for identifiability in logs/support
// tickets — the prefix carries no secret material.
func GenerateToken() (raw string, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("agent: generating token: %w", err)
	}
	raw = tokenPrefix + base64.RawURLEncoding.EncodeToString(buf)
	return raw, HashToken(raw), nil
}

// HashToken returns the SHA-256 hex digest of a raw bearer token, for
// persistence and comparison. Raw tokens are never stored.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// VerifyToken does a constant-time comparison of a raw token against a
// stored hash, to avoid leaking hash-match timing.
func VerifyToken(raw, storedHash string) bool {
	computed := HashToken(raw)
	return subtle.ConstantTimeCompare([]byte(computed), []byte(storedHash)) == 1
}
