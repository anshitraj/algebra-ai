// Package flipkart searches Flipkart's catalog through the official Flipkart
// Affiliate API (https://affiliate.flipkart.com/api-docs/), using the
// affiliate ID and token an operator gets by joining the Flipkart Affiliate
// program.
//
// What it can and cannot do:
//
//   - Search: yes, once FLIPKART_AFFILIATE_ID and FLIPKART_AFFILIATE_TOKEN
//     are set — live catalog data (title, brand, Flipkart price, stock,
//     product link) from GET /affiliate/1.0/search.json.
//   - Deals: yes, with the same credentials — Flipkart's published offers
//     and Deals of the Day from the offers API (see deals.go). They are
//     shown to the user, never applied: there is no cart to apply them to.
//   - Cart, checkout, order tracking: no. The Affiliate API is a read-only
//     catalog/offers API, and Flipkart publishes no third-party cart or order
//     API and no MCP server. Flipkart therefore never produces a checkout
//     quote in Algebra; its search results carry the product link for the
//     user to buy on Flipkart themselves.
//   - Without credentials every capability is false and agents get a link
//     to Flipkart's own search page instead.
//
// Nothing here scrapes flipkart.com.
package flipkart

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/project-algebra/algebra/connectors/sanitize"
	"github.com/project-algebra/algebra/internal/domain/merchant"
	"github.com/project-algebra/algebra/internal/domain/order"
	"github.com/project-algebra/algebra/internal/domain/quote"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

const (
	Name           = "flipkart"
	DefaultBaseURL = "https://affiliate-api.flipkart.net/affiliate/1.0"
	DocsURL        = "https://affiliate.flipkart.com/api-docs/af_prod_ref.html"

	searchPageURL = "https://www.flipkart.com/search"
	homeURL       = "https://www.flipkart.com/"
	maxResults    = 10
	maxBodyBytes  = 4 << 20
)

type Config struct {
	AffiliateID    string
	AffiliateToken string
	// BaseURL defaults to DefaultBaseURL and OffersBaseURL to
	// DefaultOffersBaseURL. Both must be https (loopback http is accepted
	// for tests).
	BaseURL       string
	OffersBaseURL string
	HTTPClient    *http.Client
}

type Connector struct {
	cfg       Config
	http      *http.Client
	configErr string
	now       func() time.Time

	// offers caches the offers feed — see offerFeed.
	offersMu      sync.Mutex
	offers        []feedOffer
	offersFetched time.Time
}

func New(cfg Config) *Connector {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	cfg.BaseURL = strings.TrimSuffix(cfg.BaseURL, "/")
	if cfg.OffersBaseURL == "" {
		cfg.OffersBaseURL = DefaultOffersBaseURL
	}
	cfg.OffersBaseURL = strings.TrimSuffix(cfg.OffersBaseURL, "/")
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	client := *hc
	// The affiliate token travels in a custom header, which net/http would
	// carry across a redirect even to another host. The API has no reason to
	// redirect, so nothing is followed.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	c := &Connector{cfg: cfg, http: &client, now: time.Now}
	switch {
	case cfg.AffiliateID == "" || cfg.AffiliateToken == "":
		c.configErr = "Set FLIPKART_AFFILIATE_ID and FLIPKART_AFFILIATE_TOKEN (from the Flipkart Affiliate program) to enable catalog search and offers. Meanwhile agents get a link to Flipkart's own search page."
	case !safeBaseURL(cfg.BaseURL) || !safeBaseURL(cfg.OffersBaseURL):
		c.configErr = "Flipkart affiliate API base URLs must be https."
	}
	return c
}

func safeBaseURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return u.Scheme == "http" && (u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback()))
}

func (c *Connector) configured() bool { return c.configErr == "" }

func (c *Connector) Name() string                { return Name }
func (c *Connector) Mode() merchant.ProviderMode { return merchant.ProviderModeReal }

func (c *Connector) Capabilities() merchant.Capabilities {
	return merchant.Capabilities{Search: c.configured()}
}

func (c *Connector) Status() merchant.Status {
	if !c.configured() {
		return merchant.Status{Integration: merchant.IntegrationAffiliateAPI, Ready: false, Detail: c.configErr, Source: DocsURL}
	}
	return merchant.Status{
		Integration: merchant.IntegrationAffiliateAPI,
		Ready:       true,
		Detail:      "Catalog search and published offers via the official Flipkart Affiliate API. Flipkart publishes no third-party cart or order API, so buying happens on Flipkart through the product link.",
		Source:      DocsURL,
	}
}

// HandoffURL returns Flipkart's public search page for query — a link for a
// human to open; Algebra never fetches it.
func (c *Connector) HandoffURL(query string) string {
	q := strings.TrimSpace(query)
	if q == "" {
		return homeURL
	}
	return searchPageURL + "?q=" + url.QueryEscape(q)
}

type price struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

type productEntry struct {
	ProductBaseInfoV1 struct {
		ProductID            string `json:"productId"`
		Title                string `json:"title"`
		ProductBrand         string `json:"productBrand"`
		InStock              bool   `json:"inStock"`
		ProductURL           string `json:"productUrl"`
		CategoryPath         string `json:"categoryPath"`
		FlipkartSellingPrice price  `json:"flipkartSellingPrice"`
		FlipkartSpecialPrice price  `json:"flipkartSpecialPrice"`
	} `json:"productBaseInfoV1"`
}

