package app

import (
	"context"
	"fmt"
	"time"

	agentpkg "github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/intent"
	"github.com/project-algebra/algebra/internal/domain/quote"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

type QuoteService struct {
	intents    IntentStore
	agents     AgentStore
	quotes     QuoteStore
	connectors *ConnectorRegistry
	now        func() time.Time
}

func NewQuoteService(intents IntentStore, agents AgentStore, quotes QuoteStore, connectors *ConnectorRegistry) *QuoteService {
	return &QuoteService{intents: intents, agents: agents, quotes: quotes, connectors: connectors, now: time.Now}
}

func (s *QuoteService) GetQuotes(ctx context.Context, agentID, intentID string) ([]*quote.CheckoutQuote, error) {
	if _, _, err := requireOwnedIntent(ctx, s.agents, s.intents, agentID, intentID, agentpkg.PermShoppingRead); err != nil {
		return nil, err
	}
	return s.quotes.ListByIntent(ctx, intentID)
}

// SelectQuote records which quote the agent wants to proceed with. It does
// not move the intent's state — POLICY_CHECK happens explicitly via
// PolicyService.EvaluateAndTransition (commerce.request_purchase), so an
// agent can compare quotes without triggering policy evaluation each time.
func (s *QuoteService) SelectQuote(ctx context.Context, agentID, intentID, quoteID string) error {
	_, pi, err := requireOwnedIntent(ctx, s.agents, s.intents, agentID, intentID, agentpkg.PermShoppingRead)
	if err != nil {
		return err
	}
	if pi.Status != intent.StateQuoted {
		return fmt.Errorf("%w: intent %s is in state %s, not QUOTED", shared.ErrConflict, intentID, pi.Status)
	}
	q, err := s.quotes.Get(ctx, quoteID)
	if err != nil {
		return err
	}
	if q == nil {
		return fmt.Errorf("%w: quote %s", shared.ErrNotFound, quoteID)
	}
	pi.SelectedQuoteID = quoteID
	return s.intents.Update(ctx, pi)
}

// RefreshQuote re-fetches the checkout quote from the merchant connector
// against the same cart. This is what mandate §18 requires immediately
// before authorization: search-engine/cached prices are never authoritative
// — only a freshly retrieved merchant quote is.
func (s *QuoteService) RefreshQuote(ctx context.Context, quoteID string) (*quote.CheckoutQuote, error) {
	existing, err := s.quotes.Get(ctx, quoteID)
	if err != nil {
		return nil, err
	}
	connector, err := s.connectors.Get(existing.Merchant)
	if err != nil {
		return nil, err
	}
	refreshed, err := connector.GetCheckoutQuote(ctx, existing.CartID)
	if err != nil {
		return nil, fmt.Errorf("app: refreshing quote %s: %w", quoteID, err)
	}
	refreshed.QuoteID = existing.QuoteID
	refreshed.CartID = existing.CartID
	refreshed.RetrievedAt = s.now()
	refreshed.Recompute()
	return refreshed, nil
}
