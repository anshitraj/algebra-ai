package app

import (
	"context"
	"time"

	"github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/integrator"
)

// IntegratorService mints and revokes Integrator credentials — the B2B
// counterpart to AgentService, for third-party applications (a wallet, a
// checkout provider) calling Algebra's standalone policy-evaluation
// surface rather than Algebra's own commerce flow. See
// internal/domain/integrator's package doc.
type IntegratorService struct {
	store IntegratorStore
	now   func() time.Time
}

func NewIntegratorService(store IntegratorStore) *IntegratorService {
	return &IntegratorService{store: store, now: time.Now}
}

// CreateIntegrator mints a new bearer token and returns it exactly once —
// only its SHA-256 hash is persisted (agent.GenerateToken, reused as-is).
// Open, like AgentService.CreateAgent: an integrator registers itself,
// there is no owning account to authorize against yet in this build.
func (s *IntegratorService) CreateIntegrator(ctx context.Context, name string) (rawToken string, i *integrator.Integrator, err error) {
	raw, hash, err := agent.GenerateToken()
	if err != nil {
		return "", nil, err
	}
	i = &integrator.Integrator{ID: newID("integrator"), Name: name, TokenHash: hash, CreatedAt: s.now()}
	if err := s.store.Create(ctx, i); err != nil {
		return "", nil, err
	}
	return raw, i, nil
}

func (s *IntegratorService) Revoke(ctx context.Context, integratorID string) error {
	return s.store.Revoke(ctx, integratorID, s.now())
}
