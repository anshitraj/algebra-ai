package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// ResultRepo backs app.ResultBlobStore — see migrations/0016_intent_results.sql.
// It holds sealed bytes: it can neither read a result nor tell what it is.
type ResultRepo struct{ db *DB }

func NewResultRepo(db *DB) *ResultRepo { return &ResultRepo{db: db} }

var _ app.ResultBlobStore = (*ResultRepo)(nil)

// SaveSealed inserts the result for an intent, or replaces it: the committed
// attempt's answer is the one that stays.
func (r *ResultRepo) SaveSealed(ctx context.Context, s app.SealedResult) error {
	_, err := r.db.Pool.Exec(ctx, `
		INSERT INTO intent_results (intent_id, principal_id, reservation_id, content_type, http_status, size_bytes, body_sha256, sealed, nonce, stored_at, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (intent_id) DO UPDATE SET
		    principal_id = EXCLUDED.principal_id, reservation_id = EXCLUDED.reservation_id, content_type = EXCLUDED.content_type,
		    http_status = EXCLUDED.http_status, size_bytes = EXCLUDED.size_bytes, body_sha256 = EXCLUDED.body_sha256,
		    sealed = EXCLUDED.sealed, nonce = EXCLUDED.nonce, stored_at = EXCLUDED.stored_at, expires_at = EXCLUDED.expires_at`,
		s.IntentID, s.PrincipalID, s.ReservationID, s.ContentType, s.HTTPStatus, s.Size, s.SHA256, s.Sealed, s.Nonce, s.StoredAt.UTC(), s.ExpiresAt.UTC())
	if err != nil {
		return fmt.Errorf("postgres: saving a result: %w", err)
	}
	return nil
}

// GetSealed returns the result for an intent unless it has expired at now.
func (r *ResultRepo) GetSealed(ctx context.Context, intentID string, now time.Time) (*app.SealedResult, error) {
	var s app.SealedResult
	err := r.db.Pool.QueryRow(ctx, `
		SELECT intent_id, principal_id, reservation_id, content_type, http_status, size_bytes, body_sha256, sealed, nonce, stored_at, expires_at
		FROM intent_results WHERE intent_id = $1 AND expires_at > $2`, intentID, now.UTC()).
		Scan(&s.IntentID, &s.PrincipalID, &s.ReservationID, &s.ContentType, &s.HTTPStatus, &s.Size, &s.SHA256, &s.Sealed, &s.Nonce, &s.StoredAt, &s.ExpiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, shared.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: reading a result: %w", err)
	}
	s.StoredAt, s.ExpiresAt = s.StoredAt.UTC(), s.ExpiresAt.UTC()
	return &s, nil
}

// DeleteExpired removes the results whose time is up.
func (r *ResultRepo) DeleteExpired(ctx context.Context, now time.Time) (int, error) {
	tag, err := r.db.Pool.Exec(ctx, `DELETE FROM intent_results WHERE expires_at <= $1`, now.UTC())
	if err != nil {
		return 0, fmt.Errorf("postgres: purging results: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
