package payment

import "context"

// ProviderMode labels which kind of backing a CardVaultProvider has. It is
// surfaced in API/UI responses so a sandbox token can never be mistaken for
// a real one (mandate §54: "Sandbox/test UI should have a visible
// environment indicator").
type ProviderMode string

const (
	ProviderModeSandbox ProviderMode = "sandbox"
	ProviderModeReal    ProviderMode = "real"
)

// TokenizeRequest carries only a provider-hosted-field nonce/reference —
// the browser talks to the PCI vault directly (hosted fields / provider JS
// SDK) and hands Algebra a single-use reference. Algebra's backend never
// receives a PAN, CVV, or track data in this struct or any other.
type TokenizeRequest struct {
	UserID        string
	ProviderNonce string // opaque, single-use, issued by the vault's client-side SDK
	Nickname      string
	Alias         string // e.g. "payment:personal"
}

// TokenizeResult wraps the PaymentSource created from a successful
// tokenization.
type TokenizeResult struct {
	Source PaymentSource
}

// CardVaultProvider is the interface every tokenization backend implements.
// Concrete adapters live under /providers/vault — this package only defines
// the contract, per the mandate's "MCP + REST + SDK share the same domain
// logic" rule extended to providers: one interface, swappable backends.
type CardVaultProvider interface {
	Mode() ProviderMode
	Tokenize(ctx context.Context, req TokenizeRequest) (*TokenizeResult, error)
	ListSources(ctx context.Context, userID string) ([]PaymentSource, error)
	RevokeSource(ctx context.Context, sourceID string) error
}
