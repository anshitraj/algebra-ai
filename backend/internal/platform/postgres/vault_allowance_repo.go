package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// VaultAllowanceRepo keeps each account's allowance choice per network
// (migrations/0030_vault_allowances.sql). What is left of an allowance is
// read from the chain, never from here.
type VaultAllowanceRepo struct{ db *DB }

func NewVaultAllowanceRepo(db *DB) *VaultAllowanceRepo { return &VaultAllowanceRepo{db: db} }

var _ app.VaultAllowanceStore = (*VaultAllowanceRepo)(nil)

func (r *VaultAllowanceRepo) VaultAllowance(ctx context.Context, userID, network string) (*app.VaultAllowance, error) {
	a := app.VaultAllowance{UserID: userID, Network: network}
	err := r.db.Pool.QueryRow(ctx, `
		SELECT state, approved_minor, delegate, token_account, signature, created_at, updated_at
		FROM vault_allowances WHERE user_id = $1 AND network = $2`, userID, network).
		Scan(&a.State, &a.ApprovedMinor, &a.Delegate, &a.TokenAccount, &a.Signature, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: no allowance on %s", shared.ErrNotFound, network)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the vault allowance: %w", err)
	}
	a.CreatedAt, a.UpdatedAt = a.CreatedAt.UTC(), a.UpdatedAt.UTC()
	return &a, nil
}

func (r *VaultAllowanceRepo) SaveVaultAllowance(ctx context.Context, a *app.VaultAllowance) error {
	_, err := r.db.Pool.Exec(ctx, `
		INSERT INTO vault_allowances (user_id, network, state, approved_minor, delegate, token_account, signature, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (user_id, network) DO UPDATE SET state = EXCLUDED.state, approved_minor = EXCLUDED.approved_minor,
			delegate = EXCLUDED.delegate, token_account = EXCLUDED.token_account, signature = EXCLUDED.signature,
			updated_at = EXCLUDED.updated_at`,
		a.UserID, a.Network, a.State, a.ApprovedMinor, a.Delegate, a.TokenAccount, a.Signature, a.CreatedAt.UTC(), a.UpdatedAt.UTC())
	if err != nil {
		return fmt.Errorf("postgres: saving the vault allowance: %w", err)
	}
	return nil
}
