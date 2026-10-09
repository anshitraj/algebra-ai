// Package razorpay is a minimal client for the parts of Razorpay's REST API
// Algebra uses to bill for its own plans: plans, subscriptions, and
// signature verification for Checkout callbacks and webhooks. No SDK — a few
// authenticated JSON calls.
//
// It has nothing to do with paying for what users buy: Algebra stays
// non-custodial for purchases.
package razorpay

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/domain/billing"
)

const DefaultAPIBase = "https://api.razorpay.com/v1"

type Config struct {
	KeyID         string
	KeySecret     string
	WebhookSecret string
	APIBase       string // tests only
	HTTPClient    *http.Client
}

type Client struct {
	cfg  Config
	http *http.Client
}

func New(cfg Config) *Client {
	if cfg.APIBase == "" {
		cfg.APIBase = DefaultAPIBase
	}
	cfg.APIBase = strings.TrimSuffix(cfg.APIBase, "/")
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	client := *hc
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{cfg: cfg, http: &client}
}

// KeyID is the public key Checkout is opened with. Safe to send to a browser;
// the secret never is.
func (c *Client) KeyID() string { return c.cfg.KeyID }

// TestMode reports whether these are test keys (rzp_test_...).
func (c *Client) TestMode() bool { return strings.HasPrefix(c.cfg.KeyID, "rzp_test_") }

// APIError is a non-2xx response from Razorpay.
type APIError struct {
	Status      int
	Code        string
	Description string
}

