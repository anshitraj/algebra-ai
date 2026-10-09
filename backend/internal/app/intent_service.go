package app

import (
	"context"
	"fmt"
	"time"

	agentpkg "github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/audit"
	"github.com/project-algebra/algebra/internal/domain/intent"
)

type IntentService struct {
	intents IntentStore
	agents  AgentStore
	audit   audit.Logger
	now     func() time.Time
}

func NewIntentService(intents IntentStore, agents AgentStore, auditLogger audit.Logger) *IntentService {
	return &IntentService{intents: intents, agents: agents, audit: auditLogger, now: time.Now}
}

type CreateIntentInput struct {
	UserID      string
	AgentID     string
	Items       []intent.Item
	Constraints intent.Constraints
}

// CreateIntent is commerce.create_purchase_intent. It does not call
// discovery — that's a separate, explicit step, so an agent can create
// several draft intents without triggering merchant fan-out for each one.
func (s *IntentService) CreateIntent(ctx context.Context, idem IdempotencyStore, idemKey string, in CreateIntentInput) (*intent.PurchaseIntent, error) {
	return RunIdempotent(ctx, idem, idemKey, "create_intent", func(ctx context.Context) (*intent.PurchaseIntent, error) {
		if _, err := requirePermission(ctx, s.agents, in.AgentID, agentpkg.PermShoppingCreateIntent); err != nil {
			return nil, err
		}
		if len(in.Items) == 0 {
			return nil, fmt.Errorf("app: intent must have at least one item")
		}
		if in.Constraints.Currency == "" || in.Constraints.MaxTotalMinorUnits <= 0 {
			return nil, fmt.Errorf("app: intent constraints must specify a positive max_total and currency")
		}

		now := s.now()
		pi := intent.New(newID("pi"), in.UserID, in.AgentID, in.Items, in.Constraints, now)
		if err := s.intents.Create(ctx, pi); err != nil {
			return nil, fmt.Errorf("app: persisting intent: %w", err)
		}

		s.recordAudit(ctx, pi, "IntentCreated", "", string(pi.Status), "created")
		return pi, nil
	})
}

func (s *IntentService) GetIntent(ctx context.Context, agentID, id string) (*intent.PurchaseIntent, error) {
	_, pi, err := requireOwnedIntent(ctx, s.agents, s.intents, agentID, id, agentpkg.PermShoppingRead)
	return pi, err
}

// CancelIntent moves an intent to CANCELLED. Legal from any state the state
// machine allows (state_machine.go) — anything else surfaces the
// *intent.ErrIllegalTransition so the caller knows precisely why it failed.
func (s *IntentService) CancelIntent(ctx context.Context, agentID, id string) (*intent.PurchaseIntent, error) {
	_, pi, err := requireOwnedIntent(ctx, s.agents, s.intents, agentID, id, agentpkg.PermShoppingExecute)
	if err != nil {
		return nil, err
	}
	prev, err := pi.ApplyTransition(intent.StateCancelled, s.now())
	if err != nil {
		return nil, err
	}
	if err := s.intents.Update(ctx, pi); err != nil {
		return nil, fmt.Errorf("app: persisting cancellation: %w", err)
	}
	s.recordAudit(ctx, pi, "IntentCancelled", string(prev), string(pi.Status), "cancelled by agent")
	return pi, nil
}

func (s *IntentService) recordAudit(ctx context.Context, pi *intent.PurchaseIntent, action, prevState, newState, result string) {
	recordAudit(ctx, s.audit, s.now, pi, action, prevState, newState, result)
}
