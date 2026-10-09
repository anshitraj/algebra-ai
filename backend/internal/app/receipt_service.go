package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/project-algebra/algebra/internal/domain/approval"
	"github.com/project-algebra/algebra/internal/domain/intent"
	"github.com/project-algebra/algebra/internal/domain/order"
	"github.com/project-algebra/algebra/internal/domain/receipt"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
)

// StoredReceipt is one issued receipt.
type StoredReceipt struct {
	ID        string
	UserID    string
	OrderID   string
	PassID    string
	JWS       string
	CreatedAt time.Time
}

// ReceiptStore persists issued receipts (migrations/0013_spend_passes.sql).
type ReceiptStore interface {
	Create(ctx context.Context, r *StoredReceipt) error
	Get(ctx context.Context, id string) (*StoredReceipt, error)
	GetByOrder(ctx context.Context, orderID string) (*StoredReceipt, error)
}

// ReceiptIssuer is how OrderService signs a receipt for a placed order and
// finds it again for the order page.
type ReceiptIssuer interface {
	Issue(ctx context.Context, ord *order.Order, pi *intent.PurchaseIntent, a *approval.Approval) (string, error)
	ForOrder(ctx context.Context, orderID string) (string, error)
}

// ReceiptService signs a receipt for every placed order and verifies
// receipts for anyone who asks.
type ReceiptService struct {
	signer    *receipt.Signer
	issuer    string
	store     ReceiptStore
	agents    AgentStore
	decisions PolicyDecisionStore
	passes    SpendPassStore
	now       func() time.Time
}

// NewReceiptService signs as issuer (the deployment's public URL).
func NewReceiptService(signer *receipt.Signer, issuer string, store ReceiptStore, agents AgentStore, decisions PolicyDecisionStore, passes SpendPassStore) *ReceiptService {
	return &ReceiptService{signer: signer, issuer: issuer, store: store, agents: agents, decisions: decisions, passes: passes, now: time.Now}
}

// Issue implements ReceiptIssuer: it signs what was authorized — the
// approval's items hash and amount, how it was approved, under which pass —
// and stores it.
func (s *ReceiptService) Issue(ctx context.Context, ord *order.Order, pi *intent.PurchaseIntent, a *approval.Approval) (string, error) {
	c := receipt.Claims{
		Issuer: s.issuer, ID: newID("rcpt"), IssuedAt: s.now().Unix(), Subject: s.signer.Pseudonym(pi.UserID),
		Merchant: ord.Merchant, MerchantOrderID: ord.MerchantOrderID,
		Amount:    receipt.Money{MinorUnits: ord.Total.MinorUnits, Currency: ord.Total.Currency},
		ItemsHash: a.ItemsHash,
		Test:      ord.ProviderMode != "real",
	}
	for _, it := range ord.Items {
		c.Items = append(c.Items, receipt.Item{Name: it.Name, Quantity: it.Quantity, UnitMinorUnits: it.UnitPrice.MinorUnits})
	}
	if ag, err := s.agents.Get(ctx, pi.AgentID); err == nil {
		c.Agent = receipt.Agent{ID: ag.ID, Name: ag.Name, Client: ag.ClientID}
	} else {
		c.Agent = receipt.Agent{ID: pi.AgentID}
	}
	c.Authorization = receipt.Authorization{Method: approvalMethod(a)}
	if a.DecidedAt != nil {
		c.Authorization.ApprovedAt = a.DecidedAt.Unix()
	}
	if d, err := s.decisions.GetLatestByIntent(ctx, pi.ID); err == nil && d != nil {
		c.Authorization.PolicyVersion = d.PolicyVersion
		c.Authorization.ReasonCodes = d.ReasonCodes
	}
	passID := ""
	if s.passes != nil {
		if p, err := s.passes.GetByAgent(ctx, pi.AgentID); err == nil {
			passID = p.ID
		}
	}
	c.Pass = passID

	jws, err := s.signer.Sign(c)
	if err != nil {
		return "", fmt.Errorf("app: signing receipt: %w", err)
	}
	if err := s.store.Create(ctx, &StoredReceipt{ID: c.ID, UserID: pi.UserID, OrderID: ord.ID, PassID: passID, JWS: jws, CreatedAt: s.now()}); err != nil {
		return "", err
	}
	return jws, nil
}

// approvalMethod: an approval created already-approved was policy's call;
// one decided later was the person's.
func approvalMethod(a *approval.Approval) receipt.Method {
	if a.DecidedAt != nil && a.DecidedAt.After(a.CreatedAt) {
		return receipt.MethodHuman
	}
	return receipt.MethodPolicy
}

// JWKS is the public key set that verifies every receipt.
func (s *ReceiptService) JWKS() receipt.JWKS { return s.signer.JWKS() }

// ForOrder returns an order's receipt, if one was issued.
func (s *ReceiptService) ForOrder(ctx context.Context, orderID string) (string, error) {
	r, err := s.store.GetByOrder(ctx, orderID)
	if err != nil {
		return "", err
	}
	return r.JWS, nil
}

// Verification is the answer to "is this receipt real?".
type Verification struct {
	Valid bool `json:"valid"`
	// Recorded: Algebra has this exact receipt on file (not merely a valid
	// signature over claims it issued).
	Recorded bool            `json:"recorded"`
	Claims   *receipt.Claims `json:"claims,omitempty"`
	// Pass is the Spend Pass's state now, if the purchase was under one.
	Pass   *PassState `json:"pass,omitempty"`
	Reason string     `json:"reason,omitempty"`
}

type PassState struct {
	Label   string `json:"label"`
	Active  bool   `json:"active"`
	Revoked bool   `json:"revoked"`
}

// Verify checks a receipt's signature and whether Algebra issued it. Open
// to anyone: a receipt reveals nothing personal.
func (s *ReceiptService) Verify(ctx context.Context, jws string) *Verification {
	c, err := receipt.Verify(jws, s.signer.JWKS())
	if err != nil {
		return &Verification{Reason: "The signature doesn't check out — this wasn't issued by this Algebra, or it was changed after signing."}
	}
	v := &Verification{Valid: true, Claims: c}
	if stored, err := s.store.Get(ctx, c.ID); err == nil && stored.JWS == jws {
		v.Recorded = true
	} else if err != nil && !errors.Is(err, shared.ErrNotFound) {
		v.Reason = "Signature valid; couldn't check the receipt log right now."
	}
	if c.Pass != "" && s.passes != nil {
		if p, err := s.passes.Get(ctx, c.Pass); err == nil {
			v.Pass = passState(p, s.now())
		}
	}
	return v
}

func passState(p *spendpass.Pass, now time.Time) *PassState {
	return &PassState{Label: p.Label, Active: p.Active(now), Revoked: p.RevokedAt != nil}
}