func (e *APIError) Error() string {
	if e.Description != "" {
		return fmt.Sprintf("razorpay: %s (HTTP %d)", e.Description, e.Status)
	}
	return fmt.Sprintf("razorpay: HTTP %d", e.Status)
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.APIBase+path, rdr)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.cfg.KeyID, c.cfg.KeySecret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("razorpay: request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("razorpay: reading response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e struct {
			Error struct {
				Code        string `json:"code"`
				Description string `json:"description"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		return &APIError{Status: resp.StatusCode, Code: e.Error.Code, Description: e.Error.Description}
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("razorpay: decoding response: %w", err)
		}
	}
	return nil
}

// Plan is a Razorpay plan (a price + billing period).
type Plan struct {
	ID       string `json:"id"`
	Period   string `json:"period"`
	Interval int    `json:"interval"`
	Item     struct {
		Name     string `json:"name"`
		Amount   int64  `json:"amount"` // paise
		Currency string `json:"currency"`
	} `json:"item"`
}

// CreateMonthlyPlan creates a plan billed every month.
func (c *Client) CreateMonthlyPlan(ctx context.Context, name string, amountMinor int64, currency, description string) (*Plan, error) {
	var p Plan
	err := c.do(ctx, http.MethodPost, "/plans", map[string]any{
		"period":   "monthly",
		"interval": 1,
		"item":     map[string]any{"name": name, "amount": amountMinor, "currency": currency, "description": description},
	}, &p)
	return &p, err
}

func (c *Client) GetPlan(ctx context.Context, id string) (*Plan, error) {
	var p Plan
	return &p, c.do(ctx, http.MethodGet, "/plans/"+url.PathEscape(id), nil, &p)
}

// Subscription is the subset of Razorpay's subscription object Algebra reads.
type Subscription struct {
	ID           string            `json:"id"`
	PlanID       string            `json:"plan_id"`
	Status       string            `json:"status"`
	CurrentStart int64             `json:"current_start"` // unix seconds, 0 if unset
	CurrentEnd   int64             `json:"current_end"`
	ShortURL     string            `json:"short_url"`
	Notes        map[string]string `json:"notes"`
}

// CreateSubscription starts a subscription the user then authorises in
// Checkout. totalCount is the number of billing cycles (Razorpay requires
// one); notes carry our user ID so webhooks can be matched back.
func (c *Client) CreateSubscription(ctx context.Context, planID string, totalCount int, notes map[string]string) (*Subscription, error) {
	var s Subscription
	err := c.do(ctx, http.MethodPost, "/subscriptions", map[string]any{
		"plan_id":         planID,
		"total_count":     totalCount,
		"customer_notify": 1,
		"notes":           notes,
	}, &s)
	return &s, err
}

func (c *Client) GetSubscription(ctx context.Context, id string) (*Subscription, error) {
	var s Subscription
	return &s, c.do(ctx, http.MethodGet, "/subscriptions/"+url.PathEscape(id), nil, &s)
}

// CancelSubscription cancels now, or at the end of the current cycle.
func (c *Client) CancelSubscription(ctx context.Context, id string, atCycleEnd bool) (*Subscription, error) {
	flag := 0
	if atCycleEnd {
		flag = 1
	}
	var s Subscription
	err := c.do(ctx, http.MethodPost, "/subscriptions/"+url.PathEscape(id)+"/cancel", map[string]any{"cancel_at_cycle_end": flag}, &s)
	return &s, err
}

func hmacHex(secret, msg string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(msg))
	return hex.EncodeToString(m.Sum(nil))
}

// VerifySubscriptionPayment checks the signature Checkout returns after a
// subscription is authorised: HMAC-SHA256(payment_id + "|" + subscription_id)
// keyed with the API secret. Never trust the browser's "success" without it.
func (c *Client) VerifySubscriptionPayment(paymentID, subscriptionID, signature string) error {
	if paymentID == "" || subscriptionID == "" || signature == "" {
		return errors.New("razorpay: missing payment confirmation fields")
	}
	want := hmacHex(c.cfg.KeySecret, paymentID+"|"+subscriptionID)
	if !hmac.Equal([]byte(want), []byte(strings.ToLower(signature))) {
		return errors.New("razorpay: payment signature does not match")
	}
	return nil
}

// VerifyWebhook checks X-Razorpay-Signature: HMAC-SHA256 of the raw body,
// keyed with the webhook secret set in the Razorpay dashboard.
func (c *Client) VerifyWebhook(body []byte, signature string) error {
	if c.cfg.WebhookSecret == "" {
		return errors.New("razorpay: RAZORPAY_WEBHOOK_SECRET is not set; refusing unverifiable webhooks")
	}
	want := hmacHex(c.cfg.WebhookSecret, string(body))
	if signature == "" || !hmac.Equal([]byte(want), []byte(strings.ToLower(signature))) {
		return errors.New("razorpay: webhook signature does not match")
	}
	return nil
}

// Gateway adapts Client to billing.Gateway (domain types in, domain types
// out), keeping the Razorpay wire shapes inside this package.
type Gateway struct{ *Client }

func toDomain(s *Subscription) *billing.GatewaySubscription {
	out := &billing.GatewaySubscription{ID: s.ID, PlanID: s.PlanID, Status: billing.Status(s.Status), Notes: s.Notes}
	if s.CurrentStart > 0 {
		t := time.Unix(s.CurrentStart, 0).UTC()
		out.CurrentStart = &t
	}
	if s.CurrentEnd > 0 {
		t := time.Unix(s.CurrentEnd, 0).UTC()
		out.CurrentEnd = &t
	}
	return out
}

func (g Gateway) CreateMonthlyPlan(ctx context.Context, name string, amountMinor int64, currency, description string) (string, error) {
	p, err := g.Client.CreateMonthlyPlan(ctx, name, amountMinor, currency, description)
	if err != nil {
		return "", err
	}
	return p.ID, nil
}

func (g Gateway) CreateSubscription(ctx context.Context, planID string, totalCount int, notes map[string]string) (*billing.GatewaySubscription, error) {
	s, err := g.Client.CreateSubscription(ctx, planID, totalCount, notes)
	if err != nil {
		return nil, err
	}
	return toDomain(s), nil
}

func (g Gateway) GetSubscription(ctx context.Context, id string) (*billing.GatewaySubscription, error) {
	s, err := g.Client.GetSubscription(ctx, id)
	if err != nil {
		return nil, err
	}
	return toDomain(s), nil
}

func (g Gateway) CancelSubscription(ctx context.Context, id string, atCycleEnd bool) (*billing.GatewaySubscription, error) {
	s, err := g.Client.CancelSubscription(ctx, id, atCycleEnd)
	if err != nil {
		return nil, err
	}
	return toDomain(s), nil
}

var _ billing.Gateway = Gateway{}
