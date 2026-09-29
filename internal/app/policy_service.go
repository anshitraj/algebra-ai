package app

import (
	"context"
	"fmt"
	"time"

	agentpkg "github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/approval"
	"github.com/project-algebra/algebra/internal/domain/audit"
	"github.com/project-algebra/algebra/internal/domain/intent"
	"github.com/project-algebra/algebra/internal/domain/quote"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
	"github.com/project-algebra/algebra/policy"
)

type PolicyService struct {
	intents     IntentStore
	agents      AgentStore
	quotes      QuoteStore
	decisions   PolicyDecisionStore
	approvals   ApprovalStore
	ledger      SpendLedger
	provider    policy.Provider
	audit       audit.Logger
	now         func() time.Time
	approvalTTL time.Duration
	userRules   UserRulesSource
	passes      PassGate // optional — see SetSpendPasses
}

// SetSpendPasses makes every purchase by an agent that holds a Spend Pass
// clear the pass as well as the person's guardrails (the stricter wins).
func (s *PolicyService) SetSpendPasses(g PassGate) { s.passes = g }

// SetUserRules makes evaluation use each user's own guardrails (see
// UserRulesSource) instead of the single platform-default provider. Nil
// keeps the platform default for everyone.
func (s *PolicyService) SetUserRules(src UserRulesSource) { s.userRules = src }

func NewPolicyService(intents IntentStore, agents AgentStore, quotes QuoteStore, decisions PolicyDecisionStore, approvals ApprovalStore, ledger SpendLedger, provider policy.Provider, auditLogger audit.Logger, approvalTTL time.Duration) *PolicyService {
	return &PolicyService{
		intents: intents, agents: agents, quotes: quotes, decisions: decisions,
		approvals: approvals, ledger: ledger, provider: provider, audit: auditLogger,
		now: time.Now, approvalTTL: approvalTTL,
	}
}

// EvaluateAndTransition is commerce.request_purchase: it runs the policy
// engine against the intent's selected quote and moves the intent to
// POLICY_REJECTED, APPROVAL_REQUIRED, or APPROVED accordingly. A DENY is
// terminal and nothing — not the agent, not this code — can override it
// (mandate §8).
func (s *PolicyService) EvaluateAndTransition(ctx context.Context, agentID, intentID string) (*policy.PolicyDecision, error) {
	if _, _, err := requireOwnedIntent(ctx, s.agents, s.intents, agentID, intentID, agentpkg.PermShoppingExecute); err != nil {
		return nil, err
	}
	pi, q, err := s.loadIntentAndSelectedQuote(ctx, intentID)
	if err != nil {
		return nil, err
	}

	if err := transitionIntent(ctx, s.intents, s.audit, s.now, pi, intent.StatePolicyCheck, "PolicyEvaluationStarted", "evaluating "+q.Merchant); err != nil {
		return nil, err
	}

	decision, err := s.evaluate(ctx, pi, q)
	if err != nil {
		return nil, err
	}

	if err := s.decisions.Save(ctx, intentID, decision); err != nil {
		return nil, fmt.Errorf("app: persisting policy decision: %w", err)
	}
	evt := audit.NewEvent("PolicyEvaluated", s.now())
	evt.UserID, evt.AgentID, evt.IntentID = pi.UserID, pi.AgentID, pi.ID
	evt.Merchant = q.Merchant
	evt.PolicyDecision = string(decision.Decision)
	evt.Result = fmt.Sprintf("%v", decision.ReasonCodes)
	_ = s.audit.Record(ctx, evt)

	switch decision.Decision {
	case policy.Deny:
		if err := transitionIntent(ctx, s.intents, s.audit, s.now, pi, intent.StatePolicyRejected, "IntentPolicyRejected", fmt.Sprintf("%v", decision.ReasonCodes)); err != nil {
			return nil, err
		}
		return decision, nil

	case policy.RequireApproval:
		if err := s.createApproval(ctx, pi, q, approval.StatusPending); err != nil {
			return nil, err
		}
		if err := transitionIntent(ctx, s.intents, s.audit, s.now, pi, intent.StateApprovalRequired, "ApprovalRequested", "policy requires explicit user approval"); err != nil {
			return nil, err
		}
		return decision, nil

	default: // policy.Allow
		if err := s.createApproval(ctx, pi, q, approval.StatusApproved); err != nil {
			return nil, err
		}
		if err := transitionIntent(ctx, s.intents, s.audit, s.now, pi, intent.StateApproved, "ApprovalGranted", "auto-approved under policy"); err != nil {
			return nil, err
		}
		return decision, nil
	}
}

