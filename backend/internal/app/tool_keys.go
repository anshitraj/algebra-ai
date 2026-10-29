package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/domain/account"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// A person's own API keys for web tools (TinyFish, Exa, Firecrawl, Tavily).
//
// A saved key makes that vendor's tools candidates for the person's own
// agents: the router prices them like any seller, the Spend Pass's budget and
// rules apply, and the vendor bills the person's own account (providers/keyed).
// Algebra holds the key sealed, shows only its last four characters, never
// returns it, and reads it only to make the owner's calls.

// ToolVendor is a vendor whose tools can be used with a person's key.
type ToolVendor struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Site  string   `json:"site"`
	Tools []string `json:"tools"`
}

// ToolKeyRecord is a saved key as the store holds it. A listing leaves Sealed
// and Nonce empty.
type ToolKeyRecord struct {
	Vendor    string
	Sealed    []byte
	Nonce     []byte
	Hint      string
	CreatedAt time.Time
}

// ToolKeyStore keeps sealed keys by owner and vendor.
type ToolKeyStore interface {
	PutToolKey(ctx context.Context, userID string, k ToolKeyRecord) error
	ToolKey(ctx context.Context, userID, vendor string) (*ToolKeyRecord, error)
	ToolKeys(ctx context.Context, userID string) ([]ToolKeyRecord, error)
	DeleteToolKey(ctx context.Context, userID, vendor string) error
}

// KeySealer seals a key under a key used for nothing else.
type KeySealer interface {
	Encrypt(plaintext, aad []byte) (ciphertext, nonce []byte, err error)
	Decrypt(ciphertext, nonce, aad []byte) ([]byte, error)
}

// ToolKeyView is one vendor as the person sees it. The key is never in it.
type ToolKeyView struct {
	ToolVendor
	Saved bool `json:"saved"`
	// Hint is the last four characters of the saved key.
	Hint    string     `json:"hint,omitempty"`
	SavedAt *time.Time `json:"saved_at,omitempty"`
}

// ToolKeys saves and reads the keys.
type ToolKeys struct {
	store   ToolKeyStore
	sealer  KeySealer
	vendors []ToolVendor
	offers  func(vendor string) []routing.Candidate
	modes   AccountModes
	now     func() time.Time
}

// NewToolKeys builds the service. offers gives a vendor's tools as candidates;
// modes (may be nil) keeps demo accounts from saving keys.
func NewToolKeys(store ToolKeyStore, sealer KeySealer, vendors []ToolVendor, offers func(vendor string) []routing.Candidate, modes AccountModes) *ToolKeys {
	return &ToolKeys{store: store, sealer: sealer, vendors: vendors, offers: offers, modes: modes, now: time.Now}
}

const (
	minToolKey = 8
	maxToolKey = 256
)

// aad binds a sealed key to its owner and vendor, so a row copied to another
// account or vendor opens to nothing.
func aad(userID, vendor string) []byte { return []byte("algebra:tool-key:v1|" + userID + "|" + vendor) }

func (s *ToolKeys) known(vendor string) bool {
	for _, v := range s.vendors {
		if v.ID == vendor {
			return true
		}
	}
	return false
}

// List is every vendor, with whether the person has saved a key for it.
func (s *ToolKeys) List(ctx context.Context, userID string) ([]ToolKeyView, error) {
	saved, err := s.store.ToolKeys(ctx, userID)
	if err != nil {
		return nil, err
	}
	by := make(map[string]ToolKeyRecord, len(saved))
	for _, k := range saved {
		by[k.Vendor] = k
	}
	out := make([]ToolKeyView, 0, len(s.vendors))
	for _, v := range s.vendors {
		view := ToolKeyView{ToolVendor: v}
		if k, ok := by[v.ID]; ok {
			at := k.CreatedAt
			view.Saved, view.Hint, view.SavedAt = true, k.Hint, &at
		}
		out = append(out, view)
	}
	return out, nil
}

// Save seals and keeps a key, replacing any saved before. The key is checked
// only for shape; the vendor says whether it works on the first call.
func (s *ToolKeys) Save(ctx context.Context, userID, vendor, key string) error {
	vendor = strings.ToLower(strings.TrimSpace(vendor))
	if !s.known(vendor) {
		return fmt.Errorf("%w: %q is not a vendor Algebra can use a key for", ErrInvalidRequest, vendor)
	}
	key = strings.TrimSpace(key)
	if len(key) < minToolKey || len(key) > maxToolKey || strings.IndexFunc(key, func(r rune) bool { return r <= ' ' || r > '~' }) >= 0 {
		return fmt.Errorf("%w: that doesn't look like an API key (%d to %d printable characters, no spaces)", ErrInvalidRequest, minToolKey, maxToolKey)
	}
	if s.modes != nil {
		mode, err := s.modes.UserMode(ctx, userID)
		if err != nil {
			return fmt.Errorf("checking the account's mode: %w", err)
		}
		if mode == account.ModeDemo {
			return fmt.Errorf("%w: a demo account can't save API keys; sign in with your email to use your own", shared.ErrUnauthorized)
		}
	}
	sealed, nonce, err := s.sealer.Encrypt([]byte(key), aad(userID, vendor))
	if err != nil {
		return fmt.Errorf("sealing the key: %w", err)
	}
	hint := ""
	if len(key) >= 12 {
		hint = key[len(key)-4:]
	}
	return s.store.PutToolKey(ctx, userID, ToolKeyRecord{Vendor: vendor, Sealed: sealed, Nonce: nonce, Hint: hint, CreatedAt: s.now().UTC()})
}

// Remove deletes a saved key.
func (s *ToolKeys) Remove(ctx context.Context, userID, vendor string) error {
	vendor = strings.ToLower(strings.TrimSpace(vendor))
	if !s.known(vendor) {
		return fmt.Errorf("%w: vendor %s", shared.ErrNotFound, vendor)
	}
	return s.store.DeleteToolKey(ctx, userID, vendor)
}

// Key opens the person's key for a vendor. It is for the keyed runner and
// nothing else; the error wraps shared.ErrNotFound when none is saved.
func (s *ToolKeys) Key(ctx context.Context, userID, vendor string) (string, error) {
	k, err := s.store.ToolKey(ctx, userID, vendor)
	if err != nil {
		return "", err
	}
	plain, err := s.sealer.Decrypt(k.Sealed, k.Nonce, aad(userID, vendor))
	if err != nil {
		return "", errors.New("app: a saved tool key could not be opened")
	}
	return string(plain), nil
}

// CandidatesFor is the tools of every vendor the person has a key for that do
// the capability. It is nil, never an error, when they have none or the store
// can't be read: a missing tool must not stop a purchase from other sellers.
func (s *ToolKeys) CandidatesFor(ctx context.Context, userID, capability string) []routing.Candidate {
	if userID == "" || s.offers == nil {
		return nil
	}
	saved, err := s.store.ToolKeys(ctx, userID)
	if err != nil {
		return nil
	}
	var out []routing.Candidate
	for _, k := range saved {
		for _, c := range s.offers(k.Vendor) {
			if c.Capability == capability {
				out = append(out, c)
			}
		}
	}
	return out
}
