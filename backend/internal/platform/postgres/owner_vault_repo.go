package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// OwnerVaultRepo keeps each account's vault addresses
// (migrations/0029_owner_vaults.sql). No key is ever stored.
type OwnerVaultRepo struct{ db *DB }

func NewOwnerVaultRepo(db *DB) *OwnerVaultRepo { return &OwnerVaultRepo{db: db} }

var _ app.OwnerVaultStore = (*OwnerVaultRepo)(nil)

func (r *OwnerVaultRepo) OwnerVault(ctx context.Context, userID string) (*app.OwnerVaultRecord, error) {
	v := app.OwnerVaultRecord{UserID: userID}
	err := r.db.Pool.QueryRow(ctx, `
		SELECT solana_address, evm_address, proof_message, created_at, updated_at FROM owner_vaults WHERE user_id = $1`, userID).
		Scan(&v.SolanaAddress, &v.EVMAddress, &v.ProofMessage, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, shared.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the vault: %w", err)
	}
	v.CreatedAt, v.UpdatedAt = v.CreatedAt.UTC(), v.UpdatedAt.UTC()
	return &v, nil
}

func (r *OwnerVaultRepo) SaveOwnerVault(ctx context.Context, v *app.OwnerVaultRecord) error {
	_, err := r.db.Pool.Exec(ctx, `
		INSERT INTO owner_vaults (user_id, solana_address, evm_address, proof_message, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id) DO UPDATE SET solana_address = EXCLUDED.solana_address, evm_address = EXCLUDED.evm_address,
			proof_message = EXCLUDED.proof_message, updated_at = EXCLUDED.updated_at`,
		v.UserID, v.SolanaAddress, v.EVMAddress, v.ProofMessage, v.CreatedAt, v.UpdatedAt)
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return shared.ErrConflict // another account's address
	}
	if err != nil {
		return fmt.Errorf("postgres: saving the vault: %w", err)
	}
	return nil
}
