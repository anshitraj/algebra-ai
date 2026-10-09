package privacy

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generating test key: %v", err)
	}
	return key
}

func TestAESGCM_RoundTrip(t *testing.T) {
	enc, err := NewAESGCMEncryptor(testKey(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	plaintext := []byte(`{"line1":"221B Baker St","city":"London"}`)
	aad := []byte("profile-123|user-1|SHIPPING")

	ciphertext, nonce, err := enc.Encrypt(plaintext, aad)
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}
	if bytes.Equal(ciphertext, plaintext) {
		t.Fatal("ciphertext must not equal plaintext")
	}

	got, err := enc.Decrypt(ciphertext, nonce, aad)
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("round trip mismatch: got %q, want %q", got, plaintext)
	}
}

func TestAESGCM_WrongKeyFails(t *testing.T) {
	enc1, _ := NewAESGCMEncryptor(testKey(t))
	enc2, _ := NewAESGCMEncryptor(testKey(t))

	ciphertext, nonce, err := enc1.Encrypt([]byte("secret"), []byte("aad"))
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}
	if _, err := enc2.Decrypt(ciphertext, nonce, []byte("aad")); err == nil {
		t.Error("expected decryption with the wrong key to fail")
	}
}

func TestAESGCM_TamperedCiphertextFails(t *testing.T) {
	enc, _ := NewAESGCMEncryptor(testKey(t))
	ciphertext, nonce, err := enc.Encrypt([]byte("secret address data"), []byte("aad"))
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}
	tampered := append([]byte(nil), ciphertext...)
	tampered[0] ^= 0xFF

	if _, err := enc.Decrypt(tampered, nonce, []byte("aad")); err == nil {
		t.Error("expected decryption of tampered ciphertext to fail")
	}
}

func TestAESGCM_WrongAADFails(t *testing.T) {
	// This is the confused-deputy check: ciphertext bound to one profile's
	// AAD must not decrypt under a different profile's AAD, even with the
	// right key and nonce.
	enc, _ := NewAESGCMEncryptor(testKey(t))
	ciphertext, nonce, err := enc.Encrypt([]byte("secret"), []byte("profile-1|user-1|SHIPPING"))
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}
	if _, err := enc.Decrypt(ciphertext, nonce, []byte("profile-2|user-1|SHIPPING")); err == nil {
		t.Error("expected decryption under a mismatched AAD to fail")
	}
}

func TestNewAESGCMEncryptor_RejectsWrongKeyLength(t *testing.T) {
	if _, err := NewAESGCMEncryptor([]byte("too-short")); err == nil {
		t.Error("expected error for non-32-byte key")
	}
}

func TestAESGCM_NoncesAreUnique(t *testing.T) {
	enc, _ := NewAESGCMEncryptor(testKey(t))
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		_, nonce, err := enc.Encrypt([]byte("x"), nil)
		if err != nil {
			t.Fatalf("encrypt failed: %v", err)
		}
		key := string(nonce)
		if seen[key] {
			t.Fatal("nonce reuse detected")
		}
		seen[key] = true
	}
}
