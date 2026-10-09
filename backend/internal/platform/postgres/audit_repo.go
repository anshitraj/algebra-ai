package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/project-algebra/algebra/internal/domain/audit"
)

type AuditRepo struct{ db *DB }

func NewAuditRepo(db *DB) *AuditRepo { return &AuditRepo{db: db} }

// Record persists an audit event. It rejects (via Event.Validate) any event
// whose metadata looks like it carries a secret before it ever reaches
// SQL — audit_events is append-only at the database level (see
// migrations/0001_init.sql's trigger), so there is no later chance to
// scrub a mistake out of it.
func (r *AuditRepo) Record(ctx context.Context, evt audit.Event) error {
	if err := evt.Validate(); err != nil {
		return fmt.Errorf("postgres: refusing to record audit event: %w", err)
	}
	metadata, err := marshalOrNull(evt.Metadata)
	if err != nil {
		return fmt.Errorf("postgres: marshaling audit metadata: %w", err)
	}
	_, err = r.db.Pool.Exec(ctx, `
		INSERT INTO audit_events (id, trace_id, "timestamp", user_id, agent_id, intent_id, agentic_payment_intent_id, tenant_id, action,
		                          previous_state, new_state, policy_decision, merchant, payment_source_alias, result, metadata)
		VALUES ($1,NULLIF($2,''),$3,NULLIF($4,''),NULLIF($5,''),NULLIF($6,''),NULLIF($7,''),NULLIF($8,''),$9,
		        NULLIF($10,''),NULLIF($11,''),NULLIF($12,''),NULLIF($13,''),NULLIF($14,''),NULLIF($15,''),$16::jsonb)`,
		evt.EventID, evt.TraceID, evt.Timestamp, evt.UserID, evt.AgentID, evt.IntentID, evt.AgenticPaymentIntentID, evt.TenantID, evt.Action,
		evt.PreviousState, evt.NewState, evt.PolicyDecision, evt.Merchant, evt.PaymentSourceAlias, evt.Result, metadata)
	if err != nil {
		return fmt.Errorf("postgres: inserting audit event: %w", err)
	}
	return nil
}

// ListByIntent reads the ordered audit trail for one intent. Read-only by
// construction: audit_events has no UPDATE/DELETE path anywhere in this
// codebase, and the database rejects both regardless (see the trigger in
// migrations/0001_init.sql).
func (r *AuditRepo) ListByIntent(ctx context.Context, intentID string, limit int) ([]audit.Event, error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT id, COALESCE(trace_id,''), "timestamp", COALESCE(user_id,''), COALESCE(agent_id,''),
		       COALESCE(intent_id,''), action, COALESCE(previous_state,''), COALESCE(new_state,''),
		       COALESCE(policy_decision,''), COALESCE(merchant,''), COALESCE(payment_source_alias,''),
		       COALESCE(result,''), metadata
		FROM audit_events WHERE intent_id = $1 ORDER BY "timestamp" ASC, created_at ASC LIMIT $2`,
		intentID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing audit events: %w", err)
	}
	defer rows.Close()

	var out []audit.Event
	for rows.Next() {
		var e audit.Event
		var metadataRaw []byte
		if err := rows.Scan(&e.EventID, &e.TraceID, &e.Timestamp, &e.UserID, &e.AgentID, &e.IntentID,
			&e.Action, &e.PreviousState, &e.NewState, &e.PolicyDecision, &e.Merchant,
			&e.PaymentSourceAlias, &e.Result, &metadataRaw); err != nil {
			return nil, fmt.Errorf("postgres: scanning audit event: %w", err)
		}
		if len(metadataRaw) > 0 {
			if err := json.Unmarshal(metadataRaw, &e.Metadata); err != nil {
				return nil, fmt.Errorf("postgres: decoding audit metadata: %w", err)
			}
		}
		out = append(out, e)
	}
	return out, nil
}

// ListByPaymentIntent is ListByIntent's counterpart for AgenticPaymentIntent
// audit trails.
func (r *AuditRepo) ListByPaymentIntent(ctx context.Context, paymentIntentID string, limit int) ([]audit.Event, error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT id, COALESCE(trace_id,''), "timestamp", COALESCE(user_id,''), COALESCE(agent_id,''),
		       COALESCE(agentic_payment_intent_id,''), action, COALESCE(previous_state,''), COALESCE(new_state,''),
		       COALESCE(policy_decision,''), COALESCE(merchant,''), COALESCE(payment_source_alias,''),
		       COALESCE(result,''), metadata
		FROM audit_events WHERE agentic_payment_intent_id = $1 ORDER BY "timestamp" ASC, created_at ASC LIMIT $2`,
		paymentIntentID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing payment intent audit events: %w", err)
	}
	defer rows.Close()

	var out []audit.Event
	for rows.Next() {
		var e audit.Event
		var metadataRaw []byte
		if err := rows.Scan(&e.EventID, &e.TraceID, &e.Timestamp, &e.UserID, &e.AgentID, &e.AgenticPaymentIntentID,
			&e.Action, &e.PreviousState, &e.NewState, &e.PolicyDecision, &e.Merchant,
			&e.PaymentSourceAlias, &e.Result, &metadataRaw); err != nil {
			return nil, fmt.Errorf("postgres: scanning audit event: %w", err)
		}
		if len(metadataRaw) > 0 {
			if err := json.Unmarshal(metadataRaw, &e.Metadata); err != nil {
				return nil, fmt.Errorf("postgres: decoding audit metadata: %w", err)
			}
		}
		out = append(out, e)
	}
	return out, nil
}

var _ audit.Logger = (*AuditRepo)(nil)
