package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/payment"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// fakeCardVault is a no-op payment.CardVaultProvider — these tests exercise
// PaymentService's ownership check (getOwned), not vault integration
// (already covered by providers/vault/sandbox_test.go), so RevokeSource
// always succeeds rather than tracking its own set of minted source IDs the
// way the real sandbox provider does.
type fakeCardVault struct{}

func (fakeCardVault) Mode() payment.ProviderMode { return payment.ProviderModeSandbox }
func (fakeCardVault) Tokenize(context.Context, payment.TokenizeRequest) (*payment.TokenizeResult, error) {
	return nil, errors.New("fakeCardVault: Tokenize is not exercised by these tests")
}
func (fakeCardVault) ListSources(context.Context, string) ([]payment.PaymentSource, error) {
	return nil, nil
}
func (fakeCardVault) RevokeSource(context.Context, string) error { return nil }

// fakePaymentSourceStore is a minimal in-memory PaymentSourceStore, in the
// same style as fakeAgentStore — PaymentService has no shared test harness
// today (unlike IntentService/OrderService/etc via internal/app's
// orchestration_test.go), so this test file builds its own.
type fakePaymentSourceStore struct {
	mu   sync.Mutex
	data map[string]*payment.PaymentSource
}

func newFakePaymentSourceStore() *fakePaymentSourceStore {
	return &fakePaymentSourceStore{data: map[string]*payment.PaymentSource{}}
}

func (f *fakePaymentSourceStore) put(s *payment.PaymentSource) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[s.ID] = s
}

func (f *fakePaymentSourceStore) List(_ context.Context, userID string) ([]payment.PaymentSource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []payment.PaymentSource
	for _, s := range f.data {
		if s.UserID == userID {
			out = append(out, *s)
		}
	}
	return out, nil
}

func (f *fakePaymentSourceStore) GetByID(_ context.Context, id string) (*payment.PaymentSource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.data[id]
	if !ok {
		return nil, shared.ErrNotFound
	}
	return s, nil
}

func (f *fakePaymentSourceStore) GetByAlias(_ context.Context, userID, alias string) (*payment.PaymentSource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.data {
		if s.UserID == userID && s.Alias == alias {
			return s, nil
		}
	}
	return nil, shared.ErrNotFound
}

func (f *fakePaymentSourceStore) Create(_ context.Context, s *payment.PaymentSource) error {
	f.put(s)
	return nil
}

func (f *fakePaymentSourceStore) Revoke(_ context.Context, id string, revokedAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.data[id]
	if !ok {
		return shared.ErrNotFound
	}
	s.RevokedAt = &revokedAt
	return nil
}

func TestPaymentService_RevokeSource_OwnerSucceeds(t *testing.T) {
	store := newFakePaymentSourceStore()
	store.put(&payment.PaymentSource{ID: "src_1", UserID: "user_1", Alias: "payment:personal", CreatedAt: time.Now()})
	svc := NewPaymentService(store, nil, fakeCardVault{})

	if err := svc.RevokeSource(context.Background(), "user_1", "src_1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, err := store.GetByID(context.Background(), "src_1")
	if err != nil {
		t.Fatalf("unexpected error re-fetching: %v", err)
	}
	if got.RevokedAt == nil {
		t.Error("expected src_1 to be revoked")
	}
}

// TestPaymentService_RevokeSource_CrossUserDenied is the regression test for
// the bug found while wiring up a browser frontend: revokePaymentSource
// previously checked only that X-User-ID was non-empty, never that the
// target source actually belonged to that user. See
// internal/api/v1/payments.go and PaymentService.getOwned's doc comment.
func TestPaymentService_RevokeSource_CrossUserDenied(t *testing.T) {
	store := newFakePaymentSourceStore()
	store.put(&payment.PaymentSource{ID: "src_1", UserID: "user_1", Alias: "payment:personal", CreatedAt: time.Now()})
	svc := NewPaymentService(store, nil, fakeCardVault{})

	err := svc.RevokeSource(context.Background(), "user_2", "src_1")
	if err == nil {
		t.Fatal("expected an error when user_2 tries to revoke user_1's payment source")
	}
	if !errors.Is(err, shared.ErrUnauthorized) {
		t.Errorf("expected shared.ErrUnauthorized, got %v", err)
	}
	got, getErr := store.GetByID(context.Background(), "src_1")
	if getErr != nil {
		t.Fatalf("unexpected error re-fetching: %v", getErr)
	}
	if got.RevokedAt != nil {
		t.Error("src_1 must still be active — the cross-user revoke must not have taken effect")
	}
}

func TestPaymentService_RevokeSource_UnknownSourceNotFound(t *testing.T) {
	store := newFakePaymentSourceStore()
	svc := NewPaymentService(store, nil, fakeCardVault{})

	err := svc.RevokeSource(context.Background(), "user_1", "does-not-exist")
	if !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("expected shared.ErrNotFound, got %v", err)
	}
}
