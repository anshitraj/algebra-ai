package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/approval"
	"github.com/project-algebra/algebra/internal/domain/intent"
	"github.com/project-algebra/algebra/internal/domain/money"
	"github.com/project-algebra/algebra/internal/domain/order"
)

// ActivityRepo backs app.ActivityStore — user-scoped list reads for the
// console. Every query filters on user_id.
type ActivityRepo struct{ db *DB }

func NewActivityRepo(db *DB) *ActivityRepo { return &ActivityRepo{db: db} }

func (r *ActivityRepo) ListIntentsByUser(ctx context.Context, userID string, limit int) ([]app.IntentActivity, error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT pi.id, pi.status, pi.items, pi.constraints, pi.created_at, pi.updated_at,
		       COALESCE(q.merchant, ''), q.payload -> 'final_payable',
		       COALESCE(a.client_id, '')
		FROM purchase_intents pi
		LEFT JOIN quotes q ON q.id = pi.selected_quote_id
		LEFT JOIN agents a ON a.id = pi.agent_id
		WHERE pi.user_id = $1
		ORDER BY pi.created_at DESC
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing intents: %w", err)
	}
	defer rows.Close()

	out := []app.IntentActivity{}
	for rows.Next() {
		var it app.IntentActivity
		var status, clientID string
		var itemsRaw, constraintsRaw, finalRaw []byte
		if err := rows.Scan(&it.ID, &status, &itemsRaw, &constraintsRaw, &it.CreatedAt, &it.UpdatedAt,
			&it.Merchant, &finalRaw, &clientID); err != nil {
			return nil, fmt.Errorf("postgres: scanning intent activity: %w", err)
		}
		it.Status = intent.State(status)
		if err := json.Unmarshal(itemsRaw, &it.Items); err != nil {
			return nil, fmt.Errorf("postgres: decoding intent items: %w", err)
		}
		var c intent.Constraints
		if err := json.Unmarshal(constraintsRaw, &c); err == nil {
			it.Category = c.Category
		}
		if len(finalRaw) > 0 && string(finalRaw) != "null" {
			var amt money.Amount
			if err := json.Unmarshal(finalRaw, &amt); err == nil && amt.Currency != "" {
				it.Amount = &amt
			}
		}
		// The console's manual flow and its agent chat share one per-session
		// agent; anything else was created by an external MCP agent.
		it.CreatedByAI = clientID != app.ConsoleAgentClientID
		out = append(out, it)
	}
	return out, rows.Err()
}

func (r *ActivityRepo) ListOpenApprovalsByUser(ctx context.Context, userID string, now time.Time, limit int) ([]app.ApprovalActivity, error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT a.id, a.intent_id, a.status, a.merchant, a.amount_minor_units, a.currency,
		       a.payment_source_alias, pi.items, a.created_at, a.expires_at
		FROM approvals a
		JOIN purchase_intents pi ON pi.id = a.intent_id
		WHERE a.user_id = $1 AND a.status IN ($2, $3) AND a.expires_at > $4
		ORDER BY a.created_at DESC
		LIMIT $5`,
		userID, string(approval.StatusPending), string(approval.StatusReapprovalRequired), now, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing approvals: %w", err)
	}
	defer rows.Close()

	out := []app.ApprovalActivity{}
	for rows.Next() {
		var a app.ApprovalActivity
		var status string
		var itemsRaw []byte
		if err := rows.Scan(&a.ApprovalID, &a.IntentID, &status, &a.Merchant, &a.Amount.MinorUnits, &a.Amount.Currency,
			&a.PaymentSourceAlias, &itemsRaw, &a.CreatedAt, &a.ExpiresAt); err != nil {
			return nil, fmt.Errorf("postgres: scanning approval activity: %w", err)
		}
		a.Status = approval.Status(status)
		if err := json.Unmarshal(itemsRaw, &a.Items); err != nil {
			return nil, fmt.Errorf("postgres: decoding intent items: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *ActivityRepo) ListOrdersByUser(ctx context.Context, userID string, limit int) ([]order.Order, error) {
	rows, err := r.db.Pool.Query(ctx, orderSelectSQL+` WHERE user_id = $1 ORDER BY placed_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing orders: %w", err)
	}
	defer rows.Close()
	out := []order.Order{}
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}

func (r *ActivityRepo) CountOrdersByUser(ctx context.Context, userID string) (int, error) {
	var n int
	if err := r.db.Pool.QueryRow(ctx, `SELECT count(*) FROM orders WHERE user_id = $1`, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting orders: %w", err)
	}
	return n, nil
}

var _ app.ActivityStore = (*ActivityRepo)(nil)
