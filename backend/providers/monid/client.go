package monid

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultBaseURL is Monid's API.
const DefaultBaseURL = "https://api.monid.ai/v1"

// Client calls Monid's API with an account's key (Authorization: Bearer). It
// is the half of a future Monid rail that already exists: discovery with live
// prices, input schemas, and runs billed to the account's Monid balance.
// Algebra doesn't route payments through it yet.
type Client struct {
	BaseURL string
	Key     string
	HTTP    *http.Client
}

// NewClient builds a client; an empty key makes every call fail with
// ErrNoKey before anything is sent.
func NewClient(key string) *Client {
	return &Client{BaseURL: DefaultBaseURL, Key: strings.TrimSpace(key), HTTP: &http.Client{Timeout: 60 * time.Second}}
}

// ErrNoKey: no Monid API key is configured (MONID_API_KEY).
var ErrNoKey = errors.New("monid: no API key (create one at app.monid.ai and set MONID_API_KEY)")

// Price is how an endpoint is billed.
type Price struct {
	Type     string  `json:"type"` // PER_CALL or PER_RESULT
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

// Minor is the price in micro-dollars.
func (p Price) Minor() int64 { return micro(p.Amount) }

// Result is one endpoint discover found.
type Result struct {
	Provider     string `json:"provider"`
	ProviderName string `json:"providerName"`
	Endpoint     string `json:"endpoint"`
	Description  string `json:"description"`
	Price        Price  `json:"price"`
}

// Discover asks Monid's catalog for endpoints matching a query (limit 1-20).
func (c *Client) Discover(ctx context.Context, query string, limit int) ([]Result, error) {
	limit = min(max(limit, 1), 20)
	var out struct {
		Results []Result `json:"results"`
	}
	if err := c.post(ctx, "/discover", map[string]any{"query": query, "limit": limit}, &out); err != nil {
		return nil, err
	}
	return out.Results, nil
}

// Inspect returns an endpoint's full description, including its input
// schema, as Monid publishes it.
func (c *Client) Inspect(ctx context.Context, provider, endpoint string) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.post(ctx, "/inspect", map[string]any{"provider": provider, "endpoint": endpoint}, &out)
	return out, err
}

// Cost is an amount in Monid's billing breakdown.
type Cost struct {
	Value    int64  `json:"value"`
	Unit     string `json:"unit"` // MICRO_DOLLAR
	Currency string `json:"currency"`
}

// Run is a run's state and, once completed, its output and what was billed.
type Run struct {
	RunID            string          `json:"runId"`
	Provider         string          `json:"provider"`
	Endpoint         string          `json:"endpoint"`
	Status           string          `json:"status"` // RUNNING, COMPLETED, FAILED
	Output           json.RawMessage `json:"output"`
	ProviderResponse struct {
		HTTPStatus int             `json:"httpStatus"`
		Error      json.RawMessage `json:"error,omitempty"`
	} `json:"providerResponse"`
	Price   Price `json:"price"`
	Billing struct {
		CalculatedCost Cost `json:"calculatedCost"`
		ActualCost     Cost `json:"actualCost"`
		ReportedCost   Cost `json:"reportedCost"`
	} `json:"billing"`
	ResultCount *int `json:"resultCount"`
}

// Charged is what the run cost the balance, in micro-dollars: Monid's
// reported cost (a provider error is not charged).
func (r Run) Charged() int64 {
	if r.Billing.ReportedCost.Unit == "MICRO_DOLLAR" {
		return r.Billing.ReportedCost.Value
	}
	return 0
}

// Done reports whether the run has finished, one way or the other.
func (r Run) Done() bool { return r.Status == "COMPLETED" || r.Status == "FAILED" }

// Delivered reports whether the run finished with a success from the provider.
func (r Run) Delivered() bool {
	return r.Status == "COMPLETED" && r.ProviderResponse.HTTPStatus >= 200 && r.ProviderResponse.HTTPStatus < 300
}

// StartRun runs an endpoint. A synchronous provider answers COMPLETED; an
// asynchronous one RUNNING, to be polled with GetRun.
func (c *Client) StartRun(ctx context.Context, provider, endpoint string, input json.RawMessage) (*Run, error) {
	var out Run
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	if err := c.post(ctx, "/run", map[string]any{"provider": provider, "endpoint": endpoint, "input": input}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetRun reads a run.
func (c *Client) GetRun(ctx context.Context, runID string) (*Run, error) {
	var out Run
	if err := c.do(ctx, http.MethodGet, "/runs/"+runID, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) post(ctx context.Context, path string, body any, out any) error {
	return c.do(ctx, http.MethodPost, path, body, out)
}

// maxBody bounds a response: a run's output can be large, a catalog page isn't.
const maxBody = 8 << 20

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	if c.Key == "" {
		return ErrNoKey
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("monid: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("monid: reading the answer: %w", err)
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(b, &e)
		msg := strings.TrimSpace(e.Error + " " + e.Message)
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return fmt.Errorf("monid: %s %s answered %d: %s", method, path, resp.StatusCode, msg)
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("monid: unreadable answer: %w", err)
	}
	return nil
}
