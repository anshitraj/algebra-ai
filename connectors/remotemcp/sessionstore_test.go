package remotemcp

import (
	"bytes"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/privacy"
	"golang.org/x/oauth2"
)

func testEncryptor(t *testing.T) *privacy.AESGCMEncryptor {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	enc, err := privacy.NewAESGCMEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	return enc
}

// testExpiry is fixed: built from the clock, two calls to testSession could
// straddle a second and disagree.
var testExpiry = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

func testSession(merchant string) *Session {
	return &Session{
		Merchant: merchant,
		Endpoint: "https://mcp.example.test/mcp",
		ClientID: "client-123",
		AuthURL:  "https://auth.example.test/authorize",
		TokenURL: "https://auth.example.test/token",
		Scopes:   []string{"tools:read", "tools:write"},
		Token: &oauth2.Token{
			AccessToken:  "access-SECRET-value",
			RefreshToken: "refresh-SECRET-value",
			TokenType:    "Bearer",
			Expiry:       testExpiry,
		},
		Settings: map[string]string{"address_id": "addr_1"},
		LinkedAt: time.Now().UTC().Truncate(time.Second),
	}
}

func TestFileSessionStore_RoundTripIsEncryptedAtRest(t *testing.T) {
	dir := t.TempDir()
	store := NewFileSessionStore(dir, testEncryptor(t))

	if err := store.Save(testSession("zepto")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "zepto.session"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"access-SECRET-value", "refresh-SECRET-value", "client-123", "addr_1"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("session file contains %q in plaintext", secret)
		}
	}

	got, err := store.Load("zepto")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := testSession("zepto")
	if got.Token.AccessToken != want.Token.AccessToken || got.Token.RefreshToken != want.Token.RefreshToken ||
		got.ClientID != want.ClientID || got.Settings["address_id"] != "addr_1" || !got.Token.Expiry.Equal(want.Token.Expiry) {
		t.Fatalf("round trip mismatch: got %+v", got)
	}
	if !store.Linked("zepto") {
		t.Fatal("Linked should be true after Save")
	}
}

func TestFileSessionStore_NotLinked(t *testing.T) {
	store := NewFileSessionStore(t.TempDir(), testEncryptor(t))
	if _, err := store.Load("zepto"); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("expected ErrNotLinked, got %v", err)
	}
	if store.Linked("zepto") {
		t.Fatal("Linked should be false with no session file")
	}
}

func TestFileSessionStore_WrongKeyFails(t *testing.T) {
	dir := t.TempDir()
	if err := NewFileSessionStore(dir, testEncryptor(t)).Save(testSession("zepto")); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileSessionStore(dir, testEncryptor(t)).Load("zepto"); err == nil {
		t.Fatal("a different master key must not decrypt the session")
	}
}

// A session file copied under another merchant's name must not decrypt —
// the AAD binds ciphertext to the merchant it was written for.
func TestFileSessionStore_FileMovedBetweenMerchantsFails(t *testing.T) {
	dir := t.TempDir()
	store := NewFileSessionStore(dir, testEncryptor(t))
	if err := store.Save(testSession("zepto")); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "zepto.session"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "swiggy_instamart.session"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load("swiggy_instamart"); err == nil {
		t.Fatal("expected decryption failure for a session moved between merchants")
	}
}

func TestFileSessionStore_RejectsUnsafeNames(t *testing.T) {
	store := NewFileSessionStore(t.TempDir(), testEncryptor(t))
	for _, name := range []string{"../zepto", "Zepto", "", "a/b", `a\b`} {
		if _, err := store.Load(name); err == nil || errors.Is(err, ErrNotLinked) {
			t.Fatalf("Load(%q) should reject the name, got %v", name, err)
		}
		if err := store.Save(testSession(name)); err == nil {
			t.Fatalf("Save with merchant %q should be rejected", name)
		}
	}
}

func TestFileSessionStore_Delete(t *testing.T) {
	store := NewFileSessionStore(t.TempDir(), testEncryptor(t))
	if err := store.Save(testSession("zepto")); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete("zepto"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load("zepto"); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("expected ErrNotLinked after Delete, got %v", err)
	}
	if err := store.Delete("zepto"); err != nil {
		t.Fatalf("deleting a missing session should not error: %v", err)
	}
}
