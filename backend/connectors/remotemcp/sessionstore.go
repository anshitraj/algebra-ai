// Package remotemcp is the shared client for merchants that publish an
// official, hosted remote MCP server (Zepto, Swiggy Instamart). It owns the
// three things every such connector needs and none should re-implement:
//
//   - Linking (Login): a one-time, user-driven OAuth 2.1 authorization-code
//     flow with PKCE and dynamic client registration, run from
//     cmd/merchant-login. The user types their phone number and OTP into the
//     MERCHANT'S OWN consent page, in their own browser. Algebra only ever
//     receives the resulting authorization code on a loopback redirect — it
//     never sees, relays, stores, or automates the OTP (mandate §23/§28).
//   - Session storage (FileSessionStore): the resulting tokens, encrypted at
//     rest with AES-256-GCM and bound (AAD) to the merchant name.
//   - Calling (Client): an MCP client session that injects the bearer token,
//     refreshes it, and decodes tool results — without ever putting a token
//     into a tool argument, a log line, or anything an agent can read.
//
// Scope of this build: one linked account per merchant (the operator's own).
// Per-user merchant sessions need a merchant_sessions table keyed by user and
// connector methods that carry the user through; see
// docs/legacy/MERCHANT_CONNECTORS.md.
package remotemcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// ErrNotLinked means nobody has linked this merchant account yet (or the
// link was removed). It is a normal state — connectors translate it into
// all-false capabilities and USER_INTERVENTION_REQUIRED, never into a
// fabricated response.
var ErrNotLinked = errors.New("remotemcp: merchant account not linked")

// Session is everything needed to call a merchant's MCP server on behalf of
// the user who linked it.
type Session struct {
	Merchant string `json:"merchant"`
	Endpoint string `json:"endpoint"`
	Issuer   string `json:"issuer,omitempty"`
	ClientID string `json:"client_id"`
	// ClientSecret is set only if the authorization server insisted on a
	// confidential client during dynamic registration. Algebra asks for a
	// public client ("none") first.
	ClientSecret string `json:"client_secret,omitempty"`
	AuthURL      string `json:"auth_url"`
	TokenURL     string `json:"token_url"`
	// AuthStyle is how the token endpoint expects client authentication, so
	// refreshes use the method registration negotiated.
	AuthStyle oauth2.AuthStyle `json:"auth_style"`
	Scopes    []string         `json:"scopes,omitempty"`
	Resource  string           `json:"resource,omitempty"`
	Token     *oauth2.Token    `json:"token"`
	// Settings holds non-secret choices the user made while linking, e.g.
	// which of their saved Swiggy address IDs Algebra's "shipping:home"
	// alias maps to. Opaque merchant IDs only — never an address, phone
	// number, or name.
	Settings map[string]string `json:"settings,omitempty"`
	LinkedAt time.Time         `json:"linked_at"`
}

// SessionStore persists linked sessions.
type SessionStore interface {
	Load(merchant string) (*Session, error)
	Save(sess *Session) error
}

// Encryptor is satisfied by internal/domain/privacy.AESGCMEncryptor. It is
// declared here so this package never imports the privacy storage domain.
type Encryptor interface {
	Encrypt(plaintext, aad []byte) (ciphertext, nonce []byte, err error)
	Decrypt(ciphertext, nonce, aad []byte) ([]byte, error)
}

// FileSessionStore keeps one encrypted file per merchant under dir. A file
// store (rather than Postgres) is deliberate for this build: linking runs
// from a CLI that must work before the API server is up, and there is
// exactly one linked account per merchant.
type FileSessionStore struct {
	dir string
	enc Encryptor
	mu  sync.Mutex
}

func NewFileSessionStore(dir string, enc Encryptor) *FileSessionStore {
	return &FileSessionStore{dir: dir, enc: enc}
}

var merchantNameRE = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)

type envelope struct {
	Version    int    `json:"v"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

// sessionAAD binds ciphertext to its merchant: a Zepto session file renamed
// to swiggy_instamart.session fails authentication instead of decrypting.
func sessionAAD(merchant string) []byte {
	return []byte("algebra/remotemcp/session/v1:" + merchant)
}

func (s *FileSessionStore) path(merchant string) (string, error) {
	if !merchantNameRE.MatchString(merchant) {
		return "", fmt.Errorf("remotemcp: invalid merchant name %q", merchant)
	}
	return filepath.Join(s.dir, merchant+".session"), nil
}

// Linked reports whether a session file exists, without decrypting it.
func (s *FileSessionStore) Linked(merchant string) bool {
	p, err := s.path(merchant)
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

func (s *FileSessionStore) Load(merchant string) (*Session, error) {
	p, err := s.path(merchant)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotLinked
	}
	if err != nil {
		return nil, fmt.Errorf("remotemcp: reading session for %s: %w", merchant, err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil || env.Version != 1 {
		return nil, fmt.Errorf("remotemcp: session file for %s is malformed", merchant)
	}
	plain, err := s.enc.Decrypt(env.Ciphertext, env.Nonce, sessionAAD(merchant))
	if err != nil {
		// Deliberately no detail: wrong key, tampering, and a file moved
		// between merchants all look the same from outside.
		return nil, fmt.Errorf("remotemcp: session file for %s could not be decrypted (wrong ALGEBRA_MASTER_KEY or modified file); re-link the account", merchant)
	}
	var sess Session
	if err := json.Unmarshal(plain, &sess); err != nil {
		return nil, fmt.Errorf("remotemcp: session for %s is malformed", merchant)
	}
	if sess.Merchant != merchant || sess.Token == nil {
		return nil, fmt.Errorf("remotemcp: session file for %s is inconsistent; re-link the account", merchant)
	}
	return &sess, nil
}

func (s *FileSessionStore) Save(sess *Session) error {
	p, err := s.path(sess.Merchant)
	if err != nil {
		return err
	}
	plain, err := json.Marshal(sess)
	if err != nil {
		return fmt.Errorf("remotemcp: encoding session: %w", err)
	}
	ct, nonce, err := s.enc.Encrypt(plain, sessionAAD(sess.Merchant))
	if err != nil {
		return fmt.Errorf("remotemcp: encrypting session: %w", err)
	}
	data, err := json.Marshal(envelope{Version: 1, Nonce: nonce, Ciphertext: ct})
	if err != nil {
		return fmt.Errorf("remotemcp: encoding session envelope: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("remotemcp: creating session dir: %w", err)
	}
	tmp, err := os.CreateTemp(s.dir, sess.Merchant+".*.tmp")
	if err != nil {
		return fmt.Errorf("remotemcp: writing session: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("remotemcp: writing session: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("remotemcp: writing session: %w", err)
	}
	_ = os.Chmod(tmpName, 0o600) // best-effort; Windows ignores POSIX modes
	if err := os.Rename(tmpName, p); err != nil {
		return fmt.Errorf("remotemcp: replacing session file: %w", err)
	}
	return nil
}

// Delete unlinks a merchant. Deleting a session that doesn't exist is not an
// error.
func (s *FileSessionStore) Delete(merchant string) error {
	p, err := s.path(merchant)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remotemcp: deleting session for %s: %w", merchant, err)
	}
	return nil
}

var _ SessionStore = (*FileSessionStore)(nil)
