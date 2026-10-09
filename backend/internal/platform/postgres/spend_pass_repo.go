package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
)

// SpendPassRepo backs app.SpendPassStore and app.PassSpendLedger — see
// migrations/0013_spend_passes.sql.
type SpendPassRepo struct{ db *DB }

func NewSpendPassRepo(db *DB) *SpendPassRepo { return &SpendPassRepo{db: db} }

var (
	_ app.SpendPassStore  = (*SpendPassRepo)(nil)
	_ app.PassSpendLedger = (*SpendPassRepo)(nil)
)

func (r *SpendPassRepo) Create(ctx context.Context, p *spendpass.Pass) error {
	cats, err := json.Marshal(p.AllowedCategories)
	if err != nil {
		return err
	}
	merchants, err := json.Marshal(p.AllowedMerchants)
	if err != nil {
		return err
	}
	controls, err := json.Marshal(p.Controls)
	if err != nil {
		return err
	}
	_, err = r.db.Pool.Exec(ctx, `
		INSERT INTO spend_passes (id, user_id, agent_id, label, agent_kind, currency, budget_minor_units, budget_period,
		                          max_per_purchase_minor_units, approve_above_minor_units, allowed_categories, allowed_merchants,
		                          created_at, expires_at, controls)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb,$12::jsonb,$13,$14,$15::jsonb)`,
		p.ID, p.UserID, p.AgentID, p.Label, string(p.AgentKind), p.Currency, p.BudgetMinorUnits, string(p.BudgetPeriod),
		p.MaxPerPurchaseMinorUnits, p.ApproveAboveMinorUnits, string(cats), string(merchants), p.CreatedAt, p.ExpiresAt, string(controls))
	if err != nil {
		return fmt.Errorf("postgres: inserting spend pass: %w", err)
	}
	return nil
}

const passSelect = `
	SELECT id, user_id, agent_id, label, agent_kind, currency, budget_minor_units, budget_period,
	       max_per_purchase_minor_units, approve_above_minor_units, allowed_categories, allowed_merchants,
	       created_at, expires_at, revoked_at, controls, frozen_at
	FROM spend_passes`

func scanPass(row pgx.Row) (*spendpass.Pass, error) {
	var p spendpass.Pass
	var kind, period string
	var cats, merchants, controls []byte
	if err := row.Scan(&p.ID, &p.UserID, &p.AgentID, &p.Label, &kind, &p.Currency, &p.BudgetMinorUnits, &period,
		&p.MaxPerPurchaseMinorUnits, &p.ApproveAboveMinorUnits, &cats, &merchants, &p.CreatedAt, &p.ExpiresAt, &p.RevokedAt,
		&controls, &p.FrozenAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, shared.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: scanning spend pass: %w", err)
	}
	p.AgentKind, p.BudgetPeriod = spendpass.AgentKind(kind), spendpass.Period(period)
	p.AllowedCategories, p.AllowedMerchants = []string{}, []string{}
	_ = json.Unmarshal(cats, &p.AllowedCategories)
	_ = json.Unmarshal(merchants, &p.AllowedMerchants)
	_ = json.Unmarshal(controls, &p.Controls)
	p.Controls = p.Controls.Effective()
	return &p, nil
}

