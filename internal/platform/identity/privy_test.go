package identity

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/account"
)

const (
	testPrivyApp = "test-privy-app"
	// Two real-shaped Solana addresses (32-byte public keys).
	embeddedAddr = "8FXvuGPQGMmGRLwkUka6dHhr4g7xUo2KUMQZMK28B1XG"
	phantomAddr  = "GhpmTWT8ng3viaQtFNDF7T7RyrmemosWvEDetKmBEoPA"
)

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// signPrivyTestToken signs claims as Privy would: ES256, raw r||s.
func signPrivyTestToken(t *testing.T, k *ecdsa.PrivateKey, kid string, header map[string]any, claims map[string]any) string {
	t.Helper()
	if header == nil {
		header = map[string]any{"alg": "ES256", "typ": "JWT", "kid": kid}
	}
	h, _ := json.Marshal(header)
	c, _ := json.Marshal(claims)
	signing := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
	digest := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, k, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func linked(accounts ...map[string]any) string {
	b, _ := json.Marshal(accounts)
	return string(b)
}

func claimsFor(sub, linkedAccounts string) map[string]any {
	now := time.Now()
	return map[string]any{
		"iss": "privy.io", "aud": testPrivyApp, "sub": sub,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "linked_accounts": linkedAccounts,
	}
}

// jwksServer serves k under kid and counts requests.
func jwksServer(t *testing.T, kid string, k *ecdsa.PrivateKey) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		raw, _ := k.PublicKey.Bytes() // 0x04 || X || Y
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{
			{"kty": "RSA", "kid": "ignored", "n": "x", "e": "AQAB"},
			{"kty": "EC", "crv": "P-256", "alg": "ES256", "use": "sig", "kid": kid,
				"x": base64.RawURLEncoding.EncodeToString(raw[1:33]), "y": base64.RawURLEncoding.EncodeToString(raw[33:])},
		}})
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func jwksVerifier(t *testing.T, srv *httptest.Server) *Privy {
	t.Helper()
	p, err := NewPrivy(testPrivyApp, "")
	if err != nil {
		t.Fatal(err)
	}
	p.jwksURL, p.client = srv.URL, srv.Client()
	return p
}

func TestPrivy_ReadsTheVerifiedEmailAndTheSolanaWallets(t *testing.T) {
	k := newKey(t)
	srv, _ := jwksServer(t, "k1", k)
	p := jwksVerifier(t, srv)

	tok := signPrivyTestToken(t, k, "k1", nil, claimsFor("did:privy:abc", linked(
		map[string]any{"type": "google_oauth", "email": "Ada@Example.com", "name": "Ada Lovelace", "subject": "g-1"},
		map[string]any{"type": "email", "address": "ada@example.org"},
		map[string]any{"type": "wallet", "chain_type": "solana", "address": embeddedAddr, "wallet_client_type": "privy", "connector_type": "embedded"},
		map[string]any{"type": "wallet", "chain_type": "solana", "address": phantomAddr, "wallet_client_type": "phantom", "connector_type": "solana_adapter"},
		map[string]any{"type": "wallet", "chain_type": "ethereum", "address": "0x0000000000000000000000000000000000000001"},
		map[string]any{"type": "wallet", "chain_type": "solana", "address": "not-a-key"},
		map[string]any{"type": "farcaster", "fid": 1},
	)))
	id, err := p.VerifyIdentityToken(context.Background(), tok)
	if err != nil {
		t.Fatal(err)
	}
	if id.Profile.Provider != "privy" || id.Profile.ProviderUserID != "did:privy:abc" {
		t.Errorf("identity: %+v", id.Profile)
	}
	// The email login (a one-time code) outranks Google's.
	if id.Profile.Email != "ada@example.org" || !id.Profile.EmailVerified || id.Profile.Name != "Ada Lovelace" {
		t.Errorf("profile: %+v", id.Profile)
	}
	want := []account.Wallet{
		{Chain: "solana", Address: embeddedAddr, Kind: account.WalletEmbedded, Source: "privy"},
		{Chain: "solana", Address: phantomAddr, Kind: account.WalletExternal, Source: "privy"},
	}
	if len(id.Wallets) != len(want) || id.Wallets[0] != want[0] || id.Wallets[1] != want[1] {
		t.Errorf("wallets: %+v", id.Wallets)
	}
}

func TestPrivy_AWalletOnlySignInHasNoEmail(t *testing.T) {
	k := newKey(t)
	srv, _ := jwksServer(t, "k1", k)
	p := jwksVerifier(t, srv)
	tok := signPrivyTestToken(t, k, "k1", nil, claimsFor("did:privy:w", linked(
		map[string]any{"type": "wallet", "chain_type": "solana", "address": phantomAddr, "wallet_client_type": "phantom"},
	)))
	id, err := p.VerifyIdentityToken(context.Background(), tok)
	if err != nil {
		t.Fatal(err)
	}
	if id.Profile.Email != "" || id.Profile.EmailVerified || len(id.Wallets) != 1 {
		t.Errorf("wallet-only: %+v", id)
	}
}

