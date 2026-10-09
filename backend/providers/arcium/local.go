// Package arcium holds ConfidentialComputeProvider implementations:
// LocalEncryptedProvider (real, default, no external dependency) and
// ArciumProvider (stub — see arcium.go). Neither is a substitute for the
// PCI card vault (internal/domain/payment); this package never touches
// card data.
package arcium

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/project-algebra/algebra/internal/domain/confidential"
	"github.com/project-algebra/algebra/internal/domain/privacy"
)

// LocalEncryptedProvider implements ConfidentialComputeProvider using plain
// AES-256-GCM (the same primitive as PrivacyResolver). It is real and fully
// functional — Algebra runs correctly with only this provider — but its
// confidentiality guarantee is "encrypted at rest, decrypted server-side to
// evaluate," not genuine multi-party/confidential computation.
type LocalEncryptedProvider struct {
	enc privacy.Encryptor
}

func NewLocalEncryptedProvider(enc privacy.Encryptor) *LocalEncryptedProvider {
	return &LocalEncryptedProvider{enc: enc}
}

func (p *LocalEncryptedProvider) Mode() confidential.Mode { return confidential.ModeLocal }

func (p *LocalEncryptedProvider) EncryptAttribute(_ context.Context, plaintext, aad []byte) ([]byte, error) {
	ciphertext, nonce, err := p.enc.Encrypt(plaintext, aad)
	if err != nil {
		return nil, fmt.Errorf("arcium: local encrypt: %w", err)
	}
	// Nonce is prepended so DecryptAttribute is self-contained (single
	// []byte round-trips), rather than requiring a second out-of-band value.
	return append(nonce, ciphertext...), nil
}

func (p *LocalEncryptedProvider) DecryptAttribute(_ context.Context, sealed, aad []byte) ([]byte, error) {
	const nonceLen = 12 // AES-GCM standard nonce size
	if len(sealed) < nonceLen {
		return nil, fmt.Errorf("arcium: sealed value too short to contain a nonce")
	}
	nonce, ciphertext := sealed[:nonceLen], sealed[nonceLen:]
	plaintext, err := p.enc.Decrypt(ciphertext, nonce, aad)
	if err != nil {
		return nil, fmt.Errorf("arcium: local decrypt: %w", err)
	}
	return plaintext, nil
}

func (p *LocalEncryptedProvider) EvaluateThresholdConfidentially(ctx context.Context, encryptedThreshold, aad []byte, amountMinorUnits int64) (bool, error) {
	plaintext, err := p.DecryptAttribute(ctx, encryptedThreshold, aad)
	if err != nil {
		return false, err
	}
	if len(plaintext) != 8 {
		return false, fmt.Errorf("arcium: decrypted threshold has unexpected length %d, want 8", len(plaintext))
	}
	threshold := int64(binary.BigEndian.Uint64(plaintext))
	return amountMinorUnits > threshold, nil
}

var _ confidential.Provider = (*LocalEncryptedProvider)(nil)
