package app

import (
	"context"
	"time"

	"github.com/project-algebra/algebra/internal/domain/approval"
	"github.com/project-algebra/algebra/internal/domain/intent"
	"github.com/project-algebra/algebra/internal/domain/money"
	"github.com/project-algebra/algebra/internal/domain/order"
)

// ActivityStore answers "what has this user been doing" — the read side
// behind the console's dashboard, approvals inbox and order history. Every
// query is scoped by user_id; there is no cross-user listing.
type ActivityStore interface {
	ListIntentsByUser(ctx context.Context, userID string, limit int) ([]IntentActivity, error)
	ListOpenApprovalsByUser(ctx context.Context, userID string, now time.Time, limit int) ([]ApprovalActivity, error)
	ListOrdersByUser(ctx context.Context, userID string, limit int) ([]order.Order, error)
	CountOrdersByUser(ctx context.Context, userID string) (int, error)
}

// IntentActivity is one intent as the user sees it in a list: what they
// asked for, where it is, and (once a quote is selected) where and for how
// much.
type IntentActivity struct {
	ID          string        `json:"intent_id"`
	Status      intent.State  `json:"status"`
	Items       []intent.Item `json:"items"`
	Category    string        `json:"category,omitempty"`
	Merchant    string        `json:"merchant,omitempty"`
	Amount      *money.Amount `json:"amount,omitempty"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	CreatedByAI bool          `json:"created_by_agent"`
}

// ApprovalActivity is a pending approval plus enough of its intent to show
// the user what they're approving.
type ApprovalActivity struct {
	ApprovalID         string          `json:"approval_id"`
	IntentID           string          `json:"intent_id"`
	Status             approval.Status `json:"status"`
	Merchant           string          `json:"merchant"`
	Amount             money.Amount    `json:"amount"`
	PaymentSourceAlias string          `json:"payment_source_alias"`
	Items              []intent.Item   `json:"items"`
	CreatedAt          time.Time       `json:"created_at"`
	ExpiresAt          time.Time       `json:"expires_at"`
}

// Overview is the dashboard's headline numbers.
type Overview struct {
	SpentToday       money.Amount `json:"spent_today"`
	PendingApprovals int          `json:"pending_approvals"`
	OrdersTotal      int          `json:"orders_total"`
}

type ActivityService struct {
	store  ActivityStore
	ledger SpendLedger
	now    func() time.Time
}

func NewActivityService(store ActivityStore, ledger SpendLedger) *ActivityService {
	return &ActivityService{store: store, ledger: ledger, now: time.Now}
}

func clampLimit(limit, def, max int) int {
	if limit <= 0 {
		return def
	}
	if limit > max {
		return max
	}
	return limit
}

func (s *ActivityService) Intents(ctx context.Context, userID string, limit int) ([]IntentActivity, error) {
	return s.store.ListIntentsByUser(ctx, userID, clampLimit(limit, 25, 100))
}

func (s *ActivityService) PendingApprovals(ctx context.Context, userID string) ([]ApprovalActivity, error) {
	return s.store.ListOpenApprovalsByUser(ctx, userID, s.now(), 50)
}

func (s *ActivityService) Orders(ctx context.Context, userID string, limit int) ([]order.Order, error) {
	return s.store.ListOrdersByUser(ctx, userID, clampLimit(limit, 25, 100))
}

func (s *ActivityService) Overview(ctx context.Context, userID, currency string) (*Overview, error) {
	if currency == "" {
		currency = "INR"
	}
	spent, err := s.ledger.SpendToday(ctx, userID, currency, s.now())
	if err != nil {
		return nil, err
	}
	pending, err := s.store.ListOpenApprovalsByUser(ctx, userID, s.now(), 50)
	if err != nil {
		return nil, err
	}
	orders, err := s.store.CountOrdersByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &Overview{
		SpentToday:       money.Amount{MinorUnits: spent, Currency: currency},
		PendingApprovals: len(pending),
		OrdersTotal:      orders,
	}, nil
}
