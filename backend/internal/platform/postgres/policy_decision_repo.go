package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/policy"
)

type PolicyDecisionRepo struct{ db *DB }

func NewPolicyDecisionRepo(db *DB) *PolicyDecisionRepo { return &PolicyDecisionRepo{db: db} }

func (r *PolicyDecisionRepo) Save(ctx context.Context, intentID string, dec *policy.PolicyDecision) error {
	reasonCodes, err := json.Marshal(dec.ReasonCodes)
	if err != nil {
		return fmt.Errorf("postgres: marshaling reason codes: %w", err)
	}
	raw, err := json.Marshal(dec)
	if err != nil {
		return fmt.Errorf("postgres: marshaling policy decision: %w", err)
	}
	_, err = r.db.Pool.Exec(ctx, `
		INSERT INTO policy_decisions (id, intent_id, decision, reason_codes, policy_version, raw, evaluated_at)
		VALUES ($1, $2, $3, $4::jsonb, $5, $6::jsonb, $7)`,
		"pold_"+uuid.NewString(), intentID, string(dec.Decision), string(reasonCodes), dec.PolicyVersion, string(raw), dec.EvaluatedAt)
	if err != nil {
		return fmt.Errorf("postgres: saving policy decision: %w", err)
	}
	return nil
}

func (r *PolicyDecisionRepo) GetLatestByIntent(ctx context.Context, intentID string) (*policy.PolicyDecision, error) {
	row := r.db.Pool.QueryRow(ctx, `SELECT raw FROM policy_decisions WHERE intent_id = $1 ORDER BY evaluated_at DESC LIMIT 1`, intentID)
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		if err == pgx.ErrNoRows {
			return nil, shared.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: scanning policy decision: %w", err)
	}
	var dec policy.PolicyDecision
	if err := json.Unmarshal(raw, &dec); err != nil {
		return nil, fmt.Errorf("postgres: decoding policy decision: %w", err)
	}
	return &dec, nil
}
