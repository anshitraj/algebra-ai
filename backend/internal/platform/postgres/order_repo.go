package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/project-algebra/algebra/internal/domain/order"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

type OrderRepo struct{ db *DB }

func NewOrderRepo(db *DB) *OrderRepo { return &OrderRepo{db: db} }

const orderSelectSQL = `
	SELECT id, intent_id, approval_id, merchant, merchant_order_id, items, total_minor_units, currency,
	       status, provider_mode, placed_at, delivery_eta, COALESCE(receipt_url, '')
	FROM orders`

func (r *OrderRepo) Create(ctx context.Context, o *order.Order) error {
	items, err := json.Marshal(o.Items)
	if err != nil {
		return fmt.Errorf("postgres: marshaling order items: %w", err)
	}
	// user_id is looked up from the intent rather than duplicated onto the
	// domain Order type — Order doesn't need to know about users, it's
	// scoped to an intent/approval, and denormalizing here keeps the
	// user_id/placed_at index (mandate §33 spend-ledger queries) without
	// widening the domain struct for a storage concern.
	_, err = r.db.Pool.Exec(ctx, `
		INSERT INTO orders (id, intent_id, approval_id, user_id, merchant, merchant_order_id, items,
		                    total_minor_units, currency, status, provider_mode, placed_at, delivery_eta, receipt_url)
		VALUES ($1,$2,$3,(SELECT user_id FROM purchase_intents WHERE id = $2),$4,$5,$6::jsonb,$7,$8,$9,$10,$11,$12,NULLIF($13,''))`,
		o.ID, o.IntentID, o.ApprovalID, o.Merchant, o.MerchantOrderID, string(items),
		o.Total.MinorUnits, o.Total.Currency, string(o.Status), o.ProviderMode, o.PlacedAt, o.DeliveryETA, o.ReceiptURL)
	if err != nil {
		return fmt.Errorf("postgres: inserting order: %w", err)
	}
	return nil
}

func (r *OrderRepo) Get(ctx context.Context, id string) (*order.Order, error) {
	row := r.db.Pool.QueryRow(ctx, orderSelectSQL+` WHERE id = $1`, id)
	return scanOrder(row)
}

func (r *OrderRepo) GetByIntent(ctx context.Context, intentID string) (*order.Order, error) {
	row := r.db.Pool.QueryRow(ctx, orderSelectSQL+` WHERE intent_id = $1 ORDER BY placed_at DESC LIMIT 1`, intentID)
	return scanOrder(row)
}

func (r *OrderRepo) UpdateStatus(ctx context.Context, orderID string, status order.Status) error {
	tag, err := r.db.Pool.Exec(ctx, `UPDATE orders SET status = $2 WHERE id = $1`, orderID, string(status))
	if err != nil {
		return fmt.Errorf("postgres: updating order status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: order %s", shared.ErrNotFound, orderID)
	}
	return nil
}

// ListEvents returns an order's timeline — the order_events rows written by
// AddEvent, which until now were write-only.
func (r *OrderRepo) ListEvents(ctx context.Context, orderID string) ([]order.Event, error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT id, order_id, type, payload, created_at FROM order_events
		WHERE order_id = $1 ORDER BY created_at ASC`, orderID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing order events: %w", err)
	}
	defer rows.Close()

	var out []order.Event
	for rows.Next() {
		var e order.Event
		var payloadRaw []byte
		if err := rows.Scan(&e.ID, &e.OrderID, &e.Type, &payloadRaw, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("postgres: scanning order event: %w", err)
		}
		if len(payloadRaw) > 0 {
			if err := json.Unmarshal(payloadRaw, &e.Payload); err != nil {
				return nil, fmt.Errorf("postgres: decoding order event payload: %w", err)
			}
		}
		out = append(out, e)
	}
	return out, nil
}

func (r *OrderRepo) AddEvent(ctx context.Context, e order.Event) error {
	payload, err := marshalOrNull(e.Payload)
	if err != nil {
		return fmt.Errorf("postgres: marshaling order event payload: %w", err)
	}
	_, err = r.db.Pool.Exec(ctx, `INSERT INTO order_events (id, order_id, type, payload, created_at) VALUES ($1,$2,$3,$4::jsonb,$5)`,
		e.ID, e.OrderID, e.Type, payload, e.CreatedAt)
	if err != nil {
		return fmt.Errorf("postgres: inserting order event: %w", err)
	}
	return nil
}

func scanOrder(row pgx.Row) (*order.Order, error) {
	var o order.Order
	var itemsRaw []byte
	var status string
	err := row.Scan(&o.ID, &o.IntentID, &o.ApprovalID, &o.Merchant, &o.MerchantOrderID, &itemsRaw,
		&o.Total.MinorUnits, &o.Total.Currency, &status, &o.ProviderMode, &o.PlacedAt, &o.DeliveryETA, &o.ReceiptURL)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, shared.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: scanning order: %w", err)
	}
	o.Status = order.Status(status)
	if err := json.Unmarshal(itemsRaw, &o.Items); err != nil {
		return nil, fmt.Errorf("postgres: decoding order items: %w", err)
	}
	return &o, nil
}
