// Package tenant identifies a business integrating Algebra as agentic-
// payments infrastructure — a card app, wallet, fintech, or stablecoin app
// that wants its own end users to be able to grant AI agents controlled
// spending authority over payment sources the business already holds.
//
// A Tenant is the root of Algebra's B2B data model: it owns end users
// (internal/app.UserRecord, via the nullable users.tenant_id column), their
// agents, their persisted policy (internal/domain/policyset), and every
// AgenticPaymentIntent (internal/domain/paymentintent) those agents create.
// Algebra's own first-party reference console (frontend/) has no Tenant of its
// own — its users simply carry a NULL tenant_id.
//
// This is distinct from internal/domain/integrator, which backs a narrower,
// stateless, unowned "evaluate one transaction against my own inline rules"
// surface (POST /api/v1/policy/evaluate-transaction) that predates and
// doesn't require the full tenant/end-user/agent ownership graph below.
package tenant

import "time"

// Tenant is a B2B API-key identity — the business's own root credential,
// used for tenant-admin operations (setting policy, registering webhook
// endpoints, listing its own transactions).
type Tenant struct {
	ID   string
	Name string

	// TokenHash is the SHA-256 hex digest of the bearer token — see
	// agent.GenerateToken/HashToken/VerifyToken, reused as-is rather than
	// duplicated here. The raw token is shown to the caller exactly once,
	// at creation, and never persisted or logged.
	TokenHash string

	CreatedAt time.Time
	RevokedAt *time.Time
}

// IsRevoked reports whether this tenant's token has been revoked.
func (t *Tenant) IsRevoked() bool { return t.RevokedAt != nil }