func TestPrivy_RefusesWhatPrivyDidNotIssueForThisApp(t *testing.T) {
	k, other := newKey(t), newKey(t)
	srv, _ := jwksServer(t, "k1", k)
	p := jwksVerifier(t, srv)
	good := func() map[string]any { return claimsFor("did:privy:abc", linked()) }
	with := func(key, value string) map[string]any {
		c := good()
		if value == "" {
			delete(c, key)
		} else {
			c[key] = value
		}
		return c
	}
	expired := good()
	expired["exp"] = time.Now().Add(-2 * time.Minute).Unix()
	future := good()
	future["iat"] = time.Now().Add(10 * time.Minute).Unix()
	audList := good()
	audList["aud"] = []string{"someone-else"}

	valid := signPrivyTestToken(t, k, "k1", nil, good())
	parts := strings.Split(valid, ".")
	tampered := parts[0] + "." + base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"privy.io","aud":"`+testPrivyApp+`","sub":"did:privy:mallory","exp":9999999999,"linked_accounts":"[]"}`)) + "." + parts[2]

	for name, tok := range map[string]string{
		"another app":           signPrivyTestToken(t, k, "k1", nil, with("aud", "someone-else")),
		"another app, as list":  signPrivyTestToken(t, k, "k1", nil, audList),
		"another issuer":        signPrivyTestToken(t, k, "k1", nil, with("iss", "evil.io")),
		"expired":               signPrivyTestToken(t, k, "k1", nil, expired),
		"from the future":       signPrivyTestToken(t, k, "k1", nil, future),
		"not a privy DID":       signPrivyTestToken(t, k, "k1", nil, with("sub", "user-1")),
		"no user data":          signPrivyTestToken(t, k, "k1", nil, with("linked_accounts", "")),
		"signed by another key": signPrivyTestToken(t, other, "k1", nil, good()),
		"unknown key":           signPrivyTestToken(t, k, "k2", nil, good()),
		"alg none":              signPrivyTestToken(t, k, "k1", map[string]any{"alg": "none", "kid": "k1"}, good()),
		"alg HS256":             signPrivyTestToken(t, k, "k1", map[string]any{"alg": "HS256", "kid": "k1"}, good()),
		"claims swapped":        tampered,
		"not a JWT":             "abc.def",
		"empty":                 "",
	} {
		if _, err := p.VerifyIdentityToken(context.Background(), tok); !errors.Is(err, ErrPrivyToken) {
			t.Errorf("%s: %v, want ErrPrivyToken", name, err)
		}
	}
	if _, err := p.VerifyIdentityToken(context.Background(), valid); err != nil {
		t.Errorf("the valid token after all that: %v", err)
	}
}

func TestPrivy_KeysAreCachedAndAnUnknownKidCantHammerPrivy(t *testing.T) {
	k := newKey(t)
	srv, hits := jwksServer(t, "k1", k)
	p := jwksVerifier(t, srv)
	now := time.Now()
	p.now = func() time.Time { return now }
	ctx := context.Background()

	for range 3 {
		if _, err := p.VerifyIdentityToken(ctx, signPrivyTestToken(t, k, "k1", nil, claimsFor("did:privy:a", linked()))); err != nil {
			t.Fatal(err)
		}
	}
	for range 5 {
		_, _ = p.VerifyIdentityToken(ctx, signPrivyTestToken(t, k, "junk", nil, claimsFor("did:privy:a", linked())))
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("JWKS fetched %d times, want 1", n)
	}
	// A minute on, a new kid may be looked up (Privy rotated its key).
	now = now.Add(2 * time.Minute)
	_, _ = p.VerifyIdentityToken(ctx, signPrivyTestToken(t, k, "rotated", nil, claimsFor("did:privy:a", linked())))
	if n := hits.Load(); n != 2 {
		t.Fatalf("JWKS fetched %d times, want 2", n)
	}
}

func TestPrivy_TheDashboardKeyNeedsNoRequest(t *testing.T) {
	k := newKey(t)
	der, _ := x509.MarshalPKIXPublicKey(&k.PublicKey)
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	// As pasted into an environment variable, line breaks written as \n.
	p, err := NewPrivy(testPrivyApp, strings.ReplaceAll(pemKey, "\n", `\n`))
	if err != nil {
		t.Fatal(err)
	}
	p.jwksURL = "http://127.0.0.1:1/never"
	if _, err := p.VerifyIdentityToken(context.Background(), signPrivyTestToken(t, k, "any", nil, claimsFor("did:privy:a", linked()))); err != nil {
		t.Fatal(err)
	}
	if _, err := NewPrivy(testPrivyApp, "not a key"); err == nil {
		t.Error("a malformed verification key must be refused at startup")
	}
	if _, err := NewPrivy(" ", ""); err == nil {
		t.Error("an app ID is required")
	}
}
