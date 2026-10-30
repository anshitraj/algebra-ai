package postgres

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/project-algebra/algebra/internal/app"
)

// The login-code table (migrations/0028_login_codes.sql): one live code per
// address, held as keyed hashes. AccountRepo keeps it beside sessions and
// reset tokens.

var _ app.LoginCodeStore = (*AccountRepo)(nil)

// LoginCodeSentAt is when the address's live code was made; the zero time when
// it has none (or it has expired).
func (r *AccountRepo) LoginCodeSentAt(ctx context.Context, addressKey string) (time.Time, error) {
	var at time.Time
	err := r.db.Pool.QueryRow(ctx, `SELECT created_at FROM login_codes WHERE address_key = $1 AND expires_at > now()`, addressKey).Scan(&at)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("postgres: reading a login code: %w", err)
	}
	return at.UTC(), nil
}

// PutLoginCode makes codeHash the address's one live code.
func (r *AccountRepo) PutLoginCode(ctx context.Context, addressKey, codeHash string, at, expires time.Time) error {
	_, err := r.db.Pool.Exec(ctx, `
		INSERT INTO login_codes (address_key, code_hash, tries, created_at, expires_at)
		VALUES ($1, $2, 0, $3, $4)
		ON CONFLICT (address_key) DO UPDATE
		SET code_hash = EXCLUDED.code_hash, tries = 0, created_at = EXCLUDED.created_at, expires_at = EXCLUDED.expires_at`,
		addressKey, codeHash, at.UTC(), expires.UTC())
	if err != nil {
		return fmt.Errorf("postgres: storing a login code: %w", err)
	}
	return nil
}

// TryLoginCode checks a code in one transaction, with the row locked so two
// guesses at once are counted one after the other: the right code is used up,
// a wrong one is counted, and the maxTries-th wrong one burns the code.
func (r *AccountRepo) TryLoginCode(ctx context.Context, addressKey, codeHash string, now time.Time, maxTries int) (app.LoginCodeOutcome, error) {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return app.LoginCodeGone, fmt.Errorf("postgres: checking a login code: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit

	var stored string
	var tries int
	var expires time.Time
	err = tx.QueryRow(ctx, `SELECT code_hash, tries, expires_at FROM login_codes WHERE address_key = $1 FOR UPDATE`, addressKey).Scan(&stored, &tries, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.LoginCodeGone, nil
	}
	if err != nil {
		return app.LoginCodeGone, fmt.Errorf("postgres: checking a login code: %w", err)
	}
	finish := func(outcome app.LoginCodeOutcome, query string, args ...any) (app.LoginCodeOutcome, error) {
		if _, err := tx.Exec(ctx, query, args...); err != nil {
			return app.LoginCodeGone, fmt.Errorf("postgres: checking a login code: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return app.LoginCodeGone, fmt.Errorf("postgres: checking a login code: %w", err)
		}
		return outcome, nil
	}
	if !now.Before(expires) {
		return finish(app.LoginCodeGone, `DELETE FROM login_codes WHERE address_key = $1`, addressKey)
	}
	if subtle.ConstantTimeCompare([]byte(stored), []byte(codeHash)) == 1 {
		return finish(app.LoginCodeAccepted, `DELETE FROM login_codes WHERE address_key = $1`, addressKey)
	}
	if tries+1 >= maxTries {
		return finish(app.LoginCodeGone, `DELETE FROM login_codes WHERE address_key = $1`, addressKey)
	}
	return finish(app.LoginCodeWrong, `UPDATE login_codes SET tries = tries + 1 WHERE address_key = $1`, addressKey)
}
