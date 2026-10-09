package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/project-algebra/algebra/internal/domain/approval"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

type ApprovalRepo struct{ db *DB }

func NewApprovalRepo(db *DB) *ApprovalRepo { return &ApprovalRepo{db: db} }

func (r *ApprovalRepo) Create(ctx context.Context, a *approval.Approval) error {
	_, err := r.db.Pool.Exec(ctx, `
		INSERT INTO approvals (id, intent_id, agentic_payment_intent_id, quote_id, user_id, agent_id, merchant, amount_minor_units, currency,
		                       payment_source_alias, items_hash, status, authentication_method, created_at, decided_at, expires_at)
		VALUES ($1,NULLIF($2,''),NULLIF($3,''),NULLIF($4,''),$5,$6,$7,$8,$9,$10,$11,$12,NULLIF($13,''),$14,$15,$16)`,
		a.ID, a.IntentID, a.AgenticPaymentIntentID, a.QuoteID, a.UserID, a.AgentID, a.Merchant, a.Amount.MinorUnits, a.Amount.Currency,
		a.PaymentSourceAlias, a.ItemsHash, string(a.Status), a.AuthenticationMethod, a.CreatedAt, a.DecidedAt, a.ExpiresAt)
	if err != nil {
		return fmt.Errorf("postgres: inserting approval: %w", err)
	}
	return nil
}

func (r *ApprovalRepo) Get(ctx context.Context, id string) (*approval.Approval, error) {
	row := r.db.Pool.QueryRow(ctx, approvalSelectSQL+` WHERE id = $1`, id)
	return scanApproval(row)
}

func (r *ApprovalRepo) GetByIntent(ctx context.Context, intentID string) (*approval.Approval, error) {
	row := r.db.Pool.QueryRow(ctx, approvalSelectSQL+` WHERE intent_id = $1 ORDER BY created_at DESC LIMIT 1`, intentID)
	return scanApproval(row)
}

// GetByPaymentIntent is GetByIntent's counterpart for the
// AgenticPaymentIntent flow — see approval.Approval's doc comment on why
// exactly one of the two FKs is ever set on a given row.
func (r *ApprovalRepo) GetByPaymentIntent(ctx context.Context, paymentIntentID string) (*approval.Approval, error) {
	row := r.db.Pool.QueryRow(ctx, approvalSelectSQL+` WHERE agentic_payment_intent_id = $1 ORDER BY created_at DESC LIMIT 1`, paymentIntentID)
	return scanApproval(row)
}

func (r *ApprovalRepo) Update(ctx context.Context, a *approval.Approval) error {
	tag, err := r.db.Pool.Exec(ctx, `
		UPDATE approvals SET merchant=$2, amount_minor_units=$3, currency=$4, payment_source_alias=$5,
		       items_hash=$6, status=$7, authentication_method=NULLIF($8,''), decided_at=$9, expires_at=$10
		WHERE id = $1`,
		a.ID, a.Merchant, a.Amount.MinorUnits, a.Amount.Currency, a.PaymentSourceAlias,
		a.ItemsHash, string(a.Status), a.AuthenticationMethod, a.DecidedAt, a.ExpiresAt)
	if err != nil {
		return fmt.Errorf("postgres: updating approval: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: approval %s", shared.ErrNotFound, a.ID)
	}
	return nil
}

// MarkConsumed atomically claims an approval: only succeeds if it is
// currently APPROVED, and only ever succeeds for one caller even under
// concurrent execution attempts (mandate §32). This is the compare-and-swap
// that the app.ApprovalStore.MarkConsumed contract requires.
func (r *ApprovalRepo) MarkConsumed(ctx context.Context, id string) (bool, error) {
	tag, err := r.db.Pool.Exec(ctx, `
		UPDATE approvals SET status = 'CONSUMED' WHERE id = $1 AND status = 'APPROVED'`, id)
	if err != nil {
		return false, fmt.Errorf("postgres: claiming approval: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

const approvalSelectSQL = `
	SELECT id, COALESCE(intent_id,''), COALESCE(agentic_payment_intent_id,''), COALESCE(quote_id,''), user_id, agent_id, merchant, amount_minor_units, currency,
	       payment_source_alias, items_hash, status, COALESCE(authentication_method, ''), created_at, decided_at, expires_at
	FROM approvals`

func scanApproval(row pgx.Row) (*approval.Approval, error) {
	var a approval.Approval
	var status string
	err := row.Scan(&a.ID, &a.IntentID, &a.AgenticPaymentIntentID, &a.QuoteID, &a.UserID, &a.AgentID, &a.Merchant,
		&a.Amount.MinorUnits, &a.Amount.Currency, &a.PaymentSourceAlias, &a.ItemsHash,
		&status, &a.AuthenticationMethod, &a.CreatedAt, &a.DecidedAt, &a.ExpiresAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, shared.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: scanning approval: %w", err)
	}
	a.Status = approval.Status(status)
	return &a, nil
}