func (s *PolicyService) loadIntentAndSelectedQuote(ctx context.Context, intentID string) (*intent.PurchaseIntent, *quote.CheckoutQuote, error) {
	pi, err := s.intents.Get(ctx, intentID)
	if err != nil {
		return nil, nil, err
	}
	if pi.SelectedQuoteID == "" {
		return nil, nil, fmt.Errorf("app: intent %s has no selected quote; call select_quote first", intentID)
	}
	q, err := s.quotes.Get(ctx, pi.SelectedQuoteID)
	if err != nil {
		return nil, nil, err
	}
	return pi, q, nil
}

// evaluate builds the policy.Input from persisted state and asks the
// provider for a decision. It is the single place the input is assembled
// so EvaluateAndTransition (real, side-effecting) and PreviewDecision
// (read-only) can never drift apart on what they actually check.
func (s *PolicyService) evaluate(ctx context.Context, pi *intent.PurchaseIntent, q *quote.CheckoutQuote) (*policy.PolicyDecision, error) {
	spendToday, err := s.ledger.SpendToday(ctx, pi.UserID, q.FinalPayable.Currency, s.now())
	if err != nil {
		return nil, fmt.Errorf("app: reading spend ledger: %w", err)
	}
	input := policy.Input{
		UserID:               pi.UserID,
		AgentID:              pi.AgentID,
		Merchant:             q.Merchant,
		Category:             pi.Constraints.Category,
		AmountMinorUnits:     q.FinalPayable.MinorUnits,
		Currency:             q.FinalPayable.Currency,
		PaymentProfile:       pi.Constraints.PaymentProfile,
		ShippingProfile:      pi.Constraints.DeliveryProfile,
		International:        pi.Constraints.International,
		SpendTodayMinorUnits: spendToday,
	}
	provider, err := providerFor(ctx, s.userRules, s.provider, pi.UserID)
	if err != nil {
		return nil, err
	}
	decision, err := provider.EvaluatePurchaseIntent(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("app: policy evaluation failed: %w", err)
	}
	if s.passes != nil {
		passDecision, pass, err := s.passes.CheckPurchase(ctx, pi.AgentID, q.Merchant, pi.Constraints.Category, q.FinalPayable)
		if err != nil {
			return nil, err
		}
		if passDecision != nil {
			decision = spendpass.Combine(decision, passDecision, pass.ID)
		}
	}
	return decision, nil
}

// PreviewDecision is policy.evaluate_intent: it answers "would this be
// allowed right now" WITHOUT transitioning the intent, creating an
// approval, or persisting a decision row. Safe to call as many times as an
// agent wants while comparing quotes.
func (s *PolicyService) PreviewDecision(ctx context.Context, agentID, intentID string) (*policy.PolicyDecision, error) {
	if _, _, err := requireOwnedIntent(ctx, s.agents, s.intents, agentID, intentID, agentpkg.PermShoppingRead); err != nil {
		return nil, err
	}
	pi, q, err := s.loadIntentAndSelectedQuote(ctx, intentID)
	if err != nil {
		return nil, err
	}
	return s.evaluate(ctx, pi, q)
}

// ExplainDecision is policy.explain_decision: it surfaces the actual
// decision + reason codes + policy version that were recorded for this
// intent's most recent evaluation — it explains by showing the real
// recorded reasoning, not by generating new prose about it.
func (s *PolicyService) ExplainDecision(ctx context.Context, agentID, intentID string) (*policy.PolicyDecision, error) {
	if _, _, err := requireOwnedIntent(ctx, s.agents, s.intents, agentID, intentID, agentpkg.PermPolicyRead); err != nil {
		return nil, err
	}
	return s.decisions.GetLatestByIntent(ctx, intentID)
}

func (s *PolicyService) createApproval(ctx context.Context, pi *intent.PurchaseIntent, q *quote.CheckoutQuote, status approval.Status) error {
	items := make([]approval.HashableItem, len(q.Items))
	for i, it := range q.Items {
		items[i] = approval.HashableItem{MerchantProductID: it.MerchantProductID, Quantity: it.Quantity, UnitPriceMinorUnits: it.UnitPrice.MinorUnits}
	}
	hash := approval.CanonicalHash(q.Merchant, items, q.FinalPayable.Currency, pi.Constraints.PaymentProfile)

	now := s.now()
	a := &approval.Approval{
		ID:                 newID("appr"),
		IntentID:           pi.ID,
		QuoteID:            q.QuoteID,
		UserID:             pi.UserID,
		AgentID:            pi.AgentID,
		Merchant:           q.Merchant,
		Amount:             q.FinalPayable,
		PaymentSourceAlias: pi.Constraints.PaymentProfile,
		ItemsHash:          hash,
		Status:             status,
		CreatedAt:          now,
		ExpiresAt:          now.Add(s.approvalTTL),
	}
	if status == approval.StatusApproved {
		a.DecidedAt = &now
	}
	if err := s.approvals.Create(ctx, a); err != nil {
		return fmt.Errorf("app: creating approval: %w", err)
	}
	return nil
}
