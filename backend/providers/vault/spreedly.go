package vault

import (
	"context"
	"net/http"
	"time"

	"github.com/project-algebra/algebra/internal/domain/payment"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// SpreedlyProvider is the adapter slot for Spreedly
// (https://developer.spreedly.com/docs/tokenization), a real PCI-compliant
// tokenization vendor. It requires a Spreedly environment key + access
// secret, neither of which exist in this environment — every method fails
// closed with shared.ErrNotImplemented rather than fabricating a token.
//
// Wiring this up for real means: swap the browser's card-entry UI to
// Spreedly's hosted iframe fields, POST the resulting payment-method token
// to Tokenize below, and implement the corresponding Spreedly API calls
// (environment key + access secret in the Authorization header, per
// Spreedly's docs) instead of returning ErrNotImplemented.
type SpreedlyProvider struct {
	EnvironmentKey string
	AccessSecret   string
	Client         *http.Client
}

func NewSpreedlyProvider(environmentKey, accessSecret string) *SpreedlyProvider {
	return &SpreedlyProvider{EnvironmentKey: environmentKey, AccessSecret: accessSecret, Client: &http.Client{Timeout: 10 * time.Second}}
}

func (p *SpreedlyProvider) Mode() payment.ProviderMode { return payment.ProviderModeReal }

func (p *SpreedlyProvider) Tokenize(context.Context, payment.TokenizeRequest) (*payment.TokenizeResult, error) {
	return nil, shared.ErrNotImplemented
}

func (p *SpreedlyProvider) ListSources(context.Context, string) ([]payment.PaymentSource, error) {
	return nil, shared.ErrNotImplemented
}

func (p *SpreedlyProvider) RevokeSource(context.Context, string) error {
	return shared.ErrNotImplemented
}

var _ payment.CardVaultProvider = (*SpreedlyProvider)(nil)
