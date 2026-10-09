package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/billing"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// BillingRepo backs app.BillingStore — see migrations/0009_billing.sql.
type BillingRepo struct{ db *DB }

func NewBillingRepo(db *DB) *BillingRepo { return &BillingRepo{db: db} }

const subscriptionSelect = `
	SELECT user_id, plan, provider, provider_subscription_id, status, current_period_start, current_period_end,
	       cancel_at_period_end, created_at, updated_at
	FROM subscriptions`

func scanSubscription(row pgx.Row) (*billing.Subscription, error) {
	var s billing.Subscription
	var plan, status string
	if err := row.Scan(&s.UserID, &plan, &s.Provider, &s.ProviderSubscriptionID, &status, &s.CurrentPeriodStart,
		&s.CurrentPeriodEnd, &s.CancelAtPeriodEnd, &s.CreatedAt, &s.UpdatedAt); err != nil {
		if err == pgx.ErrNoRows {
			return nil, shared.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: scanning subscription: %w", err)
	}
	s.Plan, s.Status = billing.Plan(plan), billing.Status(status)
	return &s, nil
}

func (r *BillingRepo) GetSubscription(ctx context.Context, userID string) (*billing.Subscription, error) {
	return scanSubscription(r.db.Pool.QueryRow(ctx, subscriptionSelect+` WHERE user_id = $1`, userID))
}

func (r *BillingRepo) GetSubscriptionByProviderID(ctx context.Context, id string) (*billing.Subscription, error) {
	return scanSubscription(r.db.Pool.QueryRow(ctx, subscriptionSelect+` WHERE provider_subscription_id = $1`, id))
}

func (r *BillingRepo) UpsertSubscription(ctx context.Context, s *billing.Subscription) error {
	_, err := r.db.Pool.Exec(ctx, `
		INSERT INTO subscriptions (user_id, plan, provider, provider_subscription_id, status, current_period_start,
		                           current_period_end, cancel_at_period_end, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (user_id) DO UPDATE SET
		    plan = EXCLUDED.plan, provider = EXCLUDED.provider,
		    provider_subscription_id = EXCLUDED.provider_subscription_id, status = EXCLUDED.status,
		    current_period_start = EXCLUDED.current_period_start, current_period_end = EXCLUDED.current_period_end,
		    cancel_at_period_end = EXCLUDED.cancel_at_period_end, created_at = EXCLUDED.created_at,
		    updated_at = EXCLUDED.updated_at`,
		s.UserID, string(s.Plan), s.Provider, s.ProviderSubscriptionID, string(s.Status), s.CurrentPeriodStart,
		s.CurrentPeriodEnd, s.CancelAtPeriodEnd, s.CreatedAt, s.UpdatedAt)
	if err != nil {
		return fmt.Errorf("postgres: upserting subscription: %w", err)
	}
	return nil
}

func (r *BillingRepo) GetProviderPlanID(ctx context.Context, plan billing.Plan, amountMinor int64, currency string) (string, error) {
	var id string
	err := r.db.Pool.QueryRow(ctx, `SELECT provider_plan_id FROM billing_plans WHERE plan = $1 AND amount_minor = $2 AND currency = $3`,
		string(plan), amountMinor, currency).Scan(&id)
	if err == pgx.ErrNoRows {
		return "", shared.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("postgres: reading billing plan: %w", err)
	}
	return id, nil
}

func (r *BillingRepo) SaveProviderPlanID(ctx context.Context, plan billing.Plan, providerPlanID string, amountMinor int64, currency string) error {
	_, err := r.db.Pool.Exec(ctx, `
		INSERT INTO billing_plans (plan, amount_minor, currency, provider_plan_id) VALUES ($1,$2,$3,$4)
		ON CONFLICT (plan, amount_minor, currency) DO NOTHING`, string(plan), amountMinor, currency, providerPlanID)
	if err != nil {
		return fmt.Errorf("postgres: saving billing plan: %w", err)
	}
	return nil
}

func (r *BillingRepo) RecordEvent(ctx context.Context, eventID, event string) (bool, error) {
	tag, err := r.db.Pool.Exec(ctx, `INSERT INTO billing_events (event_id, event) VALUES ($1,$2) ON CONFLICT (event_id) DO NOTHING`, eventID, event)
	if err != nil {
		return false, fmt.Errorf("postgres: recording billing event: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// CountExecutionsSince counts orders actually placed — the thing plans are
// priced on. Failed and cancelled orders don't count.
func (r *BillingRepo) CountExecutionsSince(ctx context.Context, userID string, since time.Time) (int, error) {
	var n int
	err := r.db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM orders WHERE user_id = $1 AND placed_at >= $2 AND status NOT IN ('FAILED','CANCELLED')`,
		userID, since).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("postgres: counting executions: %w", err)
	}
	return n, nil
}

var _ app.BillingStore = (*BillingRepo)(nil)
