package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/project-algebra/algebra/internal/domain/integrator"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

type IntegratorRepo struct{ db *DB }

func NewIntegratorRepo(db *DB) *IntegratorRepo { return &IntegratorRepo{db: db} }

func (r *IntegratorRepo) Create(ctx context.Context, i *integrator.Integrator) error {
	_, err := r.db.Pool.Exec(ctx, `
		INSERT INTO integrators (id, name, token_hash, created_at, revoked_at)
		VALUES ($1, $2, $3, $4, $5)`,
		i.ID, i.Name, i.TokenHash, i.CreatedAt, i.RevokedAt)
	if err != nil {
		return fmt.Errorf("postgres: inserting integrator: %w", err)
	}
	return nil
}

func (r *IntegratorRepo) Get(ctx context.Context, id string) (*integrator.Integrator, error) {
	row := r.db.Pool.QueryRow(ctx, `SELECT id, name, token_hash, created_at, revoked_at FROM integrators WHERE id = $1`, id)
	return scanIntegrator(row)
}

func (r *IntegratorRepo) GetByTokenHash(ctx context.Context, hash string) (*integrator.Integrator, error) {
	row := r.db.Pool.QueryRow(ctx, `SELECT id, name, token_hash, created_at, revoked_at FROM integrators WHERE token_hash = $1`, hash)
	return scanIntegrator(row)
}

func (r *IntegratorRepo) Revoke(ctx context.Context, id string, revokedAt time.Time) error {
	tag, err := r.db.Pool.Exec(ctx, `UPDATE integrators SET revoked_at = $2 WHERE id = $1`, id, revokedAt)
	if err != nil {
		return fmt.Errorf("postgres: revoking integrator: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: integrator %s", shared.ErrNotFound, id)
	}
	return nil
}

func scanIntegrator(row pgx.Row) (*integrator.Integrator, error) {
	var i integrator.Integrator
	if err := row.Scan(&i.ID, &i.Name, &i.TokenHash, &i.CreatedAt, &i.RevokedAt); err != nil {
		if err == pgx.ErrNoRows {
			return nil, shared.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: scanning integrator: %w", err)
	}
	return &i, nil
}
