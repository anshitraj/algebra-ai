package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

type UserRepo struct{ db *DB }

func NewUserRepo(db *DB) *UserRepo { return &UserRepo{db: db} }

func (r *UserRepo) Create(ctx context.Context, id, email, tenantID string, createdAt time.Time) error {
	_, err := r.db.Pool.Exec(ctx, `INSERT INTO users (id, email, tenant_id, created_at) VALUES ($1, $2, NULLIF($3,''), $4)`,
		id, email, tenantID, createdAt)
	if err != nil {
		return fmt.Errorf("postgres: inserting user: %w", err)
	}
	return nil
}

func (r *UserRepo) Get(ctx context.Context, id string) (*app.UserRecord, error) {
	row := r.db.Pool.QueryRow(ctx, `SELECT id, email, COALESCE(tenant_id,''), created_at FROM users WHERE id = $1`, id)
	var u app.UserRecord
	if err := row.Scan(&u.ID, &u.Email, &u.TenantID, &u.CreatedAt); err != nil {
		if err == pgx.ErrNoRows {
			return nil, shared.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: scanning user: %w", err)
	}
	return &u, nil
}

var _ app.UserStore = (*UserRepo)(nil)
