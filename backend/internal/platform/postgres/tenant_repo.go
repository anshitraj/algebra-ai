package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/domain/tenant"
)

type TenantRepo struct{ db *DB }

func NewTenantRepo(db *DB) *TenantRepo { return &TenantRepo{db: db} }

func (r *TenantRepo) Create(ctx context.Context, t *tenant.Tenant) error {
	_, err := r.db.Pool.Exec(ctx, `
		INSERT INTO tenants (id, name, token_hash, created_at, revoked_at)
		VALUES ($1, $2, $3, $4, $5)`,
		t.ID, t.Name, t.TokenHash, t.CreatedAt, t.RevokedAt)
	if err != nil {
		return fmt.Errorf("postgres: inserting tenant: %w", err)
	}
	return nil
}

func (r *TenantRepo) Get(ctx context.Context, id string) (*tenant.Tenant, error) {
	row := r.db.Pool.QueryRow(ctx, `SELECT id, name, token_hash, created_at, revoked_at FROM tenants WHERE id = $1`, id)
	return scanTenant(row)
}

func (r *TenantRepo) GetByTokenHash(ctx context.Context, hash string) (*tenant.Tenant, error) {
	row := r.db.Pool.QueryRow(ctx, `SELECT id, name, token_hash, created_at, revoked_at FROM tenants WHERE token_hash = $1`, hash)
	return scanTenant(row)
}

func (r *TenantRepo) Revoke(ctx context.Context, id string, revokedAt time.Time) error {
	tag, err := r.db.Pool.Exec(ctx, `UPDATE tenants SET revoked_at = $2 WHERE id = $1`, id, revokedAt)
	if err != nil {
		return fmt.Errorf("postgres: revoking tenant: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: tenant %s", shared.ErrNotFound, id)
	}
	return nil
}

func scanTenant(row pgx.Row) (*tenant.Tenant, error) {
	var t tenant.Tenant
	if err := row.Scan(&t.ID, &t.Name, &t.TokenHash, &t.CreatedAt, &t.RevokedAt); err != nil {
		if err == pgx.ErrNoRows {
			return nil, shared.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: scanning tenant: %w", err)
	}
	return &t, nil
}
