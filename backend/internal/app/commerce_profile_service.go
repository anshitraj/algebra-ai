package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	agentpkg "github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/commerceprofile"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// CommerceProfileStore backs CommerceProfileService. See
// internal/domain/commerceprofile.
type CommerceProfileStore interface {
	Get(ctx context.Context, userID string) (*commerceprofile.CommerceProfile, error)
	Upsert(ctx context.Context, p *commerceprofile.CommerceProfile) error
}

// CommerceProfileService reads and writes a user's CommerceProfile — the
// agent-readable preference layer that sits alongside (and is deliberately
// separate from) the human-only, alias-resolved ShippingProfile/
// BillingProfile system in internal/domain/privacy.
type CommerceProfileService struct {
	store  CommerceProfileStore
	agents AgentStore
	now    func() time.Time
}

func NewCommerceProfileService(store CommerceProfileStore, agents AgentStore) *CommerceProfileService {
	return &CommerceProfileService{store: store, agents: agents, now: time.Now}
}

// GetOrEmpty returns the calling agent's user's profile, or a valid empty
// one if nothing has been set yet — callers never need to special-case a
// brand-new user.
func (s *CommerceProfileService) GetOrEmpty(ctx context.Context, agentID string) (*commerceprofile.CommerceProfile, error) {
	ag, err := requirePermission(ctx, s.agents, agentID, agentpkg.PermProfilesRead)
	if err != nil {
		return nil, err
	}
	p, err := s.store.Get(ctx, ag.UserID)
	if err != nil {
		if errors.Is(err, shared.ErrNotFound) {
			return commerceprofile.Empty(ag.UserID), nil
		}
		return nil, err
	}
	return p, nil
}

// SetPreferences merges attributes into one category and persists the
// whole profile — "the profile gets better through usage," not a full
// replace. Requires PermProfilesWrite: this is a genuinely new agent
// capability, distinct from the read-only permission above.
func (s *CommerceProfileService) SetPreferences(ctx context.Context, agentID, category string, attributes map[string]any) (*commerceprofile.CommerceProfile, error) {
	if category == "" {
		return nil, fmt.Errorf("app: commerce profile category is required")
	}
	ag, err := requirePermission(ctx, s.agents, agentID, agentpkg.PermProfilesWrite)
	if err != nil {
		return nil, err
	}
	p, err := s.getOrEmptyFor(ctx, ag.UserID)
	if err != nil {
		return nil, err
	}
	p.MergePreferences(category, attributes)
	p.UpdatedAt = s.now()
	if err := s.store.Upsert(ctx, p); err != nil {
		return nil, fmt.Errorf("app: persisting commerce profile: %w", err)
	}
	return p, nil
}

// SetDefaultAliases updates the user's default shipping/payment aliases —
// pointers into the existing alias systems, never new address/payment
// storage. A nil pointer leaves that field unchanged; a pointer to "" clears
// it.
func (s *CommerceProfileService) SetDefaultAliases(ctx context.Context, agentID string, shippingAlias, paymentAlias *string) (*commerceprofile.CommerceProfile, error) {
	ag, err := requirePermission(ctx, s.agents, agentID, agentpkg.PermProfilesWrite)
	if err != nil {
		return nil, err
	}
	p, err := s.getOrEmptyFor(ctx, ag.UserID)
	if err != nil {
		return nil, err
	}
	if shippingAlias != nil {
		p.DefaultShippingAlias = *shippingAlias
	}
	if paymentAlias != nil {
		p.DefaultPaymentAlias = *paymentAlias
	}
	p.UpdatedAt = s.now()
	if err := s.store.Upsert(ctx, p); err != nil {
		return nil, fmt.Errorf("app: persisting commerce profile: %w", err)
	}
	return p, nil
}

func (s *CommerceProfileService) getOrEmptyFor(ctx context.Context, userID string) (*commerceprofile.CommerceProfile, error) {
	p, err := s.store.Get(ctx, userID)
	if err != nil {
		if errors.Is(err, shared.ErrNotFound) {
			return commerceprofile.Empty(userID), nil
		}
		return nil, err
	}
	return p, nil
}
