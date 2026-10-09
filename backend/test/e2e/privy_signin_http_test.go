package e2e

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/project-algebra/algebra/internal/platform/solana"
)

const e2ePrivyApp = "e2e-privy-app"

// privyIssuer stands in for Privy: it signs identity tokens with a key the
// API is given as its dashboard verification key.
type privyIssuer struct {
	t   *testing.T
	key *ecdsa.PrivateKey
}

func newPrivyIssuer(t *testing.T) (*privyIssuer, string) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKIXPublicKey(&k.PublicKey)
	return &privyIssuer{t, k}, string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func (p *privyIssuer) token(aud, did string, accounts ...map[string]any) string {
	p.t.Helper()
	la, _ := json.Marshal(accounts)
	h, _ := json.Marshal(map[string]any{"alg": "ES256", "typ": "JWT", "kid": "e2e"})
	c, _ := json.Marshal(map[string]any{
		"iss": "privy.io", "aud": aud, "sub": did, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
		"linked_accounts": string(la),
	})
	signing := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
	d := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, p.key, d[:])
	if err != nil {
		p.t.Fatal(err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func randomSolanaAddress(t *testing.T) string {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return solana.EncodeBase58(b)
}

func embeddedWallet(addr string) map[string]any {
	return map[string]any{"type": "wallet", "chain_type": "solana", "address": addr, "wallet_client_type": "privy", "connector_type": "embedded"}
}

// browserCall is a request the way the web app makes it: cookie, no token.
func browserCall(t *testing.T, method, url string, cookie *http.Cookie, body any) (int, *http.Cookie, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, url, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	var session *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "algebra_session" && c.Value != "" {
			session = c
		}
	}
	return resp.StatusCode, session, out
}

func userOf(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	u, _ := out["user"].(map[string]any)
	if u == nil {
		t.Fatalf("no user in %v", out)
	}
	return u
}

func TestPrivySignInOverHTTP(t *testing.T) {
	issuer, pemKey := newPrivyIssuer(t)
	_, base := serveWith(t, map[string]string{"PRIVY_APP_ID": e2ePrivyApp, "PRIVY_VERIFICATION_KEY": pemKey})
	signIn := func(tok string) (int, *http.Cookie, map[string]any) {
		return browserCall(t, "POST", base+"/api/v1/auth/privy", nil, map[string]any{"identity_token": tok})
	}

	status, _, providers := browserCall(t, "GET", base+"/api/v1/auth/providers", nil, nil)
	if status != 200 || providers["privy"] != true || providers["privy_app_id"] != e2ePrivyApp {
		t.Fatalf("providers: %d %v", status, providers)
	}

	t.Run("a wallet alone makes an account", func(t *testing.T) {
		did, wallet := "did:privy:"+uuid.NewString(), randomSolanaAddress(t)
		status, cookie, out := signIn(issuer.token(e2ePrivyApp, did, embeddedWallet(wallet)))
		if status != 201 || cookie == nil {
			t.Fatalf("first sign-in: %d %v", status, out)
		}
		u := userOf(t, out)
		if u["email"] != "" || u["email_verified"] != false {
			t.Errorf("a wallet-only account has no email to show: %v", u)
		}
		if u["name"] != "" {
			t.Errorf("no name until the person gives one: %q", u["name"])
		}
		ws, _ := u["wallets"].([]any)
		if len(ws) != 1 || ws[0].(map[string]any)["address"] != wallet || ws[0].(map[string]any)["kind"] != "embedded" {
			t.Errorf("wallets: %v", u["wallets"])
		}

		// The cookie is a session like any other.
		status, _, out = browserCall(t, "GET", base+"/api/v1/auth/session", cookie, nil)
		if status != 200 || userOf(t, out)["id"] != u["id"] {
			t.Fatalf("session: %d %v", status, out)
		}

		// Next time, with an email linked in Privy since: the same account.
		status, _, out = signIn(issuer.token(e2ePrivyApp, did, embeddedWallet(wallet),
			map[string]any{"type": "email", "address": "later-" + uuid.NewString() + "@example.com"}))
		if status != 200 || userOf(t, out)["id"] != u["id"] {
			t.Errorf("returning sign-in: %d %v (want user %v)", status, out, u["id"])
		}
	})

	t.Run("a verified email joins the account that has it", func(t *testing.T) {
		email := "privy-" + uuid.NewString() + "@example.com"
		status, _, out := browserCall(t, "POST", base+"/api/v1/auth/signup", nil, map[string]any{"name": "Pat", "email": email, "password": "correct horse battery staple"})
		if status != 201 {
			t.Fatalf("signup: %d %v", status, out)
		}
		existing := userOf(t, out)["id"]

		wallet := randomSolanaAddress(t)
		status, _, out = signIn(issuer.token(e2ePrivyApp, "did:privy:"+uuid.NewString(),
			map[string]any{"type": "email", "address": email}, embeddedWallet(wallet)))
		if status != 200 {
			t.Fatalf("privy sign-in: %d %v", status, out)
		}
		u := userOf(t, out)
		if u["id"] != existing || u["email"] != email {
			t.Errorf("signed into %v (%v), want %v", u["id"], u["email"], existing)
		}
		linked, _ := u["linked_providers"].([]any)
		if len(linked) != 1 || linked[0] != "privy" {
			t.Errorf("linked: %v", linked)
		}
	})

	t.Run("a token for another app is refused", func(t *testing.T) {
		status, cookie, out := signIn(issuer.token("another-app", "did:privy:"+uuid.NewString(), embeddedWallet(randomSolanaAddress(t))))
		if status != 401 || cookie != nil {
			t.Errorf("another app's token: %d %v", status, out)
		}
	})

	t.Run("neither email nor wallet", func(t *testing.T) {
		status, cookie, out := signIn(issuer.token(e2ePrivyApp, "did:privy:"+uuid.NewString(), map[string]any{"type": "farcaster", "fid": 7}))
		if status != 400 || cookie != nil {
			t.Errorf("no email, no wallet: %d %v", status, out)
		}
	})
}

func TestPrivySignInOverHTTP_OffUnlessConfigured(t *testing.T) {
	_, base := serveWith(t, map[string]string{"PRIVY_APP_ID": ""})
	status, _, providers := browserCall(t, "GET", base+"/api/v1/auth/providers", nil, nil)
	if status != 200 || providers["privy"] != false || providers["privy_app_id"] != nil {
		t.Errorf("providers: %v", providers)
	}
	if status, _, _ := browserCall(t, "POST", base+"/api/v1/auth/privy", nil, map[string]any{"identity_token": "x.y.z"}); status != 404 {
		t.Errorf("privy sign-in with Privy off: %d", status)
	}
}
