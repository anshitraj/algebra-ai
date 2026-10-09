package privacy

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/project-algebra/algebra/internal/domain/shared"
)

// ProfileType distinguishes the two categories of sensitive profile data
// Algebra holds on a user's behalf. Card PAN/CVV is never one of these —
// it never enters Algebra's backend at all (see internal/domain/payment).
type ProfileType string

const (
	ProfileShipping ProfileType = "SHIPPING"
	ProfileBilling  ProfileType = "BILLING"
)

// ShippingProfile is a real delivery address. It is only ever materialized
// inside a resolve call made by merchant-execution code — never serialized
// into an MCP tool result or handed to an agent.
type ShippingProfile struct {
	RecipientName string `json:"recipient_name"`
	Line1         string `json:"line1"`
	Line2         string `json:"line2,omitempty"`
	City          string `json:"city"`
	State         string `json:"state"`
	PostalCode    string `json:"postal_code"`
	Country       string `json:"country"`
	Phone         string `json:"phone"`
}

// BillingProfile is a real billing address (mandate §27: treated as private,
// never sent to an LLM; the payment source references a billing_profile_id
// and the merchant-execution path resolves it directly).
type BillingProfile struct {
	Name       string `json:"name"`
	Line1      string `json:"line1"`
	Line2      string `json:"line2,omitempty"`
	City       string `json:"city"`
	State      string `json:"state"`
	PostalCode string `json:"postal_code"`
	Country    string `json:"country"`
}

// StoredProfile is the encrypted-at-rest representation persisted by a
// Store implementation (internal/platform/postgres). Ciphertext/Nonce come
// from Encryptor; the plaintext JSON never reaches storage or logs.
type StoredProfile struct {
	ID         string
	UserID     string
	Alias      string // e.g. "shipping:home", "payment:personal" (billing)
	Type       ProfileType
	Ciphertext []byte
	Nonce      []byte
	CreatedAt  time.Time
}

// aad binds ciphertext to its row identity so a swapped/replayed ciphertext
// from a different profile can never decrypt successfully under a different
// profile's ID/user/type.
func (s *StoredProfile) aad() []byte {
	return []byte(fmt.Sprintf("%s|%s|%s", s.ID, s.UserID, s.Type))
}

// Store persists encrypted profiles. Aliases are safe to list to an agent;
// the encrypted payload never is.
type Store interface {
	Get(ctx context.Context, userID, alias string) (*StoredProfile, error)
	Put(ctx context.Context, profile *StoredProfile) error
	ListAliases(ctx context.Context, userID string, profileType ProfileType) ([]string, error)
}

// ResolveAuthorization documents WHY a resolution is happening, for the
// audit trail. It is required on every resolve call — there is no
// "resolve without a reason" path.
type ResolveAuthorization struct {
	Purpose     string // e.g. "checkout_execution", "approval_display"
	IntentID    string
	RequestedBy string // agent_id or "system"
}

// AuditSink records that a resolution happened. internal/app wires this to
// the real audit.Logger; kept as a narrow interface here so this package
// doesn't need to import the full audit event shape.
type AuditSink interface {
	RecordResolution(ctx context.Context, userID, alias string, authz ResolveAuthorization) error
}

// Resolver is the ONLY code path from a privacy alias to a real value.
// Nothing else in the codebase should read a Store or Encryptor directly.
type Resolver struct {
	store Store
	enc   Encryptor
	audit AuditSink
}

func NewResolver(store Store, enc Encryptor, audit AuditSink) *Resolver {
	return &Resolver{store: store, enc: enc, audit: audit}
}

func (r *Resolver) ListAliases(ctx context.Context, userID string, profileType ProfileType) ([]string, error) {
	return r.store.ListAliases(ctx, userID, profileType)
}

func (r *Resolver) ResolveShipping(ctx context.Context, userID, alias string, authz ResolveAuthorization) (*ShippingProfile, error) {
	stored, err := r.get(ctx, userID, alias, ProfileShipping)
	if err != nil {
		return nil, err
	}
	plaintext, err := r.enc.Decrypt(stored.Ciphertext, stored.Nonce, stored.aad())
	if err != nil {
		return nil, fmt.Errorf("privacy: resolving shipping profile %q: %w", alias, err)
	}
	var profile ShippingProfile
	if err := json.Unmarshal(plaintext, &profile); err != nil {
		return nil, fmt.Errorf("privacy: decoding shipping profile %q: %w", alias, err)
	}
	if err := r.audit.RecordResolution(ctx, userID, alias, authz); err != nil {
		return nil, fmt.Errorf("privacy: auditing resolution: %w", err)
	}
	return &profile, nil
}

func (r *Resolver) ResolveBilling(ctx context.Context, userID, alias string, authz ResolveAuthorization) (*BillingProfile, error) {
	stored, err := r.get(ctx, userID, alias, ProfileBilling)
	if err != nil {
		return nil, err
	}
	plaintext, err := r.enc.Decrypt(stored.Ciphertext, stored.Nonce, stored.aad())
	if err != nil {
		return nil, fmt.Errorf("privacy: resolving billing profile %q: %w", alias, err)
	}
	var profile BillingProfile
	if err := json.Unmarshal(plaintext, &profile); err != nil {
		return nil, fmt.Errorf("privacy: decoding billing profile %q: %w", alias, err)
	}
	if err := r.audit.RecordResolution(ctx, userID, alias, authz); err != nil {
		return nil, fmt.Errorf("privacy: auditing resolution: %w", err)
	}
	return &profile, nil
}

func (r *Resolver) get(ctx context.Context, userID, alias string, want ProfileType) (*StoredProfile, error) {
	stored, err := r.store.Get(ctx, userID, alias)
	if err != nil {
		return nil, err
	}
	if stored.Type != want {
		return nil, fmt.Errorf("%w: alias %q is a %s profile, not %s", shared.ErrNotFound, alias, stored.Type, want)
	}
	return stored, nil
}

// StoreShipping encrypts and persists a shipping profile under alias. Only
// called from user-facing profile-management code (REST/console), never
// from an agent/MCP path.
func (r *Resolver) StoreShipping(ctx context.Context, id, userID, alias string, profile ShippingProfile, now time.Time) error {
	plaintext, err := json.Marshal(profile)
	if err != nil {
		return fmt.Errorf("privacy: encoding shipping profile: %w", err)
	}
	return r.seal(ctx, id, userID, alias, ProfileShipping, plaintext, now)
}

// StoreBilling encrypts and persists a billing profile under alias.
func (r *Resolver) StoreBilling(ctx context.Context, id, userID, alias string, profile BillingProfile, now time.Time) error {
	plaintext, err := json.Marshal(profile)
	if err != nil {
		return fmt.Errorf("privacy: encoding billing profile: %w", err)
	}
	return r.seal(ctx, id, userID, alias, ProfileBilling, plaintext, now)
}

func (r *Resolver) seal(ctx context.Context, id, userID, alias string, t ProfileType, plaintext []byte, now time.Time) error {
	stored := &StoredProfile{ID: id, UserID: userID, Alias: alias, Type: t, CreatedAt: now}
	ciphertext, nonce, err := r.enc.Encrypt(plaintext, stored.aad())
	if err != nil {
		return fmt.Errorf("privacy: encrypting profile: %w", err)
	}
	stored.Ciphertext = ciphertext
	stored.Nonce = nonce
	return r.store.Put(ctx, stored)
}
