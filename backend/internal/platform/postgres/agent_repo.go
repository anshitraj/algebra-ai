package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

type AgentRepo struct{ db *DB }

func NewAgentRepo(db *DB) *AgentRepo { return &AgentRepo{db: db} }

func (r *AgentRepo) Create(ctx context.Context, a *agent.Identity) error {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op if committed

	_, err = tx.Exec(ctx, `
		INSERT INTO agents (id, user_id, client_id, name, token_hash, created_at, revoked_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		a.ID, a.UserID, a.ClientID, a.Name, a.TokenHash, a.CreatedAt, a.RevokedAt)
	if err != nil {
		return fmt.Errorf("postgres: inserting agent: %w", err)
	}
	for _, perm := range a.Permissions {
		if _, err := tx.Exec(ctx, `INSERT INTO agent_permissions (agent_id, permission) VALUES ($1, $2)`, a.ID, string(perm)); err != nil {
			return fmt.Errorf("postgres: inserting agent permission: %w", err)
		}
	}
	return tx.Commit(ctx)
}

func (r *AgentRepo) Get(ctx context.Context, id string) (*agent.Identity, error) {
	row := r.db.Pool.QueryRow(ctx, `SELECT id, user_id, client_id, name, token_hash, created_at, revoked_at FROM agents WHERE id = $1`, id)
	return r.scanWithPermissions(ctx, row)
}

func (r *AgentRepo) GetByTokenHash(ctx context.Context, hash string) (*agent.Identity, error) {
	row := r.db.Pool.QueryRow(ctx, `SELECT id, user_id, client_id, name, token_hash, created_at, revoked_at FROM agents WHERE token_hash = $1`, hash)
	return r.scanWithPermissions(ctx, row)
}

func (r *AgentRepo) Revoke(ctx context.Context, id string, revokedAt time.Time) error {
	tag, err := r.db.Pool.Exec(ctx, `UPDATE agents SET revoked_at = $2 WHERE id = $1`, id, revokedAt)
	if err != nil {
		return fmt.Errorf("postgres: revoking agent: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: agent %s", shared.ErrNotFound, id)
	}
	return nil
}

func (r *AgentRepo) scanWithPermissions(ctx context.Context, row pgx.Row) (*agent.Identity, error) {
	var a agent.Identity
	if err := row.Scan(&a.ID, &a.UserID, &a.ClientID, &a.Name, &a.TokenHash, &a.CreatedAt, &a.RevokedAt); err != nil {
		if err == pgx.ErrNoRows {
			return nil, shared.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: scanning agent: %w", err)
	}

	rows, err := r.db.Pool.Query(ctx, `SELECT permission FROM agent_permissions WHERE agent_id = $1`, a.ID)
	if err != nil {
		return nil, fmt.Errorf("postgres: loading agent permissions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("postgres: scanning agent permission: %w", err)
		}
		a.Permissions = append(a.Permissions, agent.Permission(p))
	}
	return &a, nil
}
