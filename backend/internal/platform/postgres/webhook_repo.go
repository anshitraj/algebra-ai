package postgres

import (
	"context"
	"fmt"
	"time"
)

type WebhookRepo struct{ db *DB }

func NewWebhookRepo(db *DB) *WebhookRepo { return &WebhookRepo{db: db} }

// Insert records a received webhook, deduplicating on (provider, event_id)
// per the table's UNIQUE constraint. Returns isNew=false if this exact
// provider+event was already recorded — the caller should treat that as a
// successful no-op (idempotent webhook processing), never re-apply
// whatever side effect the webhook triggers.
func (r *WebhookRepo) Insert(ctx context.Context, id, provider, eventID string, payload []byte, verified bool, receivedAt time.Time) (isNew bool, err error) {
	tag, err := r.db.Pool.Exec(ctx, `
		INSERT INTO webhook_events (id, provider, event_id, payload, verified, received_at)
		VALUES ($1, $2, $3, $4::jsonb, $5, $6)
		ON CONFLICT (provider, event_id) DO NOTHING`,
		id, provider, eventID, string(payload), verified, receivedAt)
	if err != nil {
		return false, fmt.Errorf("postgres: inserting webhook event: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *WebhookRepo) MarkProcessed(ctx context.Context, provider, eventID string, processedAt time.Time) error {
	_, err := r.db.Pool.Exec(ctx, `
		UPDATE webhook_events SET processed_at = $3 WHERE provider = $1 AND event_id = $2`,
		provider, eventID, processedAt)
	if err != nil {
		return fmt.Errorf("postgres: marking webhook processed: %w", err)
	}
	return nil
}
