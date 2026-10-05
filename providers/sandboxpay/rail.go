// Package sandboxpay is a SANDBOX payment rail and a SANDBOX x402 provider
// for exercising Algebra's economic coordination end to end without real
// money. Nothing here is a real payment: every piece of evidence it
// produces carries Test=true, network "sandbox", and the provider's data is
// simulated and says so.
//
// The rail is honest about what it can prove. Like a blockchain with
// expiring blockhashes, a sandbox payment authority has a deadline after
// which it can never be captured, so an unused authority past its deadline
// is provably NOT_SETTLED. A payment the rail has no record of (the process
// restarted since) is UNKNOWN — never assumed unpaid.
package sandboxpay

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/providers/x402"
)

// Network is the x402 network name the sandbox uses.
const Network = "sandbox"

// Rail is the sandbox payment rail: app.Rail and app.PaymentAuthorizer.
type Rail struct {
	mu       sync.Mutex
	key      []byte
	payments map[string]*payment
	now      func() time.Time
	ttl      time.Duration
}

type payment struct {
	id, payTo, asset string
	amount           int64
	expires          time.Time
	captured         bool
	tx               string
}

var (
	_ app.Rail             = (*Rail)(nil)
	_ app.PaymentAuthorizer = (*Rail)(nil)
)

// NewRail makes a sandbox rail. Payment authorities expire after ttl
// (default 60s) if never captured.
func NewRail(ttl time.Duration) *Rail {
	if ttl <= 0 {
		ttl = time.Minute
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	return &Rail{key: key, payments: map[string]*payment{}, now: time.Now, ttl: ttl}
}

func (r *Rail) Name() string { return "sandbox" }

// token is the sandbox payment authority inside the X-PAYMENT payload.
type token struct {
	PaymentID string `json:"payment_id"`
	Amount    int64  `json:"amount"`
	PayTo     string `json:"pay_to"`
	Asset     string `json:"asset"`
	Expires   int64  `json:"expires"`
	MAC       string `json:"mac"`
	Sandbox   bool   `json:"sandbox"`
}

func (r *Rail) mac(t token) string {
	m := hmac.New(sha256.New, r.key)
	fmt.Fprintf(m, "%s|%d|%s|%s|%d", t.PaymentID, t.Amount, t.PayTo, t.Asset, t.Expires)
	return hex.EncodeToString(m.Sum(nil))
}

// Authorize issues a single-use sandbox payment for the provider's 402
// requirements. The amount is the provider's price, which the caller has
// already bounded by the reservation's hold.
func (r *Rail) Authorize(_ context.Context, rv *econ.Reservation, req app.PaymentRequest) (*app.PaymentAuthority, error) {
	reqs, err := x402.Select(req.Requirements, "exact", Network)
	if err != nil {
		return nil, err
	}
	amount, err := reqs.AmountMinor()
	if err != nil {
		return nil, err
	}
	if reqs.PayTo == "" {
		return nil, errors.New("sandboxpay: requirements name no payTo")
	}
	ttl := r.ttl
	if s := time.Duration(reqs.MaxTimeoutSeconds) * time.Second; s > 0 && s < ttl {
		ttl = s
	}
	now := r.now()
	t := token{PaymentID: "sbxpay_" + uuid.NewString(), Amount: amount, PayTo: reqs.PayTo, Asset: reqs.Asset, Expires: now.Add(ttl).Unix(), Sandbox: true}
	t.MAC = r.mac(t)
	payload, _ := json.Marshal(t)
	value, err := x402.PaymentPayload{Version: 1, Scheme: "exact", Network: Network, Payload: payload}.Encode()
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.payments[t.PaymentID] = &payment{id: t.PaymentID, payTo: t.PayTo, asset: t.Asset, amount: amount, expires: time.Unix(t.Expires, 0)}
	r.mu.Unlock()
	return &app.PaymentAuthority{Header: x402.HeaderPayment, Value: value, AmountMinor: amount, Evidence: econ.Evidence{
		Rail: "sandbox", Protocol: "x402", Scheme: "exact", Network: Network, Asset: reqs.Asset,
		PaymentID: t.PaymentID, AmountMinor: amount, Payer: "algebra-sandbox-payer", PayTo: reqs.PayTo, Test: true,
	}}, nil
}

// ErrCapture is why a provider couldn't collect a sandbox payment.
var ErrCapture = errors.New("sandboxpay: payment not capturable")

// Capture is the provider side: verify the X-PAYMENT value is a genuine,
// unexpired, unused sandbox authority for exactly this price and payee,
// and collect it. A payment can be captured once.
func (r *Rail) Capture(header, payTo string, price int64) (x402.SettleResponse, error) {
	p, err := x402.DecodePayment(header)
	if err != nil || p.Network != Network || p.Scheme != "exact" {
		return x402.SettleResponse{}, fmt.Errorf("%w: not a sandbox exact payment", ErrCapture)
	}
	var t token
	if err := json.Unmarshal(p.Payload, &t); err != nil || !hmac.Equal([]byte(t.MAC), []byte(r.mac(t))) {
		return x402.SettleResponse{}, fmt.Errorf("%w: signature invalid", ErrCapture)
	}
	if t.PayTo != payTo || t.Amount != price {
		return x402.SettleResponse{}, fmt.Errorf("%w: wrong payee or amount", ErrCapture)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	pay, ok := r.payments[t.PaymentID]
	switch {
	case !ok:
		return x402.SettleResponse{}, fmt.Errorf("%w: unknown payment", ErrCapture)
	case pay.captured:
		return x402.SettleResponse{}, fmt.Errorf("%w: already captured", ErrCapture)
	case !r.now().Before(pay.expires):
		return x402.SettleResponse{}, fmt.Errorf("%w: authority expired", ErrCapture)
	}
	pay.captured = true
	pay.tx = "sbxtx_" + uuid.NewString()
	return x402.SettleResponse{Success: true, Transaction: pay.tx, Network: Network, Payer: "algebra-sandbox-payer"}, nil
}

// Settlement answers from the rail's own ledger, never from the executor.
func (r *Rail) Settlement(_ context.Context, ev econ.Evidence) (app.Settlement, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pay, ok := r.payments[ev.PaymentID]
	if !ok {
		return app.Settlement{Status: app.SettlementUnknown, Network: Network, Test: true,
			Detail: "the sandbox rail has no record of this payment (it may have restarted), so it can't prove either way"}, nil
	}
	base := app.Settlement{Network: Network, Asset: pay.asset, PayTo: pay.payTo, Payer: "algebra-sandbox-payer", Test: true}
	switch {
	case pay.captured:
		base.Status, base.AmountMinor, base.Transaction = app.SettlementSettled, pay.amount, pay.tx
		base.Detail = "captured by the provider (sandbox)"
	case !r.now().Before(pay.expires):
		base.Status = app.SettlementNotSettled
		base.Detail = "the payment authority expired unused; it can never be captured"
	default:
		base.Status = app.SettlementPending
		base.Detail = "authorized, not captured yet, still capturable until " + pay.expires.UTC().Format(time.RFC3339)
	}
	return base, nil
}
