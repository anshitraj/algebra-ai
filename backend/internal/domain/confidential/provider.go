// Package confidential defines ConfidentialComputeProvider — an OPTIONAL
// abstraction (mandate §26) for confidential computation over sensitive
// financial attributes (encrypted spending thresholds, confidential policy
// inputs, private reputation signals). Algebra must function completely
// without any implementation of this beyond LocalEncryptedProvider — Arcium
// is one possible backend, never a requirement.
package confidential

import "context"

// Mode labels which backend is active, so it's always visible whether a
// "confidential" evaluation is genuinely running under MPC/confidential
// compute (Arcium) or is just server-side encryption-at-rest
// (LocalEncryptedProvider) — the two provide very different guarantees and
// must never be conflated in a UI or audit trail.
type Mode string

const (
	ModeLocal  Mode = "local"
	ModeArcium Mode = "arcium"
)

// Provider is the confidential-compute abstraction. It is intentionally
// small: the three operations the mandate's own use cases actually need
// (encrypt/decrypt an attribute, and evaluate a threshold without the
// caller needing the plaintext threshold), not a general MPC framework.
type Provider interface {
	Mode() Mode

	EncryptAttribute(ctx context.Context, plaintext, aad []byte) (ciphertext []byte, err error)
	DecryptAttribute(ctx context.Context, ciphertext, aad []byte) (plaintext []byte, err error)

	// EvaluateThresholdConfidentially reports whether amountMinorUnits
	// exceeds a threshold that was previously sealed via EncryptAttribute,
	// without requiring the caller to hold the threshold in plaintext.
	// LocalEncryptedProvider satisfies this by decrypting server-side (a
	// real but modest guarantee: encryption at rest, not confidential
	// computation); a genuine ConfidentialComputeProvider (Arcium) would
	// perform this as an actual MPC/confidential computation instead.
	EvaluateThresholdConfidentially(ctx context.Context, encryptedThreshold, aad []byte, amountMinorUnits int64) (exceeded bool, err error)
}
