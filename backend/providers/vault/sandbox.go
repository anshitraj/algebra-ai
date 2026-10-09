// Package vault holds CardVaultProvider implementations. Algebra never
// builds its own PAN/CVV vault (mandate §21/§69) — every implementation
// here either delegates to a real PCI-compliant tokenization vendor or, for
// SandboxProvider, generates obviously-fake tokens that were never a real
// card in the first place.
package vault

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/project-algebra/algebra/internal/domain/payment"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// SandboxProvider is a real, functioning CardVaultProvider for local
// development and tests. It never receives, stores, or derives a real PAN —
// TokenizeRequest.ProviderNonce is treated as an opaque client-side
// reference (exactly as a real hosted-fields nonce would be), and the
// "last four digits" shown back are deterministically derived from that
// nonce's hash, not from any real card number.
type SandboxProvider struct {
	mu      sync.Mutex
	sources map[string]payment.PaymentSource
	now     func() time.Time
}

func NewSandboxProvider() *SandboxProvider {
	return &SandboxProvider{sources: map[string]payment.PaymentSource{}, now: time.Now}
}

func (p *SandboxProvider) Mode() payment.ProviderMode { return payment.ProviderModeSandbox }

func (p *SandboxProvider) Tokenize(_ context.Context, req payment.TokenizeRequest) (*payment.TokenizeResult, error) {
	if req.ProviderNonce == "" {
		return nil, fmt.Errorf("vault: provider nonce is required (would come from a hosted-fields client SDK)")
	}
	sum := sha256.Sum256([]byte(req.ProviderNonce))
	last4 := fmt.Sprintf("%04d", binary.BigEndian.Uint16(sum[:2])%10000)

	source := payment.PaymentSource{
		ID:               "src_" + uuid.NewString(),
		UserID:           req.UserID,
		Alias:            req.Alias,
		Type:             payment.SourceCard,
		ProviderMode:     p.Mode(),
		ProviderTokenRef: "sandbox_tok_" + uuid.NewString(),
		Network:          "visa",
		Last4:            last4,
		Nickname:         req.Nickname,
		Capabilities: payment.Capabilities{
			CanPay:                     true,
			SupportedCurrencies:        []string{"INR"},
			TransactionLimitMinorUnits: 500000,
			RequiresUserAuth:           false,
			Requires3DS:                true,
		},
		CreatedAt: p.now(),
	}

	p.mu.Lock()
	p.sources[source.ID] = source
	p.mu.Unlock()

	return &payment.TokenizeResult{Source: source}, nil
}

func (p *SandboxProvider) ListSources(_ context.Context, userID string) ([]payment.PaymentSource, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []payment.PaymentSource
	for _, s := range p.sources {
		if s.UserID == userID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (p *SandboxProvider) RevokeSource(_ context.Context, sourceID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.sources[sourceID]
	if !ok {
		return fmt.Errorf("%w: sandbox payment source %s", shared.ErrNotFound, sourceID)
	}
	now := p.now()
	s.RevokedAt = &now
	p.sources[sourceID] = s
	return nil
}

var _ payment.CardVaultProvider = (*SandboxProvider)(nil)
