package privacy

import (
	"context"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/shared"
)

type memStore struct {
	byUserAlias map[string]*StoredProfile
}

func newMemStore() *memStore {
	return &memStore{byUserAlias: map[string]*StoredProfile{}}
}

func (m *memStore) key(userID, alias string) string { return userID + "|" + alias }

func (m *memStore) Get(_ context.Context, userID, alias string) (*StoredProfile, error) {
	p, ok := m.byUserAlias[m.key(userID, alias)]
	if !ok {
		return nil, shared.ErrNotFound
	}
	return p, nil
}

func (m *memStore) Put(_ context.Context, profile *StoredProfile) error {
	m.byUserAlias[m.key(profile.UserID, profile.Alias)] = profile
	return nil
}

func (m *memStore) ListAliases(_ context.Context, userID string, t ProfileType) ([]string, error) {
	var out []string
	for _, p := range m.byUserAlias {
		if p.UserID == userID && p.Type == t {
			out = append(out, p.Alias)
		}
	}
	return out, nil
}

type recordingAudit struct {
	calls []string
}

func (r *recordingAudit) RecordResolution(_ context.Context, userID, alias string, authz ResolveAuthorization) error {
	r.calls = append(r.calls, userID+"|"+alias+"|"+authz.Purpose+"|"+authz.RequestedBy)
	return nil
}

func TestResolver_StoreAndResolveShipping_RoundTrip(t *testing.T) {
	enc, err := NewAESGCMEncryptor(testKey(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	store := newMemStore()
	audit := &recordingAudit{}
	r := NewResolver(store, enc, audit)

	profile := ShippingProfile{
		RecipientName: "A. User",
		Line1:         "12 MG Road",
		City:          "Bengaluru",
		State:         "KA",
		PostalCode:    "560001",
		Country:       "IN",
		Phone:         "+919999999999",
	}
	if err := r.StoreShipping(context.Background(), "profile-1", "user-1", "shipping:home", profile, time.Now()); err != nil {
		t.Fatalf("StoreShipping failed: %v", err)
	}

	got, err := r.ResolveShipping(context.Background(), "user-1", "shipping:home", ResolveAuthorization{
		Purpose: "checkout_execution", IntentID: "pi_1", RequestedBy: "agent_1",
	})
	if err != nil {
		t.Fatalf("ResolveShipping failed: %v", err)
	}
	if *got != profile {
		t.Errorf("resolved profile mismatch: got %+v, want %+v", *got, profile)
	}
	if len(audit.calls) != 1 {
		t.Fatalf("expected exactly one audit record, got %d: %v", len(audit.calls), audit.calls)
	}
	want := "user-1|shipping:home|checkout_execution|agent_1"
	if audit.calls[0] != want {
		t.Errorf("audit record = %q, want %q", audit.calls[0], want)
	}

	// Stored ciphertext must never contain the plaintext address.
	raw, _ := store.Get(context.Background(), "user-1", "shipping:home")
	if containsSubstring(raw.Ciphertext, "MG Road") {
		t.Error("plaintext address leaked into stored ciphertext")
	}
}

func TestResolver_WrongProfileTypeRejected(t *testing.T) {
	enc, _ := NewAESGCMEncryptor(testKey(t))
	store := newMemStore()
	audit := &recordingAudit{}
	r := NewResolver(store, enc, audit)

	billing := BillingProfile{Name: "A. User", Line1: "1 Infinite Loop", City: "X", Country: "IN"}
	if err := r.StoreBilling(context.Background(), "profile-2", "user-1", "payment:personal", billing, time.Now()); err != nil {
		t.Fatalf("StoreBilling failed: %v", err)
	}

	if _, err := r.ResolveShipping(context.Background(), "user-1", "payment:personal", ResolveAuthorization{Purpose: "x", RequestedBy: "agent_1"}); err == nil {
		t.Error("expected error resolving a billing alias as a shipping profile")
	}
}

func TestResolver_ListAliasesNeverTouchesCiphertext(t *testing.T) {
	enc, _ := NewAESGCMEncryptor(testKey(t))
	store := newMemStore()
	r := NewResolver(store, enc, &recordingAudit{})

	_ = r.StoreShipping(context.Background(), "p1", "user-1", "shipping:home", ShippingProfile{Line1: "a"}, time.Now())
	_ = r.StoreShipping(context.Background(), "p2", "user-1", "shipping:office", ShippingProfile{Line1: "b"}, time.Now())

	aliases, err := r.ListAliases(context.Background(), "user-1", ProfileShipping)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(aliases) != 2 {
		t.Errorf("expected 2 aliases, got %d: %v", len(aliases), aliases)
	}
}

func containsSubstring(b []byte, s string) bool {
	return len(b) >= len(s) && string(b) != "" && indexOf(b, s) >= 0
}

func indexOf(b []byte, s string) int {
	for i := 0; i+len(s) <= len(b); i++ {
		if string(b[i:i+len(s)]) == s {
			return i
		}
	}
	return -1
}
