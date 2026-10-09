package postgres

import (
	"context"
	"fmt"

	"github.com/project-algebra/algebra/internal/domain/tenant"
)

type WebhookEndpointRepo struct{ db *DB }

func NewWebhookEndpointRepo(db *DB) *WebhookEndpointRepo { return &WebhookEndpointRepo{db: db} }

func (r *WebhookEndpointRepo) Create(ctx context.Context, w *tenant.WebhookEndpoint) error {
	_, err := r.db.Pool.Exec(ctx, `
		INSERT INTO tenant_webhook_endpoints (id, tenant_id, url, secret, event_types, created_at, revoked_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		w.ID, w.TenantID, w.URL, w.Secret, w.EventTypes, w.CreatedAt, w.RevokedAt)
	if err != nil {
		return fmt.Errorf("postgres: inserting webhook endpoint: %w", err)
	}
	return nil
}

// ListActiveByTenant returns every non-revoked endpoint for tenantID — the
// dispatcher (app.WebhookDispatchService) filters by event type itself via
// WebhookEndpoint.Wants, since the list per tenant is small and this avoids
// an array-containment query.
func (r *WebhookEndpointRepo) ListActiveByTenant(ctx context.Context, tenantID string) ([]tenant.WebhookEndpoint, error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT id, tenant_id, url, secret, event_types, created_at, revoked_at
		FROM tenant_webhook_endpoints WHERE tenant_id = $1 AND revoked_at IS NULL`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing webhook endpoints: %w", err)
	}
	defer rows.Close()

	var out []tenant.WebhookEndpoint
	for rows.Next() {
		var w tenant.WebhookEndpoint
		if err := rows.Scan(&w.ID, &w.TenantID, &w.URL, &w.Secret, &w.EventTypes, &w.CreatedAt, &w.RevokedAt); err != nil {
			return nil, fmt.Errorf("postgres: scanning webhook endpoint: %w", err)
		}
		out = append(out, w)
	}
	return out, nil
}
