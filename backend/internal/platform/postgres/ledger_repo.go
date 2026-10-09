package postgres

import (
	"context"
	"fmt"
	"time"
)

type SpendLedgerRepo struct{ db *DB }

func NewSpendLedgerRepo(db *DB) *SpendLedgerRepo { return &SpendLedgerRepo{db: db} }

// SpendToday sums actually-charged order totals for userID/currency since
// the start of now's calendar day (UTC). This is what makes the daily
// spend-limit policy check tamper-proof: it reads real placed orders, never
// a client-supplied running total.
func (r *SpendLedgerRepo) SpendToday(ctx context.Context, userID, currency string, now time.Time) (int64, error) {
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	var total *int64
	row := r.db.Pool.QueryRow(ctx, `
		SELECT SUM(total_minor_units) FROM orders
		WHERE user_id = $1 AND currency = $2 AND placed_at >= $3
		  AND status NOT IN ('CANCELLED', 'FAILED')`,
		userID, currency, startOfDay)
	if err := row.Scan(&total); err != nil {
		return 0, fmt.Errorf("postgres: summing today's spend: %w", err)
	}
	if total == nil {
		return 0, nil
	}
	return *total, nil
}