// SetFrozen turns the kill switch on (at set) or off (at nil) for one pass.
func (r *SpendPassRepo) SetFrozen(ctx context.Context, id string, at *time.Time) error {
	tag, err := r.db.Pool.Exec(ctx, `UPDATE spend_passes SET frozen_at = $2 WHERE id = $1 AND revoked_at IS NULL`, id, at)
	if err != nil {
		return fmt.Errorf("postgres: freezing spend pass: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return shared.ErrNotFound
	}
	return nil
}

// SetFrozenAll turns the kill switch on or off for every live pass of a
// person, and says how many it changed.
func (r *SpendPassRepo) SetFrozenAll(ctx context.Context, userID string, at *time.Time) (int, error) {
	q := `UPDATE spend_passes SET frozen_at = $2 WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now() AND frozen_at IS NULL`
	if at == nil {
		q = `UPDATE spend_passes SET frozen_at = NULL WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now() AND frozen_at IS NOT NULL`
		tag, err := r.db.Pool.Exec(ctx, q, userID)
		if err != nil {
			return 0, fmt.Errorf("postgres: unfreezing spend passes: %w", err)
		}
		return int(tag.RowsAffected()), nil
	}
	tag, err := r.db.Pool.Exec(ctx, q, userID, at)
	if err != nil {
		return 0, fmt.Errorf("postgres: freezing spend passes: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// SetControls replaces a pass's controls.
func (r *SpendPassRepo) SetControls(ctx context.Context, id string, c spendpass.Controls) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tag, err := r.db.Pool.Exec(ctx, `UPDATE spend_passes SET controls = $2::jsonb WHERE id = $1 AND revoked_at IS NULL`, id, string(b))
	if err != nil {
		return fmt.Errorf("postgres: updating spend pass controls: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return shared.ErrNotFound
	}
	return nil
}

func (r *SpendPassRepo) Get(ctx context.Context, id string) (*spendpass.Pass, error) {
	return scanPass(r.db.Pool.QueryRow(ctx, passSelect+` WHERE id = $1`, id))
}

func (r *SpendPassRepo) GetByAgent(ctx context.Context, agentID string) (*spendpass.Pass, error) {
	return scanPass(r.db.Pool.QueryRow(ctx, passSelect+` WHERE agent_id = $1`, agentID))
}

func (r *SpendPassRepo) ListByUser(ctx context.Context, userID string) ([]spendpass.Pass, error) {
	rows, err := r.db.Pool.Query(ctx, passSelect+` WHERE user_id = $1 ORDER BY created_at DESC LIMIT 100`, userID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing spend passes: %w", err)
	}
	defer rows.Close()
	out := []spendpass.Pass{}
	for rows.Next() {
		p, err := scanPass(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (r *SpendPassRepo) Revoke(ctx context.Context, id string, at time.Time) error {
	if _, err := r.db.Pool.Exec(ctx, `UPDATE spend_passes SET revoked_at = COALESCE(revoked_at, $2) WHERE id = $1`, id, at); err != nil {
		return fmt.Errorf("postgres: revoking spend pass: %w", err)
	}
	return nil
}

// SpentByAgentSince sums what an agent's orders actually cost since a
// point in time (cancelled and failed orders don't count).
func (r *SpendPassRepo) SpentByAgentSince(ctx context.Context, agentID, currency string, since time.Time) (int64, error) {
	var total int64
	err := r.db.Pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(o.total_minor_units), 0)
		FROM orders o JOIN purchase_intents pi ON pi.id = o.intent_id
		WHERE pi.agent_id = $1 AND o.currency = $2 AND o.placed_at >= $3 AND o.status NOT IN ('CANCELLED', 'FAILED')`,
		agentID, currency, since).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("postgres: summing agent spend: %w", err)
	}
	return total, nil
}

// --- receipts ---

// ReceiptRepo backs app.ReceiptStore.
type ReceiptRepo struct{ db *DB }

func NewReceiptRepo(db *DB) *ReceiptRepo { return &ReceiptRepo{db: db} }

var _ app.ReceiptStore = (*ReceiptRepo)(nil)

func (r *ReceiptRepo) Create(ctx context.Context, rc *app.StoredReceipt) error {
	_, err := r.db.Pool.Exec(ctx, `
		INSERT INTO spend_receipts (id, user_id, order_id, pass_id, jws, created_at) VALUES ($1,$2,$3,NULLIF($4,''),$5,$6)`,
		rc.ID, rc.UserID, rc.OrderID, rc.PassID, rc.JWS, rc.CreatedAt)
	if err != nil {
		return fmt.Errorf("postgres: inserting receipt: %w", err)
	}
	return nil
}

const receiptSelect = `SELECT id, user_id, order_id, COALESCE(pass_id, ''), jws, created_at FROM spend_receipts`

func scanReceipt(row pgx.Row) (*app.StoredReceipt, error) {
	var rc app.StoredReceipt
	if err := row.Scan(&rc.ID, &rc.UserID, &rc.OrderID, &rc.PassID, &rc.JWS, &rc.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, shared.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: scanning receipt: %w", err)
	}
	return &rc, nil
}

func (r *ReceiptRepo) Get(ctx context.Context, id string) (*app.StoredReceipt, error) {
	return scanReceipt(r.db.Pool.QueryRow(ctx, receiptSelect+` WHERE id = $1`, id))
}

func (r *ReceiptRepo) GetByOrder(ctx context.Context, orderID string) (*app.StoredReceipt, error) {
	return scanReceipt(r.db.Pool.QueryRow(ctx, receiptSelect+` WHERE order_id = $1`, orderID))
}
