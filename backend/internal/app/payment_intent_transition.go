package app

import (
	"context"
	"fmt"
	"time"

	"github.com/project-algebra/algebra/internal/domain/audit"
	"github.com/project-algebra/algebra/internal/domain/paymentintent"
)

// transitionPaymentIntent is transitionIntent's counterpart for
// AgenticPaymentIntent — the one shared path every service uses to change a
// payment intent's state and emit the paired audit event. Used by both
// PaymentIntentService and ApprovalService (an approval's Approve/Reject
// transitions whichever kind of parent it belongs to).
func transitionPaymentIntent(ctx context.Context, intents PaymentIntentStore, auditLogger audit.Logger, now func() time.Time, p *paymentintent.AgenticPaymentIntent, to paymentintent.State, action, result string) error {
	prev, err := p.ApplyTransition(to, now())
	if err != nil {
		return err
	}
	if err := intents.Update(ctx, p); err != nil {
		return fmt.Errorf("app: persisting payment intent transition to %s: %w", to, err)
	}
	recordPaymentIntentAudit(ctx, auditLogger, now, p, action, string(prev), string(p.Status), result)
	return nil
}

func recordPaymentIntentAudit(ctx context.Context, auditLogger audit.Logger, now func() time.Time, p *paymentintent.AgenticPaymentIntent, action, prevState, newState, result string) {
	evt := audit.NewEvent(action, now())
	evt.TenantID = p.TenantID
	evt.UserID = p.UserID
	evt.AgentID = p.AgentID
	evt.AgenticPaymentIntentID = p.ID
	evt.PreviousState = prevState
	evt.NewState = newState
	evt.Merchant = p.Merchant
	evt.PaymentSourceAlias = p.PaymentSourceAlias
	evt.Result = result
	// Audit-write failures are intentionally not surfaced to the caller —
	// same rationale as recordAudit in transition.go.
	_ = auditLogger.Record(ctx, evt)
}
