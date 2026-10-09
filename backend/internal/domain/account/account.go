// Package account holds the human side of identity: how a person proves who
// they are (email + password, or a Google/GitHub OAuth sign-in) and the
// browser session that results. It is deliberately separate from
// internal/domain/agent — a session belongs to a human and can approve
// spend; an agent token never can, and nothing here ever hands an agent a
// session.
package account

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// ErrInvalidCredentials is returned for every failed password sign-in,
// regardless of whether the email exists — never reveal which half was
// wrong.
var ErrInvalidCredentials = errors.New("invalid email or password")

// ErrEmailTaken is returned when signing up with an email that already has
// an account.
var ErrEmailTaken = errors.New("an account with this email already exists")

// ErrSessionInvalid covers a missing, expired, or revoked session token.
var ErrSessionInvalid = errors.New("session is invalid or expired")

const (
	MinPasswordLength = 8
	MaxPasswordLength = 128
)

// NormalizeEmail lowercases and trims an email and checks it parses as a
// single bare address. Emails are compared case-insensitively everywhere
// in this package.
func NormalizeEmail(raw string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(raw))
	if e == "" {
		return "", errors.New("email is required")
	}
	addr, err := mail.ParseAddress(e)
	if err != nil || addr.Address != e || !strings.Contains(e[strings.LastIndex(e, "@")+1:], ".") {
		return "", errors.New("enter a valid email address")
	}
	return e, nil
}

// ValidatePassword enforces length only — NIST 800-63B guidance: long
// passphrases beat composition rules.
func ValidatePassword(pw string) error {
	n := utf8.RuneCountInString(pw)
	if n < MinPasswordLength {
		return fmt.Errorf("password must be at least %d characters", MinPasswordLength)
	}
	if n > MaxPasswordLength {
		return fmt.Errorf("password must be at most %d characters", MaxPasswordLength)
	}
	return nil
}

// argon2id parameters — OWASP's second recommended configuration
// (m=64 MiB, t=3, p=2). Encoded into every hash so they can be raised
// later without invalidating existing hashes.
const (
	argonMemoryKiB = 64 * 1024
	argonTime      = 3
	argonThreads   = 2
	argonKeyLen    = 32
	argonSaltLen   = 16
)

