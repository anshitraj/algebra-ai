package app

import (
	"context"
	"fmt"
	"time"

	"github.com/project-algebra/algebra/internal/domain/approval"
	"github.com/project-algebra/algebra/internal/domain/audit"
	"github.com/project-algebra/algebra/internal/domain/intent"
	"github.com/project-algebra/algebra/internal/domain/paymentintent"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// ApprovalService implements the user-approval workflow. Approve/Reject are
// called with a userID, not an agentID — approval is a user action, a
// distinct trust boundary from agent authorization (an agent can request a
// purchase or an agentic payment; only the user can approve one).
//
// Exactly one of IntentID / AgenticPaymentIntentID is set on any given
// approval.Approval — Approve/Reject branch on which, transitioning
// whichever kind of parent it belongs to. Reapprove stays PurchaseIntent-
// only: AgenticPaymentIntent has no reapproval-required state (no
// discovery/requote step whose price could drift between approval and
// execution the way a merchant quote can).
type ApprovalService struct {
	intents        IntentStore
	paymentIntents PaymentIntentStore
	approvals      ApprovalStore
	quoteSvc       *QuoteService
	audit          audit.Logger
	now            func() time.Time
}

func NewApprovalService(intents IntentStore, paymentIntents PaymentIntentStore, approvals ApprovalStore, quoteSvc *QuoteService, auditLogger audit.Logger) *ApprovalService {
	return &ApprovalService{intents: intents, paymentIntents: paymentIntents, approvals: approvals, quoteSvc: quoteSvc, audit: auditLogger, now: time.Now}
}

// GetByIntent returns the most recent approval for an intent, owned by
// userID. This is what a real Approval UI (mandate §41) calls to render the
// "Agent requesting purchase..." screen before the user clicks Approve/Reject.
func (s *ApprovalService) GetByIntent(ctx context.Context, userID, intentID string) (*approval.Approval, error) {
	a, err := s.approvals.GetByIntent(ctx, intentID)
	if err != nil {
		return nil, err
	}
	if a.UserID != userID {
		return nil, fmt.Errorf("%w: approval for intent %s does not belong to user %s", shared.ErrUnauthorized, intentID, userID)
	}
	return a, nil
}

// GetByPaymentIntent is GetByIntent's counterpart for the
// AgenticPaymentIntent flow — resolves the payment-intent-scoped REST/MCP
// routes (which take a payment_intent_id, not an approval_id) to the
// underlying approval.
func (s *ApprovalService) GetByPaymentIntent(ctx context.Context, userID, paymentIntentID string) (*approval.Approval, error) {
	a, err := s.approvals.GetByPaymentIntent(ctx, paymentIntentID)
	if err != nil {
		return nil, err
	}
	if a.UserID != userID {
		return nil, fmt.Errorf("%w: approval for payment intent %s does not belong to user %s", shared.ErrUnauthorized, paymentIntentID, userID)
	}
	return a, nil
}

func (s *ApprovalService) getOwned(ctx context.Context, userID, approvalID string) (*approval.Approval, error) {
	a, err := s.approvals.Get(ctx, approvalID)
	if err != nil {
		return nil, err
	}
	if a.UserID != userID {
		return nil, fmt.Errorf("%w: approval %s does not belong to user %s", shared.ErrUnauthorized, approvalID, userID)
	}
	return a, nil
}

// Approve grants a PENDING approval exactly as it was presented to the
// user — merchant, items, amount, payment source. It does not re-fetch the
// quote; the freshness guarantee that matters is enforced at execution time
// (order_service.Execute refreshes and re-verifies immediately before
// charging, per mandate §18).
func (s *ApprovalService) Approve(ctx context.Context, userID, approvalID string) (*approval.Approval, error) {
	a, err := s.getOwned(ctx, userID, approvalID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	if a.Status == approval.StatusExpired || (a.Status == approval.StatusPending && a.IsExpired(now)) {
		return nil, fmt.Errorf("%w: approval %s has expired", shared.ErrConflict, approvalID)
	}
	if a.Status != approval.StatusPending {
		return nil, fmt.Errorf("%w: approval %s is in status %s, not PENDING", shared.ErrConflict, approvalID, a.Status)
	}

	a.Status = approval.StatusApproved
	a.DecidedAt = &now
	if err := s.approvals.Update(ctx, a); err != nil {
		return nil, fmt.Errorf("app: persisting approval: %w", err)
	}

	if a.AgenticPaymentIntentID != "" {
		p, err := s.paymentIntents.Get(ctx, a.AgenticPaymentIntentID)
		if err != nil {
			return nil, err
		}
		if err := transitionPaymentIntent(ctx, s.paymentIntents, s.audit, s.now, p, paymentintent.StateAuthorized, "ApprovalGranted", "user approved"); err != nil {
			return nil, err
		}
		return a, nil
	}

	pi, err := s.intents.Get(ctx, a.IntentID)
	if err != nil {
		return nil, err
	}
	if err := transitionIntent(ctx, s.intents, s.audit, s.now, pi, intent.StateApproved, "ApprovalGranted", "user approved"); err != nil {
		return nil, err
	}
	return a, nil
}

// Reject cancels a PENDING approval and its intent.
func (s *ApprovalService) Reject(ctx context.Context, userID, approvalID string) (*approval.Approval, error) {
	a, err := s.getOwned(ctx, userID, approvalID)
	if err != nil {
		return nil, err
	}
	if a.Status != approval.StatusPending && a.Status != approval.StatusReapprovalRequired {
		return nil, fmt.Errorf("%w: approval %s is in status %s, not rejectable", shared.ErrConflict, approvalID, a.Status)
	}
	now := s.now()
	a.Status = approval.StatusRejected
	a.DecidedAt = &now
	if err := s.approvals.Update(ctx, a); err != nil {
		return nil, fmt.Errorf("app: persisting rejection: %w", err)
	}

	if a.AgenticPaymentIntentID != "" {
		p, err := s.paymentIntents.Get(ctx, a.AgenticPaymentIntentID)
		if err != nil {
			return nil, err
		}
		if err := transitionPaymentIntent(ctx, s.paymentIntents, s.audit, s.now, p, paymentintent.StateCancelled, "ApprovalRejected", "user rejected"); err != nil {
			return nil, err
		}
		return a, nil
	}

	pi, err := s.intents.Get(ctx, a.IntentID)
	if err != nil {
		return nil, err
	}
	if err := transitionIntent(ctx, s.intents, s.audit, s.now, pi, intent.StateCancelled, "ApprovalRejected", "user rejected"); err != nil {
		return nil, err
	}
	return a, nil
}

// Reapprove re-confirms an approval that execution flagged
// REAPPROVAL_REQUIRED after the merchant's price drifted. It re-binds the
// approval to the freshly refreshed quote — the user is approving what they
// are shown NOW, not the stale original terms.
func (s *ApprovalService) Reapprove(ctx context.Context, userID, approvalID string) (*approval.Approval, error) {
	a, err := s.getOwned(ctx, userID, approvalID)
	if err != nil {
		return nil, err
	}
	if a.Status != approval.StatusReapprovalRequired {
		return nil, fmt.Errorf("%w: approval %s is in status %s, not REAPPROVAL_REQUIRED", shared.ErrConflict, approvalID, a.Status)
	}

	refreshed, err := s.quoteSvc.RefreshQuote(ctx, a.QuoteID)
	if err != nil {
		return nil, err
	}
	items := make([]approval.HashableItem, len(refreshed.Items))
	for i, it := range refreshed.Items {
		items[i] = approval.HashableItem{MerchantProductID: it.MerchantProductID, Quantity: it.Quantity, UnitPriceMinorUnits: it.UnitPrice.MinorUnits}
	}
	now := s.now()
	a.Amount = refreshed.FinalPayable
	a.ItemsHash = approval.CanonicalHash(a.Merchant, items, refreshed.FinalPayable.Currency, a.PaymentSourceAlias)
	a.Status = approval.StatusApproved
	a.DecidedAt = &now
	if err := s.approvals.Update(ctx, a); err != nil {
		return nil, fmt.Errorf("app: persisting reapproval: %w", err)
	}

	pi, err := s.intents.Get(ctx, a.IntentID)
	if err != nil {
		return nil, err
	}
	if err := transitionIntent(ctx, s.intents, s.audit, s.now, pi, intent.StateApproved, "ApprovalGranted", "user reapproved refreshed terms"); err != nil {
		return nil, err
	}
	return a, nil
}
