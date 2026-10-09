package identity

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/project-algebra/algebra/internal/domain/account"
	"github.com/project-algebra/algebra/internal/platform/solana"
)

// PrivyProvider is the provider name a Privy sign-in is linked under: the
// user's Privy DID is the provider user ID.
const PrivyProvider = "privy"

// ErrPrivyToken is any identity token Algebra won't accept. Its detail is
// for the log; the person is told only that sign-in didn't work.
var ErrPrivyToken = errors.New("privy: identity token rejected")

// PrivyIdentity is who a verified Privy identity token says the person is.
type PrivyIdentity struct {
	// Profile is the sign-in identity: provider "privy", the DID as provider
	// user ID, and the email when one of the person's linked accounts has a
	// verified one (empty when they signed in with a wallet only).
	Profile account.OAuthProfile
	// Wallets are the Solana wallets linked to the Privy user: the embedded
	// wallet Privy made for them and any wallet they signed in with.
	Wallets []account.Wallet
}

// Privy verifies Privy identity tokens: ES256 JWTs, signed with the app's
// key, whose linked_accounts claim lists the person's email, social logins
// and wallets. (docs.privy.io, "Identity tokens".) The keys come from
// Privy's JWKS for the app, or from the verification key copied out of the
// Privy dashboard, which needs no request at all.
type Privy struct {
	appID   string
	jwksURL string
	client  *http.Client
	now     func() time.Time

	mu      sync.Mutex
	keys    map[string]*ecdsa.PublicKey // by kid; "" is the dashboard key
	fixed   bool                        // keys came from the dashboard key: never fetched
	fetched time.Time
}

// privyJWKSRefresh bounds how often an unknown kid triggers a refetch, and
// how long fetched keys are trusted.
const (
	privyJWKSMinRefetch = time.Minute
	privyJWKSMaxAge     = time.Hour
	// privyClockSkew is the leeway on exp and iat.
	privyClockSkew = time.Minute
)

// NewPrivy verifies tokens for appID. verificationKey is the PEM public key
// from the Privy dashboard (Configuration → App settings), optional: without
// it, keys are fetched from Privy's JWKS for the app.
func NewPrivy(appID, verificationKey string) (*Privy, error) {
	appID = strings.TrimSpace(appID)
	if appID == "" {
		return nil, errors.New("privy: app ID is required")
	}
	p := &Privy{
		appID:   appID,
		jwksURL: "https://auth.privy.io/api/v1/apps/" + appID + "/jwks.json",
		client:  httpClient, now: time.Now, keys: map[string]*ecdsa.PublicKey{},
	}
	if k := strings.TrimSpace(verificationKey); k != "" {
		pub, err := parsePrivyPEM(k)
		if err != nil {
			return nil, err
		}
		p.keys[""], p.fixed = pub, true
	}
	return p, nil
}

// AppID is the Privy app this server accepts sign-ins for; the web app
// needs it to open Privy's sign-in.
func (p *Privy) AppID() string { return p.appID }

// parsePrivyPEM reads the dashboard's verification key. It is often pasted
// into an environment variable with its line breaks written as \n.
func parsePrivyPEM(k string) (*ecdsa.PublicKey, error) {
	k = strings.ReplaceAll(k, `\n`, "\n")
	block, _ := pem.Decode([]byte(k))
	if block == nil {
		return nil, errors.New("privy: PRIVY_VERIFICATION_KEY is not a PEM public key")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("privy: PRIVY_VERIFICATION_KEY: %w", err)
	}
	pub, ok := key.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		return nil, errors.New("privy: PRIVY_VERIFICATION_KEY must be a P-256 (ES256) key")
	}
	return pub, nil
}

type privyHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

type privyClaims struct {
	Iss            string          `json:"iss"`
	Aud            json.RawMessage `json:"aud"`
	Sub            string          `json:"sub"`
	Exp            int64           `json:"exp"`
	Iat            int64           `json:"iat"`
	LinkedAccounts string          `json:"linked_accounts"`
}

// privyLinkedAccount is the union of the linked-account shapes Algebra
// reads; every other field and type is ignored.
type privyLinkedAccount struct {
	Type             string  `json:"type"`
	Address          string  `json:"address"`
	Email            *string `json:"email"`
	Name             *string `json:"name"`
	ChainType        string  `json:"chain_type"`
	WalletClientType string  `json:"wallet_client_type"`
	ConnectorType    string  `json:"connector_type"`
}

