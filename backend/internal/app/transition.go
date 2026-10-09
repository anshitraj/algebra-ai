package app

import (
	"context"
	"fmt"
	"time"

	"github.com/project-algebra/algebra/internal/domain/audit"
	"github.com/project-algebra/algebra/internal/domain/intent"
)

// transitionIntent moves pi to `to`, persists it, and records the audit
// event — the one shared path every service uses to change an intent's
// state, so "every state transition gets an audit event" (mandate §3) is
// structural rather than a convention each service has to remember.
func transitionIntent(ctx context.Context, intents IntentStore, auditLogger audit.Logger, now func() time.Time, pi *intent.PurchaseIntent, to intent.State, action, result string) error {
	prev, err := pi.ApplyTransition(to, now())
	if err != nil {
		return err
	}
	if err := intents.Update(ctx, pi); err != nil {
		return fmt.Errorf("app: persisting transition to %s: %w", to, err)
	}
	recordAudit(ctx, auditLogger, now, pi, action, string(prev), string(pi.Status), result)
	return nil
}

func recordAudit(ctx context.Context, auditLogger audit.Logger, now func() time.Time, pi *intent.PurchaseIntent, action, prevState, newState, result string) {
	evt := audit.NewEvent(action, now())
	evt.UserID = pi.UserID
	evt.AgentID = pi.AgentID
	evt.IntentID = pi.ID
	evt.PreviousState = prevState
	evt.NewState = newState
	evt.Result = result
	// Audit-write failures are intentionally not surfaced to the caller: a
	// commerce operation that already succeeded must not be unwound because
	// the audit sink hiccuped. A production deployment should alert on this
	// error rather than drop it silently; that alerting hook is not built in
	// this session.
	_ = auditLogger.Record(ctx, evt)
}
