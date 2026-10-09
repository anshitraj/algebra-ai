package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type IdempotencyRepo struct{ db *DB }

func NewIdempotencyRepo(db *DB) *IdempotencyRepo { return &IdempotencyRepo{db: db} }

// Begin reserves (key, scope) via INSERT ... ON CONFLICT DO NOTHING, which
// is atomic under Postgres's unique constraint — two concurrent callers can
// never both win the reservation. If the key is already COMPLETED, its
// stored response is returned for the caller to replay verbatim.
//
// Known simplification: if a prior call reserved the key but crashed
// before calling Complete, the row is stuck IN_PROGRESS forever and a retry
// is treated as a fresh attempt (proceeds rather than blocking) rather than
// being fenced with a lease/TTL. The ultimate double-execution guard for
// the purchase-execution path is approvals.MarkConsumed's atomic
// compare-and-swap (approval_repo.go), which this simplification does not
// weaken — it only affects how quickly a genuinely crashed retry is allowed
// to proceed.
func (r *IdempotencyRepo) Begin(ctx context.Context, key, scope string) ([]byte, bool, error) {
	tag, err := r.db.Pool.Exec(ctx, `
		INSERT INTO idempotency_keys (key, scope, status) VALUES ($1, $2, 'IN_PROGRESS')
		ON CONFLICT (key, scope) DO NOTHING`, key, scope)
	if err != nil {
		return nil, false, fmt.Errorf("postgres: reserving idempotency key: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil, false, nil // fresh reservation, caller should proceed
	}

	var status string
	var response []byte
	row := r.db.Pool.QueryRow(ctx, `SELECT status, response FROM idempotency_keys WHERE key = $1 AND scope = $2`, key, scope)
	if err := row.Scan(&status, &response); err != nil {
		if err == pgx.ErrNoRows {
			// Reservation raced and lost, then the other row vanished
			// (should not happen under a durable unique constraint) —
			// treat as a fresh attempt rather than failing the caller.
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("postgres: reading idempotency key: %w", err)
	}
	if status == "COMPLETED" {
		return response, true, nil
	}
	return nil, false, nil
}

func (r *IdempotencyRepo) Complete(ctx context.Context, key, scope string, response []byte) error {
	_, err := r.db.Pool.Exec(ctx, `
		UPDATE idempotency_keys SET status = 'COMPLETED', response = $3 WHERE key = $1 AND scope = $2`,
		key, scope, response)
	if err != nil {
		return fmt.Errorf("postgres: completing idempotency key: %w", err)
	}
	return nil
}