// searchResponse accepts both documented result keys: the API reference
// names the array productInfoList; responses in the wild (and client
// libraries) use products. Both hold productBaseInfoV1 objects.
type searchResponse struct {
	ProductInfoList []productEntry `json:"productInfoList"`
	Products        []productEntry `json:"products"`
}

func (c *Connector) SearchProducts(ctx context.Context, query string, limit int) ([]merchant.Product, error) {
	if !c.configured() {
		return nil, fmt.Errorf("%w: %s", shared.ErrNotImplemented, c.configErr)
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, errors.New("flipkart: empty search query")
	}
	if limit <= 0 || limit > maxResults {
		limit = maxResults
	}
	endpoint := c.cfg.BaseURL + "/search.json?" + url.Values{"query": {q}, "resultCount": {strconv.Itoa(limit)}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("flipkart: building search request: %w", err)
	}
	req.Header.Set("Fk-Affiliate-Id", c.cfg.AffiliateID)
	req.Header.Set("Fk-Affiliate-Token", c.cfg.AffiliateToken)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("flipkart: search request failed: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("flipkart: affiliate API rejected the credentials (HTTP %d)", resp.StatusCode)
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, errors.New("flipkart: affiliate API rate limit reached")
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("flipkart: affiliate API returned HTTP %d", resp.StatusCode)
	}

	var body searchResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(&body); err != nil {
		return nil, fmt.Errorf("flipkart: decoding search response: %w", err)
	}
	entries := body.ProductInfoList
	if len(entries) == 0 {
		entries = body.Products
	}
	out := make([]merchant.Product, 0, len(entries))
	for _, e := range entries {
		if len(out) >= limit {
			break
		}
		info := e.ProductBaseInfoV1
		p := info.FlipkartSpecialPrice
		if p.Amount <= 0 {
			p = info.FlipkartSellingPrice
		}
		minor, ok := toPaise(p)
		if info.ProductID == "" || info.Title == "" || !ok {
			continue
		}
		out = append(out, merchant.Product{
			MerchantProductID: info.ProductID,
			Merchant:          Name,
			Brand:             sanitize.Text(info.ProductBrand, 80),
			Name:              sanitize.Text(info.Title, 200),
			Category:          sanitize.Text(info.CategoryPath, 120),
			PriceMinorUnits:   minor,
			Currency:          "INR",
			Available:         info.InStock,
			URL:               info.ProductURL,
			Confidence:        rankConfidence(len(out)),
		})
	}
	return out, nil
}

// toPaise accepts only INR prices: Flipkart's affiliate catalog is
// Flipkart India, and a price in any other currency would mean a response
// this connector doesn't understand.
func toPaise(p price) (int64, bool) {
	if p.Currency != "INR" || math.IsNaN(p.Amount) || math.IsInf(p.Amount, 0) || p.Amount <= 0 || p.Amount > 100_000_000 {
		return 0, false
	}
	return int64(math.Round(p.Amount * 100)), true
}

// rankConfidence reflects Flipkart's own result order, not an independent
// check that the listing is what the user asked for.
func rankConfidence(rank int) float64 {
	return math.Max(0.8-0.05*float64(rank), 0.3)
}

func (c *Connector) Authenticate(context.Context, merchant.AuthRequest) (*merchant.AuthResult, error) {
	return &merchant.AuthResult{Authenticated: c.configured()}, nil
}

func (c *Connector) GetProduct(context.Context, string) (*merchant.Product, error) {
	return nil, fmt.Errorf("%w: Flipkart product-by-ID lookup is not wired (its parameter contract isn't in the public reference)", shared.ErrNotImplemented)
}

func noCart() error {
	return fmt.Errorf("%w: Flipkart publishes no third-party cart or order API", shared.ErrNotImplemented)
}

func (c *Connector) GetOffers(context.Context, string) ([]quote.Offer, error) { return nil, noCart() }
func (c *Connector) CreateCart(context.Context, string) (*merchant.Cart, error) {
	return nil, noCart()
}
func (c *Connector) AddToCart(context.Context, string, string, int) (*merchant.Cart, error) {
	return nil, noCart()
}
func (c *Connector) RemoveFromCart(context.Context, string, string) (*merchant.Cart, error) {
	return nil, noCart()
}
func (c *Connector) GetDeliveryOptions(context.Context, string, string) ([]merchant.DeliveryOption, error) {
	return nil, noCart()
}
func (c *Connector) ApplyCoupon(context.Context, string, string) (*merchant.Cart, error) {
	return nil, noCart()
}
func (c *Connector) GetCheckoutQuote(context.Context, string) (*quote.CheckoutQuote, error) {
	return nil, noCart()
}

func (c *Connector) ExecuteCheckout(context.Context, string, string, merchant.Fulfillment) (*merchant.ExecutionResult, error) {
	return &merchant.ExecutionResult{
		Status: merchant.ExecutionUserInterventionNeeded,
		Reason: "Flipkart publishes no third-party checkout API; the user must buy on Flipkart through the product link",
	}, nil
}

func (c *Connector) GetOrder(context.Context, string) (*order.Order, error) { return nil, noCart() }
func (c *Connector) CancelOrder(context.Context, string) error              { return noCart() }

var (
	_ merchant.Connector      = (*Connector)(nil)
	_ merchant.StatusReporter = (*Connector)(nil)
	_ merchant.HandoffLinker  = (*Connector)(nil)
)