// VerifyIdentityToken checks the token's signature, issuer, audience and
// lifetime, and reads who it names.
func (p *Privy) VerifyIdentityToken(ctx context.Context, token string) (*PrivyIdentity, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 || len(token) > 64<<10 {
		return nil, fmt.Errorf("%w: not a JWT", ErrPrivyToken)
	}
	var hdr privyHeader
	if err := decodeSegment(parts[0], &hdr); err != nil {
		return nil, fmt.Errorf("%w: header: %v", ErrPrivyToken, err)
	}
	if hdr.Alg != "ES256" {
		return nil, fmt.Errorf("%w: alg %q, want ES256", ErrPrivyToken, hdr.Alg)
	}
	key, err := p.key(ctx, hdr.Kid)
	if err != nil {
		return nil, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		return nil, fmt.Errorf("%w: malformed signature", ErrPrivyToken)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(key, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		return nil, fmt.Errorf("%w: bad signature", ErrPrivyToken)
	}

	var c privyClaims
	if err := decodeSegment(parts[1], &c); err != nil {
		return nil, fmt.Errorf("%w: claims: %v", ErrPrivyToken, err)
	}
	now := p.now()
	switch {
	case c.Iss != "privy.io":
		return nil, fmt.Errorf("%w: issuer %q", ErrPrivyToken, c.Iss)
	case !audienceIs(c.Aud, p.appID):
		return nil, fmt.Errorf("%w: issued for another app", ErrPrivyToken)
	case c.Exp == 0 || now.After(time.Unix(c.Exp, 0).Add(privyClockSkew)):
		return nil, fmt.Errorf("%w: expired", ErrPrivyToken)
	case c.Iat != 0 && time.Unix(c.Iat, 0).After(now.Add(privyClockSkew)):
		return nil, fmt.Errorf("%w: issued in the future", ErrPrivyToken)
	case !strings.HasPrefix(c.Sub, "did:privy:") || len(c.Sub) > 200:
		return nil, fmt.Errorf("%w: subject %q", ErrPrivyToken, c.Sub)
	}
	if c.LinkedAccounts == "" {
		// The dashboard setting that puts user data in the token is off.
		return nil, fmt.Errorf("%w: no linked_accounts claim (turn on \"Return user data in an identity token\" in the Privy dashboard)", ErrPrivyToken)
	}
	var linked []privyLinkedAccount
	if err := json.Unmarshal([]byte(c.LinkedAccounts), &linked); err != nil {
		return nil, fmt.Errorf("%w: linked_accounts: %v", ErrPrivyToken, err)
	}
	return identityFrom(c.Sub, linked), nil
}

// identityFrom reads the profile and wallets out of the linked accounts.
// An email counts only when Privy verified it: an email login (one-time
// code) first, then Google's or Apple's, which those providers verify.
func identityFrom(did string, linked []privyLinkedAccount) *PrivyIdentity {
	id := &PrivyIdentity{Profile: account.OAuthProfile{Provider: PrivyProvider, ProviderUserID: did}}
	emailRank := 99
	seen := map[string]bool{}
	for _, a := range linked {
		rank, email := 99, ""
		switch a.Type {
		case "email":
			rank, email = 0, a.Address
		case "google_oauth":
			rank, email = 1, deref(a.Email)
		case "apple_oauth":
			rank, email = 2, deref(a.Email)
		case "wallet":
			if a.ChainType != "solana" {
				continue
			}
			pk, err := solana.ParsePublicKey(a.Address)
			if err != nil || seen[pk.String()] {
				continue
			}
			seen[pk.String()] = true
			kind := account.WalletExternal
			if a.WalletClientType == "privy" || a.ConnectorType == "embedded" {
				kind = account.WalletEmbedded
			}
			id.Wallets = append(id.Wallets, account.Wallet{Chain: account.ChainSolana, Address: pk.String(), Kind: kind, Source: PrivyProvider})
		}
		if a.Type == "google_oauth" && id.Profile.Name == "" {
			id.Profile.Name = deref(a.Name)
		}
		if email = strings.TrimSpace(email); email != "" && rank < emailRank {
			if e, err := account.NormalizeEmail(email); err == nil {
				emailRank, id.Profile.Email, id.Profile.EmailVerified = rank, e, true
			}
		}
	}
	return id
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func decodeSegment(seg string, v any) error {
	raw, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

// audienceIs accepts aud as a string or an array of strings.
func audienceIs(raw json.RawMessage, appID string) bool {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return one == appID
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		for _, a := range many {
			if a == appID {
				return true
			}
		}
	}
	return false
}

// key returns the public key for kid: the dashboard key when one was given,
// otherwise from the JWKS, refetched when it is old or the kid is new (at
// most once a minute, so a stream of junk kids can't hammer Privy).
func (p *Privy) key(ctx context.Context, kid string) (*ecdsa.PublicKey, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fixed {
		return p.keys[""], nil
	}
	now := p.now()
	k, ok := p.keys[kid]
	stale := now.Sub(p.fetched) > privyJWKSMaxAge
	if ok && !stale {
		return k, nil
	}
	if !stale && now.Sub(p.fetched) < privyJWKSMinRefetch {
		return nil, fmt.Errorf("%w: unknown key %q", ErrPrivyToken, kid)
	}
	keys, err := p.fetchJWKS(ctx)
	if err != nil {
		if ok {
			return k, nil // Privy unreachable: a key we already trust still verifies
		}
		return nil, err
	}
	p.keys, p.fetched = keys, now
	if k, ok = keys[kid]; !ok {
		return nil, fmt.Errorf("%w: unknown key %q", ErrPrivyToken, kid)
	}
	return k, nil
}

type jwk struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Use string `json:"use"`
}

func (p *Privy) fetchJWKS(ctx context.Context) (map[string]*ecdsa.PublicKey, error) {
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := getJSON(ctx, p.client, p.jwksURL, nil, &set); err != nil {
		return nil, fmt.Errorf("privy: fetching signing keys: %w", err)
	}
	out := map[string]*ecdsa.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "EC" || k.Crv != "P-256" || (k.Alg != "" && k.Alg != "ES256") || (k.Use != "" && k.Use != "sig") {
			continue
		}
		pub, err := ecKey(k.X, k.Y)
		if err != nil {
			continue
		}
		out[k.Kid] = pub
	}
	if len(out) == 0 {
		return nil, errors.New("privy: the app's JWKS has no ES256 keys")
	}
	return out, nil
}

// ecKey builds a P-256 public key from a JWK's coordinates, rejecting any
// point not on the curve.
func ecKey(x64, y64 string) (*ecdsa.PublicKey, error) {
	x, err1 := base64.RawURLEncoding.DecodeString(x64)
	y, err2 := base64.RawURLEncoding.DecodeString(y64)
	if err1 != nil || err2 != nil || len(x) != 32 || len(y) != 32 {
		return nil, errors.New("malformed EC key")
	}
	uncompressed := append(append([]byte{4}, x...), y...)
	return ecdsa.ParseUncompressedPublicKey(elliptic.P256(), uncompressed)
}
