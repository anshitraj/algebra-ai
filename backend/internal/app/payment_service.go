package app

import (
	"context"
	"fmt"
	"time"

	agentpkg "github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/payment"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

type PaymentService struct {
	sources PaymentSourceStore
	agents  AgentStore
	vault   payment.CardVaultProvider
	now     func() time.Time
}

func NewPaymentService(sources PaymentSourceStore, agents AgentStore, vault payment.CardVaultProvider) *PaymentService {
	return &PaymentService{sources: sources, agents: agents, vault: vault, now: time.Now}
}

// ListSources is payments.list_sources. It returns the Safe() projection
// only — ProviderTokenRef and BillingProfileID never leave this method.
func (s *PaymentService) ListSources(ctx context.Context, agentID, userID string) ([]payment.PaymentSource, error) {
	if _, err := requirePermission(ctx, s.agents, agentID, agentpkg.PermPaymentsRequest); err != nil {
		return nil, err
	}
	sources, err := s.sources.List(ctx, userID)
	if err != nil {
		return nil, err
	}
	safe := make([]payment.PaymentSource, len(sources))
	for i, src := range sources {
		safe[i] = src.Safe()
	}
	return safe, nil
}

// GetSpendingCapability is payments.get_spending_capability /
// payments.get_source_capabilities: capability metadata only, never the
// underlying credential.
func (s *PaymentService) GetSpendingCapability(ctx context.Context, agentID, userID, alias string) (*payment.Capabilities, error) {
	if _, err := requirePermission(ctx, s.agents, agentID, agentpkg.PermPaymentsRequest); err != nil {
		return nil, err
	}
	src, err := s.sources.GetByAlias(ctx, userID, alias)
	if err != nil {
		return nil, err
	}
	if src.IsRevoked() {
		return nil, fmt.Errorf("%w: payment source %q has been revoked", shared.ErrConflict, alias)
	}
	return &src.Capabilities, nil
}

// AddCard tokenizes a card via the configured CardVaultProvider and stores
// only the resulting safe metadata. This is a user action (REST/console),
// never exposed through MCP — an agent has no reason to add a payment
// method.
func (s *PaymentService) AddCard(ctx context.Context, req payment.TokenizeRequest) (*payment.PaymentSource, error) {
	result, err := s.vault.Tokenize(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("app: tokenizing card: %w", err)
	}
	result.Source.CreatedAt = s.now()
	if err := s.sources.Create(ctx, &result.Source); err != nil {
		return nil, fmt.Errorf("app: persisting payment source: %w", err)
	}
	safe := result.Source.Safe()
	return &safe, nil
}

// RevokeSource is a user action that both revokes the source locally and
// asks the vault provider to invalidate its token. userID must be the
// source's own owner — see getOwned.
func (s *PaymentService) RevokeSource(ctx context.Context, userID, sourceID string) error {
	if _, err := s.getOwned(ctx, userID, sourceID); err != nil {
		return err
	}
	if err := s.vault.RevokeSource(ctx, sourceID); err != nil {
		return fmt.Errorf("app: revoking source at vault: %w", err)
	}
	return s.sources.Revoke(ctx, sourceID, s.now())
}

// getOwned loads a payment source and verifies it belongs to userID — same
// load-then-compare idiom as ApprovalService.getOwned. Without this, the
// X-User-ID header (a caller-asserted, non-cryptographic identifier — see
// currentUserID's doc comment in internal/api/v1/router.go) would let any
// caller revoke any other user's payment source by guessing its ID.
func (s *PaymentService) getOwned(ctx context.Context, userID, sourceID string) (*payment.PaymentSource, error) {
	src, err := s.sources.GetByID(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	if src.UserID != userID {
		return nil, fmt.Errorf("%w: payment source %s does not belong to user %s", shared.ErrUnauthorized, sourceID, userID)
	}
	return src, nil
}
