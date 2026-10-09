package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/project-algebra/algebra/internal/domain/paymentintent"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

type PaymentIntentRepo struct{ db *DB }

func NewPaymentIntentRepo(db *DB) *PaymentIntentRepo { return &PaymentIntentRepo{db: db} }

const paymentIntentSelectSQL = `
	SELECT id, tenant_id, user_id, agent_id, COALESCE(purpose,''), merchant, COALESCE(merchant_domain,''),
	       COALESCE(category,''), international, amount_minor_units, currency, tolerance_minor_units,
	       COALESCE(product_ref,''), payment_source_alias, COALESCE(requested_capability,''), status,
	       COALESCE(policy_version,''), COALESCE(provider_transaction_id,''), COALESCE(provider_status,''),
	       COALESCE(final_amount_minor_units,0), COALESCE(final_currency,''), metadata,
	       created_at, updated_at, expires_at
	FROM agentic_payment_intents`

func (r *PaymentIntentRepo) Create(ctx context.Context, p *paymentintent.AgenticPaymentIntent) error {
	metadata, err := marshalOrNull(p.Metadata)
	if err != nil {
		return fmt.Errorf("postgres: marshaling payment intent metadata: %w", err)
	}
	_, err = r.db.Pool.Exec(ctx, `
		INSERT INTO agentic_payment_intents (
			id, tenant_id, user_id, agent_id, purpose, merchant, merchant_domain, category, international,
			amount_minor_units, currency, tolerance_minor_units, product_ref, payment_source_alias,
			requested_capability, status, policy_version, provider_transaction_id, provider_status,
			final_amount_minor_units, final_currency, metadata, created_at, updated_at, expires_at)
		VALUES ($1,$2,$3,$4,NULLIF($5,''),$6,NULLIF($7,''),NULLIF($8,''),$9,
		        $10,$11,$12,NULLIF($13,''),$14,
		        NULLIF($15,''),$16,NULLIF($17,''),NULLIF($18,''),NULLIF($19,''),
		        NULLIF($20,0),NULLIF($21,''),$22::jsonb,$23,$24,$25)`,
		p.ID, p.TenantID, p.UserID, p.AgentID, p.Purpose, p.Merchant, p.MerchantDomain, p.Category, p.International,
		p.AmountMinorUnits, p.Currency, p.ToleranceMinorUnits, p.ProductRef, p.PaymentSourceAlias,
		p.RequestedCapability, string(p.Status), p.PolicyVersion, p.ProviderTransactionID, p.ProviderStatus,
		p.FinalAmountMinorUnits, p.FinalCurrency, metadata, p.CreatedAt, p.UpdatedAt, p.ExpiresAt)
	if err != nil {
		return fmt.Errorf("postgres: inserting payment intent: %w", err)
	}
	return nil
}

func (r *PaymentIntentRepo) Get(ctx context.Context, id string) (*paymentintent.AgenticPaymentIntent, error) {
	row := r.db.Pool.QueryRow(ctx, paymentIntentSelectSQL+` WHERE id = $1`, id)
	return scanPaymentIntent(row)
}

func (r *PaymentIntentRepo) Update(ctx context.Context, p *paymentintent.AgenticPaymentIntent) error {
	metadata, err := marshalOrNull(p.Metadata)
	if err != nil {
		return fmt.Errorf("postgres: marshaling payment intent metadata: %w", err)
	}
	tag, err := r.db.Pool.Exec(ctx, `
		UPDATE agentic_payment_intents SET
			status = $2, policy_version = NULLIF($3,''), provider_transaction_id = NULLIF($4,''),
			provider_status = NULLIF($5,''), final_amount_minor_units = NULLIF($6,0),
			final_currency = NULLIF($7,''), metadata = $8::jsonb, updated_at = $9
		WHERE id = $1`,
		p.ID, string(p.Status), p.PolicyVersion, p.ProviderTransactionID, p.ProviderStatus,
		p.FinalAmountMinorUnits, p.FinalCurrency, metadata, p.UpdatedAt)
	if err != nil {
		return fmt.Errorf("postgres: updating payment intent: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: payment intent %s", shared.ErrNotFound, p.ID)
	}
	return nil
}

// ListByTenant is GET /api/v1/transactions — most recent first, capped by
// limit (callers pass a sane default; there is no cursor-based pagination
// in this build).
func (r *PaymentIntentRepo) ListByTenant(ctx context.Context, tenantID string, limit int) ([]paymentintent.AgenticPaymentIntent, error) {
	rows, err := r.db.Pool.Query(ctx, paymentIntentSelectSQL+` WHERE tenant_id = $1 ORDER BY created_at DESC LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing payment intents: %w", err)
	}
	defer rows.Close()

	var out []paymentintent.AgenticPaymentIntent
	for rows.Next() {
		p, err := scanPaymentIntentRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, nil
}

func scanPaymentIntent(row pgx.Row) (*paymentintent.AgenticPaymentIntent, error) {
	return scanPaymentIntentRow(row)
}

func scanPaymentIntentRow(row rowScanner) (*paymentintent.AgenticPaymentIntent, error) {
	var p paymentintent.AgenticPaymentIntent
	var status string
	var metadataRaw []byte
	err := row.Scan(&p.ID, &p.TenantID, &p.UserID, &p.AgentID, &p.Purpose, &p.Merchant, &p.MerchantDomain,
		&p.Category, &p.International, &p.AmountMinorUnits, &p.Currency, &p.ToleranceMinorUnits,
		&p.ProductRef, &p.PaymentSourceAlias, &p.RequestedCapability, &status,
		&p.PolicyVersion, &p.ProviderTransactionID, &p.ProviderStatus,
		&p.FinalAmountMinorUnits, &p.FinalCurrency, &metadataRaw,
		&p.CreatedAt, &p.UpdatedAt, &p.ExpiresAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, shared.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: scanning payment intent: %w", err)
	}
	p.Status = paymentintent.State(status)
	if len(metadataRaw) > 0 {
		if err := json.Unmarshal(metadataRaw, &p.Metadata); err != nil {
			return nil, fmt.Errorf("postgres: decoding payment intent metadata: %w", err)
		}
	}
	return &p, nil
}
