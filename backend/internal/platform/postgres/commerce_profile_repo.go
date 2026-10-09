package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/project-algebra/algebra/internal/domain/commerceprofile"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

type CommerceProfileRepo struct{ db *DB }

func NewCommerceProfileRepo(db *DB) *CommerceProfileRepo { return &CommerceProfileRepo{db: db} }

func (r *CommerceProfileRepo) Get(ctx context.Context, userID string) (*commerceprofile.CommerceProfile, error) {
	row := r.db.Pool.QueryRow(ctx, `
		SELECT user_id, COALESCE(default_shipping_alias,''), COALESCE(default_payment_alias,''), preferences, updated_at
		FROM commerce_profiles WHERE user_id = $1`, userID)

	var p commerceprofile.CommerceProfile
	var prefsRaw []byte
	if err := row.Scan(&p.UserID, &p.DefaultShippingAlias, &p.DefaultPaymentAlias, &prefsRaw, &p.UpdatedAt); err != nil {
		if err == pgx.ErrNoRows {
			return nil, shared.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: scanning commerce profile: %w", err)
	}
	if err := json.Unmarshal(prefsRaw, &p.Preferences); err != nil {
		return nil, fmt.Errorf("postgres: decoding commerce profile preferences: %w", err)
	}
	return &p, nil
}

// Upsert replaces the whole row. Callers (CommerceProfileService) always
// read-modify-write the full profile first, so there's no partial-update
// ambiguity here — a caller that only wants to change one field already
// has the current value of every other field in hand before calling this.
func (r *CommerceProfileRepo) Upsert(ctx context.Context, p *commerceprofile.CommerceProfile) error {
	prefs, err := json.Marshal(p.Preferences)
	if err != nil {
		return fmt.Errorf("postgres: marshaling commerce profile preferences: %w", err)
	}
	_, err = r.db.Pool.Exec(ctx, `
		INSERT INTO commerce_profiles (user_id, default_shipping_alias, default_payment_alias, preferences, updated_at)
		VALUES ($1, NULLIF($2,''), NULLIF($3,''), $4::jsonb, $5)
		ON CONFLICT (user_id) DO UPDATE SET
			default_shipping_alias = NULLIF($2,''), default_payment_alias = NULLIF($3,''),
			preferences = $4::jsonb, updated_at = $5`,
		p.UserID, p.DefaultShippingAlias, p.DefaultPaymentAlias, string(prefs), p.UpdatedAt)
	if err != nil {
		return fmt.Errorf("postgres: upserting commerce profile: %w", err)
	}
	return nil
}
