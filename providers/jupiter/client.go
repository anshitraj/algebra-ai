// Package jupiter swaps USDC for a token through Jupiter's Swap API (v2), as an
// outcome Algebra runs for an agent under the person's Spend Pass.
//
// Three parts, each with its own job:
//
//   - Client speaks to Jupiter: ask for an order (a quote, and with a taker an
//     assembled transaction) and have a signed transaction landed.
//   - Runner prices a swap and runs it: it is the execution type solana_swap.
//   - Rail is the one that signs. It does not trust the transaction Jupiter
//     built: it simulates it and signs only if the wallet's own accounts would
//     change exactly as the swap says (see rail.go).
//
// Only buying with USDC is supported: the Spend Pass is a USDC budget, and
// selling a token or buying SOL would move value the pass does not measure.
package jupiter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/project-algebra/algebra/internal/platform/safehttp"
)

// DefaultBaseURL is Jupiter's Swap API v2.
const DefaultBaseURL = "https://api.jup.ag/swap/v2"

// HTTPDoer is the HTTP client used. *safehttp.Client satisfies it.
type HTTPDoer interface {
	Do(req *http.Request) (*safehttp.Response, error)
}

// Client talks to Jupiter's Swap API.
type Client struct {
	base   string
	apiKey string
	http   HTTPDoer
}

// NewClient builds a Client. An empty base is DefaultBaseURL, and an empty key
// is sent as no key (Jupiter serves a keyless tier at a lower rate).
func NewClient(base, apiKey string, h HTTPDoer) *Client {
	if base == "" {
		base = DefaultBaseURL
	}
	return &Client{base: strings.TrimRight(base, "/"), apiKey: apiKey, http: h}
}

// OrderRequest asks for an order.
type OrderRequest struct {
	InputMint, OutputMint string
	// Amount is in atoms of the input mint.
	Amount uint64
	// Taker is the wallet that would sign. Empty asks for a quote only.
	Taker string
	// SlippageBps is the most the output may fall short of the quote.
	SlippageBps int
}

// Order is Jupiter's answer: the best route's quote and, with a taker, the
// transaction to sign. Amounts are atoms in decimal strings.
type Order struct {
	// Transaction is the unsigned transaction in base64: empty without a taker,
	// or when Jupiter could not build one (see ErrorCode).
	Transaction         string
	RequestID           string
	InAmount, OutAmount string
	// OtherAmountThreshold is the least output the transaction accepts, as
	// Jupiter states it. Empty when it doesn't say.
	OtherAmountThreshold string
	Router               string
	TransactionVersion   int
	LastValidBlockHeight uint64
	FeeBps               int
	FeeMint              string
	// PriceImpactBps is how much worse the swap's value out is than its value in,
	// in basis points, as Jupiter measures it; never negative. Zero when it
	// doesn't say.
	PriceImpactBps int
	ErrorCode      *int
	ErrorMessage   string
}

type orderJSON struct {
	Transaction          *string `json:"transaction"`
	RequestID            string  `json:"requestId"`
	InAmount             string  `json:"inAmount"`
	OutAmount            string  `json:"outAmount"`
	OtherAmountThreshold string  `json:"otherAmountThreshold"`
	Router               string  `json:"router"`
	TransactionVersion   int     `json:"transactionVersion"`
	LastValidBlockHeight uint64  `json:"lastValidBlockHeight"`
	FeeBps               int     `json:"feeBps"`
	FeeMint              string  `json:"feeMint"`
	// PriceImpact is in percent and negative when the swap loses value.
	PriceImpact  *float64 `json:"priceImpact"`
	ErrorCode    *int     `json:"errorCode"`
	ErrorMessage string   `json:"errorMessage"`
}

// Order asks Jupiter for the best route. Version 0 transactions are asked for:
// the only kind the rail can sign.
func (c *Client) Order(ctx context.Context, r OrderRequest) (*Order, error) {
	q := url.Values{
		"inputMint": {r.InputMint}, "outputMint": {r.OutputMint}, "amount": {strconv.FormatUint(r.Amount, 10)},
		"maxSupportedTransactionVersion": {"0"},
	}
	if r.Taker != "" {
		q.Set("taker", r.Taker)
	}
	if r.SlippageBps > 0 {
		q.Set("slippageBps", strconv.Itoa(r.SlippageBps))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/order?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	var raw orderJSON
	if err := json.Unmarshal(resp.Body, &raw); err != nil {
		return nil, errors.New("jupiter: the order isn't valid JSON")
	}
	o := &Order{
		RequestID: raw.RequestID, InAmount: raw.InAmount, OutAmount: raw.OutAmount, OtherAmountThreshold: raw.OtherAmountThreshold, Router: raw.Router,
		TransactionVersion: raw.TransactionVersion, LastValidBlockHeight: raw.LastValidBlockHeight, FeeBps: raw.FeeBps, FeeMint: raw.FeeMint,
		ErrorCode: raw.ErrorCode, ErrorMessage: raw.ErrorMessage,
	}
	if raw.PriceImpact != nil && *raw.PriceImpact < 0 {
		// Percent to basis points, capped at the whole amount.
		o.PriceImpactBps = int(min(-*raw.PriceImpact*100, 10_000))
	}
	if raw.Transaction != nil {
		o.Transaction = *raw.Transaction
	}
	return o, nil
}

// ExecResult is Jupiter's report on a transaction it was asked to land.
type ExecResult struct {
	Status    string `json:"status"` // "Success" or "Failed"
	Signature string `json:"signature"`
	Code      int    `json:"code"`
	// TotalInputAmount is what left the wallet, fees included; the others are
	// as Jupiter accounts for the route.
	TotalInputAmount   string `json:"totalInputAmount"`
	InputAmountResult  string `json:"inputAmountResult"`
	OutputAmountResult string `json:"outputAmountResult"`
	TotalOutputAmount  string `json:"totalOutputAmount"`
	Error              string `json:"error"`
}

// Success is whether Jupiter says the swap landed.
func (e *ExecResult) Success() bool { return e.Status == "Success" && e.Code == 0 }

// Execute has Jupiter land a signed transaction and report the outcome.
func (c *Client) Execute(ctx context.Context, signedTx, requestID string, lastValidBlockHeight uint64) (*ExecResult, error) {
	body, _ := json.Marshal(map[string]any{"signedTransaction": signedTx, "requestId": requestID, "lastValidBlockHeight": lastValidBlockHeight})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/execute", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	var out ExecResult
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return nil, errors.New("jupiter: the execute answer isn't valid JSON")
	}
	return &out, nil
}

// do sends a request with the key, and turns a non-2xx answer into an error
// that carries Jupiter's own words, bounded.
func (c *Client) do(req *http.Request) (*safehttp.Response, error) {
	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		req.Header.Set("x-api-key", c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jupiter: %w", err)
	}
	if resp.Status < 200 || resp.Status >= 300 {
		return nil, &HTTPError{Status: resp.Status, Body: snippet(resp.Body)}
	}
	return resp, nil
}

// HTTPError is Jupiter answering something other than 2xx.
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("jupiter: answered HTTP %d", e.Status)
	}
	return fmt.Sprintf("jupiter: answered HTTP %d: %s", e.Status, e.Body)
}

func snippet(b []byte) string {
	s := strings.Join(strings.Fields(string(b)), " ")
	if len(s) > 200 {
		s = s[:200]
	}
	return strings.ToValidUTF8(s, "")
}
