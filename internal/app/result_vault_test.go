package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/privacy"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// memResultStore is an in-memory ResultBlobStore.
type memResultStore struct {
	mu   sync.Mutex
	rows map[string]SealedResult
}

func newMemResultStore() *memResultStore { return &memResultStore{rows: map[string]SealedResult{}} }

func (m *memResultStore) SaveSealed(_ context.Context, r SealedResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[r.IntentID] = r
	return nil
}

func (m *memResultStore) GetSealed(_ context.Context, intentID string, now time.Time) (*SealedResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[intentID]
	if !ok || !r.ExpiresAt.After(now) {
		return nil, shared.ErrNotFound
	}
	return &r, nil
}

func (m *memResultStore) DeleteExpired(_ context.Context, now time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for id, r := range m.rows {
		if !r.ExpiresAt.After(now) {
			delete(m.rows, id)
			n++
		}
	}
	return n, nil
}

func testVault(t *testing.T, retention time.Duration, maxBytes int) (*ResultVault, *memResultStore, *time.Time) {
	t.Helper()
	enc, err := privacy.NewAESGCMEncryptor(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := newMemResultStore()
	v := NewResultVault(store, enc, retention, maxBytes)
	clock := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	v.now = func() time.Time { return clock }
	return v, store, &clock
}

func TestResultVaultKeepsSealedAndReturnsWhatWasKept(t *testing.T) {
	ctx := context.Background()
	v, store, _ := testVault(t, time.Hour, 0)
	body := []byte(`{"risk_score":12,"marker":"NOT-IN-THE-CLEAR"}`)

	k, err := v.Keep(ctx, "user_1", "eint_1", "rsv_1", "application/json", 200, body)
	if err != nil || !k.Stored || k.Size != len(body) || k.ExpiresAt != time.Date(2026, 10, 7, 13, 0, 0, 0, time.UTC) {
		t.Fatalf("kept: %+v %v", k, err)
	}
	row := store.rows["eint_1"]
	if bytes.Contains(row.Sealed, []byte("NOT-IN-THE-CLEAR")) || row.Size != len(body) || !strings.HasPrefix(row.SHA256, "sha256:") {
		t.Errorf("what the store holds is sealed: %+v", row)
	}
	got, err := v.Get(ctx, "user_1", "eint_1")
	if err != nil || !bytes.Equal(got.Body, body) || got.ReservationID != "rsv_1" || got.ContentType != "application/json" || got.HTTPStatus != 200 || got.SHA256 != row.SHA256 {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := v.Get(ctx, "user_2", "eint_1"); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("another person's result is not found: %v", err)
	}
}

func TestResultVaultRefusesWhatItShouldNotKeep(t *testing.T) {
	ctx := context.Background()
	v, store, _ := testVault(t, time.Hour, 16)

	if k, err := v.Keep(ctx, "u", "i1", "r", "text/plain", 200, []byte("0123456789abcdef!")); err != nil || k.Stored || k.Reason != "too_large" || k.Size != 17 {
		t.Errorf("over the limit is not kept, not truncated: %+v %v", k, err)
	}
	if k, err := v.Keep(ctx, "u", "i2", "r", "text/plain", 200, nil); err != nil || k.Stored || k.Reason != "empty" {
		t.Errorf("an empty answer is not kept: %+v %v", k, err)
	}
	if len(store.rows) != 0 {
		t.Errorf("nothing reached the store: %d", len(store.rows))
	}
	// Off means off, including for a nil vault.
	off, _, _ := testVault(t, 0, 0)
	if k, err := off.Keep(ctx, "u", "i", "r", "", 200, []byte("x")); err != nil || k.Stored || k.Reason != "disabled" || off.Enabled() {
		t.Errorf("retention zero keeps nothing: %+v %v", k, err)
	}
	var none *ResultVault
	if none.Enabled() {
		t.Error("a nil vault is off")
	}
	if _, err := none.Get(ctx, "u", "i"); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("a nil vault has nothing: %v", err)
	}
	if n, err := none.Purge(ctx); err != nil || n != 0 {
		t.Errorf("purging a nil vault: %d %v", n, err)
	}
}

func TestResultVaultExpiresAndPurges(t *testing.T) {
	ctx := context.Background()
	v, store, clock := testVault(t, time.Hour, 0)
	if _, err := v.Keep(ctx, "u", "i", "r", "text/plain", 200, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	*clock = clock.Add(59 * time.Minute)
	if _, err := v.Get(ctx, "u", "i"); err != nil {
		t.Errorf("still kept after 59 minutes: %v", err)
	}
	*clock = clock.Add(2 * time.Minute)
	if _, err := v.Get(ctx, "u", "i"); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("gone after the hour: %v", err)
	}
	if n, err := v.Purge(ctx); err != nil || n != 1 || len(store.rows) != 0 {
		t.Errorf("purge: %d %v rows=%d", n, err, len(store.rows))
	}
}

func TestResultVaultNoticesTampering(t *testing.T) {
	ctx := context.Background()
	v, store, _ := testVault(t, time.Hour, 0)
	if _, err := v.Keep(ctx, "u", "i", "r", "text/plain", 200, []byte("the real answer")); err != nil {
		t.Fatal(err)
	}
	row := store.rows["i"]

	// The ciphertext changed.
	bad := row
	bad.Sealed = append([]byte(nil), row.Sealed...)
	bad.Sealed[0] ^= 1
	store.rows["i"] = bad
	if _, err := v.Get(ctx, "u", "i"); err == nil {
		t.Error("a changed ciphertext must not open")
	}
	// The row moved to another attempt: the additional data differs.
	moved := row
	moved.ReservationID = "r2"
	store.rows["i"] = moved
	if _, err := v.Get(ctx, "u", "i"); err == nil {
		t.Error("a result moved to another attempt must not open")
	}
	// The stored hash was changed to match something else.
	rehashed := row
	rehashed.SHA256 = "sha256:0000"
	store.rows["i"] = rehashed
	if _, err := v.Get(ctx, "u", "i"); err == nil {
		t.Error("a body that doesn't match its hash is never returned")
	}
}
