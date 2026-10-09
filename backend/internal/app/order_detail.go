package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/project-algebra/algebra/internal/domain/order"
	"github.com/project-algebra/algebra/internal/domain/privacy"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// OrderDetail is one order as its owner sees it: the order, its timeline,
// and where it's going — what the console's order page, invoice and map
// are built from.
type OrderDetail struct {
	Order    order.Order   `json:"order"`
	Events   []order.Event `json:"events"`
	Category string        `json:"category,omitempty"`
	// Simulated is true for an order no real merchant received (demo
	// checkout, mock store): no money moved, nothing ships.
	Simulated bool `json:"simulated"`
	// ShipTo is the delivery address, resolved for the owner only (and
	// audited as such). Nil when the order had no delivery profile.
	ShipTo       *privacy.ShippingProfile `json:"ship_to,omitempty"`
	ShipToAlias  string                   `json:"ship_to_alias,omitempty"`
	PaymentAlias string                   `json:"payment_alias,omitempty"`
	// Receipt is the signed spend receipt (compact JWS), if one was issued.
	Receipt string `json:"receipt,omitempty"`
}

// OrderForUser returns one of the user's own orders. Anyone else's order
// reports ErrNotFound, so the check never confirms an ID exists. This is a
// human-session read: agents get orders through their intents instead.
func (s *OrderService) OrderForUser(ctx context.Context, userID, orderID string) (*OrderDetail, error) {
	ord, err := s.orders.Get(ctx, orderID)
	if err != nil {
		return nil, err
	}
	pi, err := s.intents.Get(ctx, ord.IntentID)
	if err != nil {
		return nil, err
	}
	if pi.UserID != userID {
		return nil, fmt.Errorf("%w: order %s", shared.ErrNotFound, orderID)
	}
	events, err := s.orders.ListEvents(ctx, orderID)
	if err != nil {
		return nil, err
	}
	d := &OrderDetail{
		Order: *ord, Events: events, Category: pi.Constraints.Category,
		Simulated:    ord.ProviderMode != "real",
		ShipToAlias:  pi.Constraints.DeliveryProfile,
		PaymentAlias: pi.Constraints.PaymentProfile,
	}
	if d.Events == nil {
		d.Events = []order.Event{}
	}
	if s.receipts != nil {
		if jws, err := s.receipts.ForOrder(ctx, orderID); err == nil {
			d.Receipt = jws
		}
	}
	if alias := pi.Constraints.DeliveryProfile; alias != "" && s.privacy != nil {
		shipTo, err := s.privacy.ResolveShipping(ctx, userID, alias, privacy.ResolveAuthorization{
			Purpose: "order_view_by_owner", IntentID: pi.ID, RequestedBy: "user:" + userID,
		})
		switch {
		case err == nil:
			d.ShipTo = shipTo
		case errors.Is(err, shared.ErrNotFound):
			// The address was removed since; the order stands without it.
		default:
			return nil, err
		}
	}
	return d, nil
}
