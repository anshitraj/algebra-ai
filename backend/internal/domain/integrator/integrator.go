// Package integrator identifies a third-party application calling
// Algebra's standalone policy-evaluation surface (POST
// /api/v1/policy/evaluate-transaction, MCP policy.evaluate_transaction) —
// not an AI shopping agent acting inside Algebra's own commerce flow
// (see internal/domain/agent), and not tied to an Algebra user account.
// A Kite-style wallet registers once as an Integrator and then asks, per
// transaction, "should this go through" — Algebra never sees its users'
// payment credentials or transaction history, only what the integrator
// chooses to send in each request (see policy.Input and
// app.EvaluateTransactionInput).
package integrator

import "time"

// Integrator is a B2B API-key identity. Deliberately minimal: no
// permissions to scope (single purpose — evaluate a transaction) and no
// owning user (it isn't an Algebra end user).
type Integrator struct {
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

// IsRevoked reports whether this integrator's token has been revoked.
func (i *Integrator) IsRevoked() bool { return i.RevokedAt != nil }
