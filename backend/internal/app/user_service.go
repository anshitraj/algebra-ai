package app

import (
	"context"
	"time"
)

// UserRecord is intentionally minimal — full user profile management is
// out of scope for Phase 1 (no OIDC provider is configured in this
// environment; see docs/LOCAL_DEVELOPMENT.md). This exists so agents and
// payment sources have a real users(id) row to reference via foreign key,
// not to be a user-management system in its own right.
type UserRecord struct {
	ID    string
	Email string
	// TenantID is "" for Algebra's own first-party reference app (the
	// existing frontend/ dev-mode console) — set when this user was provisioned
	// by a tenant integration. See internal/domain/tenant's package doc.
	TenantID  string
	CreatedAt time.Time
}

type UserStore interface {
	Create(ctx context.Context, id, email, tenantID string, createdAt time.Time) error
	Get(ctx context.Context, id string) (*UserRecord, error)
}

type UserService struct {
	store UserStore
	now   func() time.Time
}

func NewUserService(store UserStore) *UserService {
	return &UserService{store: store, now: time.Now}
}

// Create provisions a user. tenantID is "" for the first-party reference
// app's own dev-mode bootstrap (see internal/api/v1.createUser); a tenant
// integration calling with its own bearer token gets its end user scoped to
// it.
func (s *UserService) Create(ctx context.Context, email, tenantID string) (*UserRecord, error) {
	rec := &UserRecord{ID: newID("user"), Email: email, TenantID: tenantID, CreatedAt: s.now()}
	if err := s.store.Create(ctx, rec.ID, rec.Email, rec.TenantID, rec.CreatedAt); err != nil {
		return nil, err
	}
	return rec, nil
}

func (s *UserService) Get(ctx context.Context, id string) (*UserRecord, error) {
	return s.store.Get(ctx, id)
}