// HashPassword returns a PHC-format argon2id hash:
// $argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash>
func HashPassword(pw string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("account: generating salt: %w", err)
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemoryKiB, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword reports whether pw matches encoded, in constant time with
// respect to the derived key.
func VerifyPassword(encoded, pw string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var mem uint32
	var t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &t, &p); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, mem, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is verified against when an email has no account (or no
// password), so a failed sign-in takes the same time either way and can't
// be used to enumerate registered emails.
var dummyHash, _ = HashPassword("algebra-timing-equalizer")

// VerifyPasswordOrDummy runs a full verification even when encoded is empty.
func VerifyPasswordOrDummy(encoded, pw string) bool {
	if encoded == "" {
		VerifyPassword(dummyHash, pw)
		return false
	}
	return VerifyPassword(encoded, pw)
}

const (
	sessionTokenPrefix = "alg_sess_"
	resetTokenPrefix   = "alg_reset_"
)

// NewSessionToken returns a raw session token (set in an HttpOnly cookie,
// never persisted) and its SHA-256 hex digest (what the database stores).
func NewSessionToken() (raw, hash string, err error) { return newToken(sessionTokenPrefix) }

// NewResetToken is NewSessionToken's counterpart for password-reset links.
func NewResetToken() (raw, hash string, err error) { return newToken(resetTokenPrefix) }

func newToken(prefix string) (string, string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("account: generating token: %w", err)
	}
	raw := prefix + base64.RawURLEncoding.EncodeToString(buf)
	return raw, HashToken(raw), nil
}

// HashToken is the SHA-256 hex digest used to persist and look up tokens.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// Mode separates the two kinds of account. A live account shops for real:
// only real merchants, never a demo or test store. A demo account (one
// click from the sign-in page, no signup) shops real product listings but
// checks out through a simulated store with fake money, so anyone can see
// the whole flow end to end without spending anything.
type Mode string

const (
	ModeLive Mode = "live"
	ModeDemo Mode = "demo"
)

// User is a person with an account on Algebra's own first-party app.
type User struct {
	ID              string     `json:"id"`
	Email           string     `json:"email"`
	Name            string     `json:"name,omitempty"`
	AvatarURL       string     `json:"avatar_url,omitempty"`
	PasswordHash    string     `json:"-"`
	EmailVerifiedAt *time.Time `json:"email_verified_at,omitempty"`
	OnboardedAt     *time.Time `json:"onboarded_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	// Mode is ModeLive unless the account was created as a demo.
	Mode Mode `json:"mode"`
}

// IsDemo reports whether this is a demo account.
func (u *User) IsDemo() bool { return u.Mode == ModeDemo }

// HasPassword reports whether the user can sign in with email + password
// (an OAuth-only account has no password until they set one via reset).
func (u *User) HasPassword() bool { return u.PasswordHash != "" }

// Session is a signed-in browser. AgentID is the per-session console agent
// the web app acts through for agent-scoped endpoints — revoked together
// with the session, so signing out also kills every token the session
// minted.
type Session struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	TokenHash  string     `json:"-"`
	AgentID    string     `json:"-"`
	UserAgent  string     `json:"user_agent,omitempty"`
	IP         string     `json:"ip,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	LastSeenAt time.Time  `json:"last_seen_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`

	// AgentTokenCiphertext/AgentTokenNonce hold the console agent's bearer
	// token, AES-256-GCM encrypted under the master key, so the web app's
	// server-side LLM loop can act as the agent without ever holding the
	// human session itself. Never serialized.
	AgentTokenCiphertext []byte `json:"-"`
	AgentTokenNonce      []byte `json:"-"`
}

// Active reports whether the session can still be used at now.
func (s *Session) Active(now time.Time) bool {
	return s.RevokedAt == nil && now.Before(s.ExpiresAt)
}

// Wallet is a wallet a person signed in with, or was given when they signed
// in (Privy makes an embedded Solana wallet for anyone who has none). Only
// the public address: Algebra never holds the key.
type Wallet struct {
	Chain   string `json:"chain"`
	Address string `json:"address"`
	// Kind is WalletEmbedded (Privy holds the key for the person) or
	// WalletExternal (the person's own, e.g. Phantom).
	Kind   string `json:"kind"`
	Source string `json:"source"`
}

const (
	ChainSolana    = "solana"
	WalletEmbedded = "embedded"
	WalletExternal = "external"
)

// WalletEmailDomain is the undeliverable domain of the address an account
// gets when its person signed in with a wallet and no email: the account
// needs a unique email, and this one can't be mistaken for, or linked to, a
// real one.
const WalletEmailDomain = "wallet.algebra.invalid"

// OAuthProfile is what a provider told us about the person after a
// successful authorization-code exchange.
type OAuthProfile struct {
	Provider       string
	ProviderUserID string
	Email          string
	EmailVerified  bool
	Name           string
	AvatarURL      string
}

// Validate rejects a profile that can't safely become an account.
func (p OAuthProfile) Validate() error {
	if p.Provider == "" || p.ProviderUserID == "" {
		return errors.New("oauth profile is missing provider identity")
	}
	if p.Email == "" {
		return errors.New("your account has no email address we can use — add a public or verified email and try again")
	}
	if !p.EmailVerified {
		return errors.New("your email address isn't verified with this provider — verify it and try again")
	}
	return nil
}

// ClientMeta is request context stored on a session for the user's own
// "where am I signed in" view.
type ClientMeta struct {
	UserAgent string
	IP        string
}
