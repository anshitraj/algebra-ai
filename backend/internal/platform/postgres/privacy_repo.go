package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/project-algebra/algebra/internal/domain/audit"
	"github.com/project-algebra/algebra/internal/domain/privacy"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

type PrivacyProfileRepo struct{ db *DB }

func NewPrivacyProfileRepo(db *DB) *PrivacyProfileRepo { return &PrivacyProfileRepo{db: db} }

func (r *PrivacyProfileRepo) Get(ctx context.Context, userID, alias string) (*privacy.StoredProfile, error) {
	row := r.db.Pool.QueryRow(ctx, `
		SELECT id, user_id, alias, type, ciphertext, nonce, created_at
		FROM private_profiles WHERE user_id = $1 AND alias = $2`, userID, alias)

	var p privacy.StoredProfile
	var profileType string
	err := row.Scan(&p.ID, &p.UserID, &p.Alias, &profileType, &p.Ciphertext, &p.Nonce, &p.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, shared.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: scanning private profile: %w", err)
	}
	p.Type = privacy.ProfileType(profileType)
	return &p, nil
}

func (r *PrivacyProfileRepo) Put(ctx context.Context, profile *privacy.StoredProfile) error {
	_, err := r.db.Pool.Exec(ctx, `
		INSERT INTO private_profiles (id, user_id, alias, type, ciphertext, nonce, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (user_id, alias) DO UPDATE SET id = EXCLUDED.id, ciphertext = EXCLUDED.ciphertext, nonce = EXCLUDED.nonce, type = EXCLUDED.type, created_at = EXCLUDED.created_at`,
		profile.ID, profile.UserID, profile.Alias, string(profile.Type), profile.Ciphertext, profile.Nonce, profile.CreatedAt)
	if err != nil {
		return fmt.Errorf("postgres: upserting private profile: %w", err)
	}
	return nil
}

func (r *PrivacyProfileRepo) ListAliases(ctx context.Context, userID string, profileType privacy.ProfileType) ([]string, error) {
	rows, err := r.db.Pool.Query(ctx, `SELECT alias FROM private_profiles WHERE user_id = $1 AND type = $2 ORDER BY alias`, userID, string(profileType))
	if err != nil {
		return nil, fmt.Errorf("postgres: listing profile aliases: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var alias string
		if err := rows.Scan(&alias); err != nil {
			return nil, fmt.Errorf("postgres: scanning alias: %w", err)
		}
		out = append(out, alias)
	}
	return out, nil
}

var _ privacy.Store = (*PrivacyProfileRepo)(nil)

// ResolutionAuditSink adapts audit.Logger into privacy.AuditSink, so every
// PrivacyResolver.Resolve* call produces a real audit_events row rather
// than privacy needing to know about the full audit.Event shape.
type ResolutionAuditSink struct {
	Logger audit.Logger
	Now    func() time.Time
}

func (s *ResolutionAuditSink) RecordResolution(ctx context.Context, userID, alias string, authz privacy.ResolveAuthorization) error {
	evt := audit.NewEvent("PrivacyProfileResolved", s.Now())
	evt.UserID = userID
	evt.AgentID = authz.RequestedBy
	evt.IntentID = authz.IntentID
	evt.Result = "resolved:" + alias + " purpose:" + authz.Purpose
	return s.Logger.Record(ctx, evt)
}

var _ privacy.AuditSink = (*ResolutionAuditSink)(nil)
