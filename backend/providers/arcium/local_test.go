package arcium

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"testing"

	"github.com/project-algebra/algebra/internal/domain/privacy"
)

func newTestProvider(t *testing.T) *LocalEncryptedProvider {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generating key: %v", err)
	}
	enc, err := privacy.NewAESGCMEncryptor(key)
	if err != nil {
		t.Fatalf("NewAESGCMEncryptor: %v", err)
	}
	return NewLocalEncryptedProvider(enc)
}

func TestLocalEncryptedProvider_RoundTrip(t *testing.T) {
	p := newTestProvider(t)
	ctx := context.Background()
	plaintext := []byte("confidential-attribute")
	aad := []byte("attr-1")

	sealed, err := p.EncryptAttribute(ctx, plaintext, aad)
	if err != nil {
		t.Fatalf("EncryptAttribute failed: %v", err)
	}
	got, err := p.DecryptAttribute(ctx, sealed, aad)
	if err != nil {
		t.Fatalf("DecryptAttribute failed: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Errorf("round trip mismatch: got %q, want %q", got, plaintext)
	}
}

func TestLocalEncryptedProvider_EvaluateThresholdConfidentially(t *testing.T) {
	p := newTestProvider(t)
	ctx := context.Background()
	aad := []byte("threshold-1")

	thresholdBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(thresholdBytes, 500000) // ₹5,000
	sealed, err := p.EncryptAttribute(ctx, thresholdBytes, aad)
	if err != nil {
		t.Fatalf("EncryptAttribute failed: %v", err)
	}

	exceeded, err := p.EvaluateThresholdConfidentially(ctx, sealed, aad, 600000)
	if err != nil {
		t.Fatalf("EvaluateThresholdConfidentially failed: %v", err)
	}
	if !exceeded {
		t.Error("expected 600000 to exceed a 500000 threshold")
	}

	notExceeded, err := p.EvaluateThresholdConfidentially(ctx, sealed, aad, 100000)
	if err != nil {
		t.Fatalf("EvaluateThresholdConfidentially failed: %v", err)
	}
	if notExceeded {
		t.Error("expected 100000 to NOT exceed a 500000 threshold")
	}
}

func TestLocalEncryptedProvider_WrongAADFails(t *testing.T) {
	p := newTestProvider(t)
	ctx := context.Background()
	sealed, err := p.EncryptAttribute(ctx, []byte("secret"), []byte("aad-1"))
	if err != nil {
		t.Fatalf("EncryptAttribute failed: %v", err)
	}
	if _, err := p.DecryptAttribute(ctx, sealed, []byte("aad-2")); err == nil {
		t.Error("expected decryption under a mismatched AAD to fail")
	}
}
