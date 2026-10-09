package app

import (
	"context"
	"time"

	"github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/tenant"
)

// TenantService mints and revokes Tenant credentials — the B2B root
// credential a business integration authenticates its own admin operations
// with (setting policy, registering webhook endpoints, provisioning its end
// users). See internal/domain/tenant's package doc.
type TenantService struct {
	store TenantStore
	now   func() time.Time
}

func NewTenantService(store TenantStore) *TenantService {
	return &TenantService{store: store, now: time.Now}
}

// CreateTenant mints a new bearer token and returns it exactly once — only
// its SHA-256 hash is persisted (agent.GenerateToken, reused as-is). Open,
// like AgentService/IntegratorService: a business registers itself, there
// is no owning account to authorize against yet in this build.
func (s *TenantService) CreateTenant(ctx context.Context, name string) (rawToken string, t *tenant.Tenant, err error) {
	raw, hash, err := agent.GenerateToken()
	if err != nil {
		return "", nil, err
	}
	t = &tenant.Tenant{ID: newID("tenant"), Name: name, TokenHash: hash, CreatedAt: s.now()}
	if err := s.store.Create(ctx, t); err != nil {
		return "", nil, err
	}
	return raw, t, nil
}

func (s *TenantService) Revoke(ctx context.Context, tenantID string) error {
	return s.store.Revoke(ctx, tenantID, s.now())
}
