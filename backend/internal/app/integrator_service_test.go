package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/integrator"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// fakeIntegratorStore is a minimal in-memory IntegratorStore, same style as
// fakeAgentStore/fakePaymentSourceStore — shared by integrator_service_test.go
// and transaction_policy_service_test.go.
type fakeIntegratorStore struct {
	mu   sync.Mutex
	data map[string]*integrator.Integrator
}

func newFakeIntegratorStore() *fakeIntegratorStore {
	return &fakeIntegratorStore{data: map[string]*integrator.Integrator{}}
}

func (f *fakeIntegratorStore) put(i *integrator.Integrator) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[i.ID] = i
}

func (f *fakeIntegratorStore) Get(_ context.Context, id string) (*integrator.Integrator, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i, ok := f.data[id]
	if !ok {
		return nil, shared.ErrNotFound
	}
	return i, nil
}

func (f *fakeIntegratorStore) GetByTokenHash(_ context.Context, hash string) (*integrator.Integrator, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, i := range f.data {
		if i.TokenHash == hash {
			return i, nil
		}
	}
	return nil, shared.ErrNotFound
}

func (f *fakeIntegratorStore) Create(_ context.Context, i *integrator.Integrator) error {
	f.put(i)
	return nil
}

func (f *fakeIntegratorStore) Revoke(_ context.Context, id string, revokedAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	i, ok := f.data[id]
	if !ok {
		return shared.ErrNotFound
	}
	i.RevokedAt = &revokedAt
	return nil
}

func TestIntegratorService_CreateIntegrator(t *testing.T) {
	store := newFakeIntegratorStore()
	svc := NewIntegratorService(store)

	token, integ, err := svc.CreateIntegrator(context.Background(), "Kite")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token == "" {
		t.Error("expected a non-empty raw token")
	}
	if integ.Name != "Kite" {
		t.Errorf("expected name Kite, got %q", integ.Name)
	}
	if integ.IsRevoked() {
		t.Error("expected a freshly created integrator to not be revoked")
	}

	// The raw token must never be persisted — only its hash.
	stored, err := store.Get(context.Background(), integ.ID)
	if err != nil {
		t.Fatalf("unexpected error re-fetching: %v", err)
	}
	if stored.TokenHash == token {
		t.Error("expected TokenHash to be a hash, not the raw token")
	}
}

func TestIntegratorService_Revoke(t *testing.T) {
	store := newFakeIntegratorStore()
	svc := NewIntegratorService(store)

	_, integ, err := svc.CreateIntegrator(context.Background(), "Kite")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := svc.Revoke(context.Background(), integ.ID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, err := store.Get(context.Background(), integ.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.IsRevoked() {
		t.Error("expected integrator to be revoked")
	}
}

func TestIntegratorService_Revoke_UnknownNotFound(t *testing.T) {
	store := newFakeIntegratorStore()
	svc := NewIntegratorService(store)

	err := svc.Revoke(context.Background(), "does-not-exist")
	if !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("expected shared.ErrNotFound, got %v", err)
	}
}
