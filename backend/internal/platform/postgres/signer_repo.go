package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/platform/payersigner"
)

// SignerRepo keeps the payer key Algebra generated for devnet, sealed — see
// migrations/0026_platform_signers.sql.
type SignerRepo struct{ db *DB }

func NewSignerRepo(db *DB) *SignerRepo { return &SignerRepo{db: db} }

var _ payersigner.Store = (*SignerRepo)(nil)

func (r *SignerRepo) Signer(ctx context.Context, cluster string) (*payersigner.Stored, error) {
	var s payersigner.Stored
	err := r.db.Pool.QueryRow(ctx, `
		SELECT cluster, public_key, sealed_key, nonce, created_at
		FROM platform_signers WHERE cluster = $1`, cluster).
		Scan(&s.Cluster, &s.PublicKey, &s.Sealed, &s.Nonce, &s.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: no signer stored for %s", shared.ErrNotFound, cluster)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading payer signer: %w", err)
	}
	s.CreatedAt = s.CreatedAt.UTC()
	return &s, nil
}

func (r *SignerRepo) SaveSigner(ctx context.Context, s *payersigner.Stored) (bool, error) {
	tag, err := r.db.Pool.Exec(ctx, `
		INSERT INTO platform_signers (cluster, public_key, sealed_key, nonce, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (cluster) DO NOTHING`,
		s.Cluster, s.PublicKey, s.Sealed, s.Nonce, s.CreatedAt.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: saving payer signer: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *SignerRepo) HasPasses(ctx context.Context, network string) (bool, error) {
	var has bool
	err := r.db.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM onchain_passes WHERE network = $1)`, network).Scan(&has)
	if err != nil {
		return false, fmt.Errorf("postgres: checking for on-chain passes: %w", err)
	}
	return has, nil
}
