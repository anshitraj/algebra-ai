// Package privacy implements PrivacyResolver: the only code path from a
// privacy alias (e.g. "shipping:home", "payment:personal") to a real,
// sensitive value. Agents and LLMs only ever see aliases — see resolver.go.
package privacy

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// Encryptor is application-level envelope encryption for sensitive profile
// fields (shipping/billing addresses, phone, email — never card PAN/CVV,
// which never touches Algebra's backend at all). AESGCMEncryptor is the
// real, functional default; a Cloud KMS-backed variant is the production
// target on GCP (mandate §25) and is not implemented in this build — there
// is no GCP project/credentials configured here.
type Encryptor interface {
	// Encrypt seals plaintext, authenticating aad (additional data, e.g. the
	// profile ID) without encrypting it. Returns ciphertext and the nonce
	// used, which must be stored alongside the ciphertext to decrypt later.
	Encrypt(plaintext, aad []byte) (ciphertext, nonce []byte, err error)
	// Decrypt opens ciphertext produced by Encrypt with the same aad and
	// nonce. Returns an error (never partial plaintext) if either the key,
	// nonce, ciphertext, or aad don't match — this is what makes tampering
	// detectable rather than silently accepted.
	Decrypt(ciphertext, nonce, aad []byte) ([]byte, error)
}

// AESGCMEncryptor implements Encryptor with AES-256-GCM. The key is a
// 32-byte data-encryption key (DEK). In production the DEK itself should be
// wrapped by Cloud KMS and only unwrapped in memory (mandate §25); this
// build reads it directly from config (see internal/platform/config) since
// no KMS project is configured here.
type AESGCMEncryptor struct {
	gcm cipher.AEAD
}

// NewAESGCMEncryptor builds an encryptor from a 32-byte key.
func NewAESGCMEncryptor(key []byte) (*AESGCMEncryptor, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("privacy: key must be 32 bytes for AES-256, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("privacy: creating AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("privacy: creating GCM mode: %w", err)
	}
	return &AESGCMEncryptor{gcm: gcm}, nil
}

func (e *AESGCMEncryptor) Encrypt(plaintext, aad []byte) (ciphertext, nonce []byte, err error) {
	nonce = make([]byte, e.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("privacy: generating nonce: %w", err)
	}
	ciphertext = e.gcm.Seal(nil, nonce, plaintext, aad)
	return ciphertext, nonce, nil
}

func (e *AESGCMEncryptor) Decrypt(ciphertext, nonce, aad []byte) ([]byte, error) {
	if len(nonce) != e.gcm.NonceSize() {
		return nil, errors.New("privacy: invalid nonce length")
	}
	plaintext, err := e.gcm.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		// Deliberately no error detail beyond "failed" — GCM auth failures
		// must not leak information about why (padding-oracle-style risk).
		return nil, errors.New("privacy: decryption failed (tampered data, wrong key, or wrong aad)")
	}
	return plaintext, nil
}

var _ Encryptor = (*AESGCMEncryptor)(nil)
