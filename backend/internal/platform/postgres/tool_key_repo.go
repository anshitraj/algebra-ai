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

// ToolKeyRepo backs app.ToolKeyStore — see migrations/0036_tool_keys.sql. It
// holds sealed bytes: it can neither read a key nor tell what it is.
type ToolKeyRepo struct{ db *DB }

func NewToolKeyRepo(db *DB) *ToolKeyRepo { return &ToolKeyRepo{db: db} }

var _ app.ToolKeyStore = (*ToolKeyRepo)(nil)

// PutToolKey saves a person's key for a vendor, replacing the one before.
func (r *ToolKeyRepo) PutToolKey(ctx context.Context, userID string, k app.ToolKeyRecord) error {
	_, err := r.db.Pool.Exec(ctx, `
		INSERT INTO tool_keys (user_id, vendor, sealed, nonce, hint, created_at) VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (user_id, vendor) DO UPDATE SET sealed = EXCLUDED.sealed, nonce = EXCLUDED.nonce,
		    hint = EXCLUDED.hint, created_at = EXCLUDED.created_at`,
		userID, k.Vendor, k.Sealed, k.Nonce, k.Hint, k.CreatedAt.UTC())
	if err != nil {
		return fmt.Errorf("postgres: saving a tool key: %w", err)
	}
	return nil
}

// ToolKey reads one sealed key.
func (r *ToolKeyRepo) ToolKey(ctx context.Context, userID, vendor string) (*app.ToolKeyRecord, error) {
	var k app.ToolKeyRecord
	err := r.db.Pool.QueryRow(ctx, `SELECT vendor, sealed, nonce, hint, created_at FROM tool_keys WHERE user_id = $1 AND vendor = $2`,
		userID, vendor).Scan(&k.Vendor, &k.Sealed, &k.Nonce, &k.Hint, &k.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, shared.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: reading a tool key: %w", err)
	}
	k.CreatedAt = k.CreatedAt.UTC()
	return &k, nil
}

// ToolKeys lists a person's saved keys without their sealed bytes.
func (r *ToolKeyRepo) ToolKeys(ctx context.Context, userID string) ([]app.ToolKeyRecord, error) {
	rows, err := r.db.Pool.Query(ctx, `SELECT vendor, hint, created_at FROM tool_keys WHERE user_id = $1 ORDER BY vendor`, userID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing tool keys: %w", err)
	}
	defer rows.Close()
	var out []app.ToolKeyRecord
	for rows.Next() {
		var k app.ToolKeyRecord
		var at time.Time
		if err := rows.Scan(&k.Vendor, &k.Hint, &at); err != nil {
			return nil, fmt.Errorf("postgres: listing tool keys: %w", err)
		}
		k.CreatedAt = at.UTC()
		out = append(out, k)
	}
	return out, rows.Err()
}

// DeleteToolKey removes a saved key; removing none is not an error.
func (r *ToolKeyRepo) DeleteToolKey(ctx context.Context, userID, vendor string) error {
	if _, err := r.db.Pool.Exec(ctx, `DELETE FROM tool_keys WHERE user_id = $1 AND vendor = $2`, userID, vendor); err != nil {
		return fmt.Errorf("postgres: removing a tool key: %w", err)
	}
	return nil
}
