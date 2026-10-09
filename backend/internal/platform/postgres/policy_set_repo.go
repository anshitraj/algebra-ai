package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/project-algebra/algebra/internal/domain/policyset"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

type PolicySetRepo struct{ db *DB }

func NewPolicySetRepo(db *DB) *PolicySetRepo { return &PolicySetRepo{db: db} }

func (r *PolicySetRepo) Create(ctx context.Context, ps *policyset.PolicySet) error {
	rules, err := json.Marshal(ps.Rules)
	if err != nil {
		return fmt.Errorf("postgres: marshaling policy rules: %w", err)
	}
	_, err = r.db.Pool.Exec(ctx, `
		INSERT INTO policy_sets (id, tenant_id, user_id, rules, version, created_at, superseded_at)
		VALUES ($1,$2,$3,$4::jsonb,$5,$6,$7)`,
		ps.ID, ps.TenantID, ps.UserID, string(rules), ps.Version, ps.CreatedAt, ps.SupersededAt)
	if err != nil {
		return fmt.Errorf("postgres: inserting policy set: %w", err)
	}
	return nil
}

// GetActive returns the current (non-superseded) policy set for the exact
// (tenantID, userID) pair — nil userID means the tenant's own default. It
// does NOT fall back from a user-specific miss to the tenant default; that
// two-tier lookup is the service layer's job (app.PolicySetService), so
// this stays a plain, predictable query.
func (r *PolicySetRepo) GetActive(ctx context.Context, tenantID string, userID *string) (*policyset.PolicySet, error) {
	row := r.db.Pool.QueryRow(ctx, `
		SELECT id, tenant_id, user_id, rules, version, created_at, superseded_at
		FROM policy_sets
		WHERE tenant_id = $1 AND user_id IS NOT DISTINCT FROM $2 AND superseded_at IS NULL
		ORDER BY version DESC LIMIT 1`, tenantID, userID)
	return scanPolicySet(row)
}

// SupersedeActive marks whatever is currently active for (tenantID, userID)
// as superseded, so a newly Create'd version becomes the only active one.
// A no-op (not an error) when nothing was active yet — the very first
// policy set for a tenant has nothing to supersede.
func (r *PolicySetRepo) SupersedeActive(ctx context.Context, tenantID string, userID *string, supersededAt time.Time) error {
	_, err := r.db.Pool.Exec(ctx, `
		UPDATE policy_sets SET superseded_at = $3
		WHERE tenant_id = $1 AND user_id IS NOT DISTINCT FROM $2 AND superseded_at IS NULL`,
		tenantID, userID, supersededAt)
	if err != nil {
		return fmt.Errorf("postgres: superseding policy set: %w", err)
	}
	return nil
}

func scanPolicySet(row pgx.Row) (*policyset.PolicySet, error) {
	var ps policyset.PolicySet
	var rulesRaw []byte
	if err := row.Scan(&ps.ID, &ps.TenantID, &ps.UserID, &rulesRaw, &ps.Version, &ps.CreatedAt, &ps.SupersededAt); err != nil {
		if err == pgx.ErrNoRows {
			return nil, shared.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: scanning policy set: %w", err)
	}
	if err := json.Unmarshal(rulesRaw, &ps.Rules); err != nil {
		return nil, fmt.Errorf("postgres: decoding policy rules: %w", err)
	}
	return &ps, nil
}
