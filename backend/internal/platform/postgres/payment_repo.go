package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/project-algebra/algebra/internal/domain/payment"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

type PaymentSourceRepo struct{ db *DB }

func NewPaymentSourceRepo(db *DB) *PaymentSourceRepo { return &PaymentSourceRepo{db: db} }

const paymentSourceSelectSQL = `
	SELECT id, user_id, alias, type, provider_token_ref, provider_mode, network, last4, issuer_meta,
	       COALESCE(expiry_meta,''), COALESCE(nickname,''), COALESCE(billing_profile_id,''),
	       capabilities, created_at, revoked_at
	FROM payment_sources`

func (r *PaymentSourceRepo) Create(ctx context.Context, s *payment.PaymentSource) error {
	issuerMeta, err := marshalOrNull(s.IssuerMeta)
	if err != nil {
		return fmt.Errorf("postgres: marshaling issuer metadata: %w", err)
	}
	capabilities, err := json.Marshal(s.Capabilities)
	if err != nil {
		return fmt.Errorf("postgres: marshaling capabilities: %w", err)
	}
	providerMode := string(s.ProviderMode)
	if providerMode == "" {
		providerMode = string(payment.ProviderModeSandbox)
	}

	_, err = r.db.Pool.Exec(ctx, `
		INSERT INTO payment_sources (id, user_id, alias, type, provider_token_ref, provider_mode, network, last4,
		                             issuer_meta, expiry_meta, nickname, billing_profile_id, capabilities, created_at, revoked_at)
		VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),NULLIF($8,''),$9::jsonb,NULLIF($10,''),NULLIF($11,''),NULLIF($12,''),$13::jsonb,$14,$15)`,
		s.ID, s.UserID, s.Alias, string(s.Type), s.ProviderTokenRef, providerMode, s.Network, s.Last4,
		issuerMeta, s.ExpiryMeta, s.Nickname, s.BillingProfileID, string(capabilities), s.CreatedAt, s.RevokedAt)
	if err != nil {
		return fmt.Errorf("postgres: inserting payment source: %w", err)
	}
	return nil
}

func (r *PaymentSourceRepo) List(ctx context.Context, userID string) ([]payment.PaymentSource, error) {
	rows, err := r.db.Pool.Query(ctx, paymentSourceSelectSQL+` WHERE user_id = $1 ORDER BY created_at ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing payment sources: %w", err)
	}
	defer rows.Close()

	var out []payment.PaymentSource
	for rows.Next() {
		s, err := scanPaymentSourceRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, nil
}

func (r *PaymentSourceRepo) GetByAlias(ctx context.Context, userID, alias string) (*payment.PaymentSource, error) {
	row := r.db.Pool.QueryRow(ctx, paymentSourceSelectSQL+` WHERE user_id = $1 AND alias = $2`, userID, alias)
	return scanPaymentSourceRow(row)
}

// GetByID looks up a payment source by its raw ID regardless of owner — the
// caller (PaymentService.getOwned) is responsible for the ownership check;
// see its doc comment for why that's done in the service layer rather than
// folded into this query.
func (r *PaymentSourceRepo) GetByID(ctx context.Context, id string) (*payment.PaymentSource, error) {
	row := r.db.Pool.QueryRow(ctx, paymentSourceSelectSQL+` WHERE id = $1`, id)
	return scanPaymentSourceRow(row)
}

func (r *PaymentSourceRepo) Revoke(ctx context.Context, id string, revokedAt time.Time) error {
	tag, err := r.db.Pool.Exec(ctx, `UPDATE payment_sources SET revoked_at = $2 WHERE id = $1`, id, revokedAt)
	if err != nil {
		return fmt.Errorf("postgres: revoking payment source: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: payment source %s", shared.ErrNotFound, id)
	}
	return nil
}

// rowScanner is satisfied by both pgx.Row (QueryRow) and pgx.Rows (Query),
// letting List and GetByAlias share one scan function.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanPaymentSourceRow(row rowScanner) (*payment.PaymentSource, error) {
	var s payment.PaymentSource
	var sourceType, providerMode string
	var issuerMetaRaw, capabilitiesRaw []byte

	err := row.Scan(&s.ID, &s.UserID, &s.Alias, &sourceType, &s.ProviderTokenRef, &providerMode, &s.Network, &s.Last4,
		&issuerMetaRaw, &s.ExpiryMeta, &s.Nickname, &s.BillingProfileID, &capabilitiesRaw, &s.CreatedAt, &s.RevokedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, shared.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: scanning payment source: %w", err)
	}
	s.Type = payment.SourceType(sourceType)
	s.ProviderMode = payment.ProviderMode(providerMode)
	if len(issuerMetaRaw) > 0 {
		if err := json.Unmarshal(issuerMetaRaw, &s.IssuerMeta); err != nil {
			return nil, fmt.Errorf("postgres: decoding issuer metadata: %w", err)
		}
	}
	if err := json.Unmarshal(capabilitiesRaw, &s.Capabilities); err != nil {
		return nil, fmt.Errorf("postgres: decoding capabilities: %w", err)
	}
	return &s, nil
}
