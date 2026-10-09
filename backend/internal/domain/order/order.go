// Package order defines the Order entity — created only after a merchant
// connector confirms a real (or, in dev, explicitly mock) checkout. Algebra
// never synthesizes an Order to make a flow look complete.
package order

import (
	"time"

	"github.com/project-algebra/algebra/internal/domain/money"
)

type Status string

const (
	StatusPlaced    Status = "PLACED"
	StatusConfirmed Status = "CONFIRMED"
	StatusShipped   Status = "SHIPPED"
	StatusDelivered Status = "DELIVERED"
	StatusCancelled Status = "CANCELLED"
	StatusFailed    Status = "FAILED"
)

type Item struct {
	MerchantProductID string       `json:"merchant_product_id"`
	Name              string       `json:"name"`
	Quantity          int          `json:"quantity"`
	UnitPrice         money.Amount `json:"unit_price"`
}

// Order is the receipt-bearing record of a completed (or in-flight)
// merchant checkout.
type Order struct {
	ID              string       `json:"order_id"`
	IntentID        string       `json:"intent_id"`
	ApprovalID      string       `json:"approval_id"`
	Merchant        string       `json:"merchant"`
	MerchantOrderID string       `json:"merchant_order_id"`
	Items           []Item       `json:"items"`
	Total           money.Amount `json:"total"`
	Status          Status       `json:"status"`
	PlacedAt        time.Time    `json:"placed_at"`
	DeliveryETA     *time.Time   `json:"delivery_eta,omitempty"`
	ReceiptURL      string       `json:"receipt_url,omitempty"`

	// ProviderMode is "mock" | "sandbox" | "real" — copied from the
	// connector that produced this order, so a mock order can never be
	// mistaken for a real one downstream (UI, receipts, exports).
	ProviderMode string `json:"provider_mode"`
}

// Event is one entry in an order's timeline (order_events table).
type Event struct {
	ID        string         `json:"id"`
	OrderID   string         `json:"order_id"`
	Type      string         `json:"type"`
	Payload   map[string]any `json:"payload,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}
