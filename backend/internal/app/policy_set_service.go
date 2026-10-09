package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/project-algebra/algebra/internal/domain/policyset"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/policy"
)

// PolicySetService persists a tenant's policy.Rules with versioning — see
// internal/domain/policyset's package doc. The policy package itself
// (Provider/LocalProvider/Rules) is untouched by this.
type PolicySetService struct {
	store PolicySetStore
	now   func() time.Time
}

func NewPolicySetService(store PolicySetStore) *PolicySetService {
	return &PolicySetService{store: store, now: time.Now}
}

// SetPolicy persists a new version, superseding whatever was active for
// (tenantID, userID) — nil userID sets the tenant's own default; a set
// userID overrides it for one end user only. Superseded versions are kept,
// never deleted.
func (s *PolicySetService) SetPolicy(ctx context.Context, tenantID string, userID *string, rules policy.Rules) (*policyset.PolicySet, error) {
	now := s.now()
	version := 1
	if current, err := s.store.GetActive(ctx, tenantID, userID); err == nil {
		version = current.Version + 1
	} else if !errors.Is(err, shared.ErrNotFound) {
		return nil, err
	}
	if err := s.store.SupersedeActive(ctx, tenantID, userID, now); err != nil {
		return nil, err
	}
	ps := &policyset.PolicySet{
		ID: newID("policyset"), TenantID: tenantID, UserID: userID, Rules: rules, Version: version, CreatedAt: now,
	}
	if err := s.store.Create(ctx, ps); err != nil {
		return nil, fmt.Errorf("app: persisting policy set: %w", err)
	}
	return ps, nil
}

// GetActive returns the effective policy for (tenantID, userID): a
// user-specific override if one exists, falling back to the tenant's own
// default. Returns shared.ErrNotFound if the tenant has never set any
// policy at all — callers must not silently fall back to
// policy.DefaultRules() in that case; that ruleset is INR-specific example
// data and would silently mismatch a tenant that configured nothing.
func (s *PolicySetService) GetActive(ctx context.Context, tenantID string, userID *string) (*policyset.PolicySet, error) {
	if userID != nil {
		ps, err := s.store.GetActive(ctx, tenantID, userID)
		switch {
		case err == nil:
			return ps, nil
		case !errors.Is(err, shared.ErrNotFound):
			return nil, err
		}
	}
	return s.store.GetActive(ctx, tenantID, nil)
}
