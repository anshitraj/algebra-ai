// Package amazon searches Amazon's catalog through the official Amazon
// Creators API — the Associates catalog API that replaced Product
// Advertising API 5.0 (retired May 2026) — with credentials an operator
// creates in Associates Central.
//
// Contract used (Creators API reference and PA-API migration guide):
//
//	POST https://creatorsapi.amazon/catalog/v1/searchItems
//	Authorization: Bearer <token>              credential version 3.x (Login with Amazon)
//	Authorization: Bearer <token>, Version 2.x credential version 2.x (Cognito)
//	x-marketplace: www.amazon.in
//	{"keywords", "partnerTag", "marketplace", "itemCount" (≤10), "resources": [...]}
//
// Tokens are OAuth2 client_credentials from the token endpoint for the
// credential's version. India belongs to the Europe/Middle East/India
// group: version 3.2 (https://api.amazon.co.uk/auth/o2/token) or 2.2
// (Cognito, eu-south-2).
//
// Capabilities: search only. Amazon offers Associates no cart or order API,
// so checkout is always the user's, on Amazon, via the detail-page link.
// Deals (deals.go) come from the same searchItems call: the listing's
// savings against Amazon's reference price and any live deal details.
// Associates policy also limits how long prices may be shown without
// refreshing; search results here are fetched live and never cached.
package amazon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/project-algebra/algebra/connectors/sanitize"
	"github.com/project-algebra/algebra/internal/domain/merchant"
	"github.com/project-algebra/algebra/internal/domain/order"
	"github.com/project-algebra/algebra/internal/domain/quote"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

const (
	Name               = "amazon"
	DefaultAPIBase     = "https://creatorsapi.amazon/catalog/v1"
	DefaultMarketplace = "www.amazon.in"
	DocsURL            = "https://affiliate-program.amazon.com/creatorsapi/docs/en-us/api-reference"

	maxItems     = 10
	maxBodyBytes = 4 << 20
)

type tokenEndpoint struct {
	url, scope string
}

// tokenEndpoints maps a Creators API credential version to its token
// endpoint and scope.
var tokenEndpoints = map[string]tokenEndpoint{
	"2.1": {"https://creatorsapi.auth.us-east-1.amazoncognito.com/oauth2/token", "creatorsapi/default"},
	"2.2": {"https://creatorsapi.auth.eu-south-2.amazoncognito.com/oauth2/token", "creatorsapi/default"},
	"2.3": {"https://creatorsapi.auth.us-west-2.amazoncognito.com/oauth2/token", "creatorsapi/default"},
	"3.1": {"https://api.amazon.com/auth/o2/token", "creatorsapi::default"},
	"3.2": {"https://api.amazon.co.uk/auth/o2/token", "creatorsapi::default"},
	"3.3": {"https://api.amazon.co.jp/auth/o2/token", "creatorsapi::default"},
}

// catalogResources are the response groups requested from both searchItems
// and getItems — the same resource vocabulary applies to each per the
// Creators API reference.
var catalogResources = []string{
	"itemInfo.title",
	"itemInfo.byLineInfo",
	"offersV2.listings.price",
	"offersV2.listings.availability",
}

// asinRE matches an Amazon Standard Identification Number: 10 alphanumeric
// characters (most product ASINs start with a letter; ISBN-10-derived ASINs
// are all digits).
var asinRE = regexp.MustCompile(`^[A-Z0-9]{10}$`)

const maxCartItems = 10

type Config struct {
	CredentialID      string
	CredentialSecret  string
	CredentialVersion string // e.g. "3.2" or "2.2" for www.amazon.in
	PartnerTag        string // Associates tracking ID, e.g. "yourtag-21"
	Marketplace       string // defaults to www.amazon.in
	// APIBase, TokenURL and Scope override the published defaults; APIBase
	// and TokenURL must be https (loopback http is accepted for tests).
	APIBase    string
	TokenURL   string
	Scope      string
	HTTPClient *http.Client
}

type Connector struct {
	cfg       Config
	http      *http.Client
	tokens    oauth2.TokenSource
	configErr string
	now       func() time.Time
}

func New(cfg Config) *Connector {
	if cfg.Marketplace == "" {
		cfg.Marketplace = DefaultMarketplace
	}
	if cfg.APIBase == "" {
		cfg.APIBase = DefaultAPIBase
	}
	cfg.APIBase = strings.TrimSuffix(cfg.APIBase, "/")
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	client := *hc
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c := &Connector{cfg: cfg, http: &client, now: time.Now}

	if cfg.CredentialID == "" || cfg.CredentialSecret == "" || cfg.PartnerTag == "" {
		c.configErr = "Set AMAZON_CREATORS_CREDENTIAL_ID, AMAZON_CREATORS_CREDENTIAL_SECRET, AMAZON_CREATORS_CREDENTIAL_VERSION and AMAZON_ASSOCIATE_TAG (an Amazon Associates account with Creators API access) to enable catalog search and deals. Meanwhile agents get a link to Amazon's own search page."
		return c
	}
	endpoint, known := tokenEndpoints[cfg.CredentialVersion]
	tokenURL, scope := cfg.TokenURL, cfg.Scope
	if tokenURL == "" {
		if !known {
			c.configErr = fmt.Sprintf("Unknown AMAZON_CREATORS_CREDENTIAL_VERSION %q: expected 2.1-2.3 or 3.1-3.3 (www.amazon.in uses 3.2 or 2.2).", cfg.CredentialVersion)
			return c
		}
		tokenURL = endpoint.url
	}
	if scope == "" && known {
		scope = endpoint.scope
	}
	if !safeURL(cfg.APIBase) || !safeURL(tokenURL) {
		c.configErr = "Amazon Creators API and token URLs must be https."
		return c
	}
	cc := &clientcredentials.Config{
		ClientID:     cfg.CredentialID,
		ClientSecret: cfg.CredentialSecret,
		TokenURL:     tokenURL,
		// Cognito (2.x) expects HTTP Basic client auth; autodetect also
		// covers endpoints that want credentials in the form body.
		AuthStyle: oauth2.AuthStyleAutoDetect,
	}
	if scope != "" {
		cc.Scopes = []string{scope}
	}
	c.tokens = cc.TokenSource(context.WithValue(context.Background(), oauth2.HTTPClient, c.http))
	return c
}

func safeURL(raw string) bool {
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
		return merchant.Status{Integration: merchant.IntegrationOfficialAPI, Ready: false, Detail: c.configErr, Source: DocsURL}
	}
	return merchant.Status{
		Integration: merchant.IntegrationOfficialAPI,
		Ready:       true,
		Detail:      "Catalog search via the official Amazon Creators API (" + c.cfg.Marketplace + "). Amazon has no Associates cart or order API, so buying happens on Amazon through the product link.",
		Source:      DocsURL,
	}
}

// HandoffURL returns the marketplace's public search page for query — a
// link for a human to open; Algebra never fetches it.
func (c *Connector) HandoffURL(query string) string {
	base := "https://" + c.cfg.Marketplace + "/"
	q := strings.TrimSpace(query)
	if q == "" {
		return base
	}
	return base + "s?k=" + url.QueryEscape(q)
}

type money struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

// catalogItem is the item shape both searchItems and getItems return.
type catalogItem struct {
	ASIN          string `json:"asin"`
	DetailPageURL string `json:"detailPageURL"`
	ItemInfo      struct {
		Title *struct {
			DisplayValue string `json:"displayValue"`
		} `json:"title"`
		ByLineInfo *struct {
			Brand *struct {
				DisplayValue string `json:"displayValue"`
			} `json:"brand"`
		} `json:"byLineInfo"`
	} `json:"itemInfo"`
	OffersV2 *struct {
		Listings []struct {
			Price *struct {
				Money *money `json:"money"`
				// Savings/SavingBasis are only present when the listing is
				// discounted against Amazon's reference price (deals.go).
				Savings *struct {
					Money      *money  `json:"money"`
					Percentage flexInt `json:"percentage"`
				} `json:"savings"`
				SavingBasis *struct {
					Money                *money `json:"money"`
					SavingBasisType      string `json:"savingBasisType"`
					SavingBasisTypeLabel string `json:"savingBasisTypeLabel"`
				} `json:"savingBasis"`
			} `json:"price"`
			Availability *struct {
				Type string `json:"type"`
			} `json:"availability"`
			DealDetails *dealDetails `json:"dealDetails"`
		} `json:"listings"`
	} `json:"offersV2"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type searchResponse struct {
	SearchResult *struct {
		Items []catalogItem `json:"items"`
	} `json:"searchResult"`
	Errors []apiError `json:"errors"`
}

// itemsResponse is getItems' response envelope. Its item shape and error
// container follow the same lowerCamelCase convention as searchItems
// (verified live); the "itemsResult" envelope name itself mirrors PA-API
// 5's GetItems (ItemsResult/Items) translated to that convention — not run
// against a live getItems call in this environment.
type itemsResponse struct {
	ItemsResult *struct {
		Items []catalogItem `json:"items"`
	} `json:"itemsResult"`
	Errors []apiError `json:"errors"`
}

func (c *Connector) SearchProducts(ctx context.Context, query string, limit int) ([]merchant.Product, error) {
	if limit <= 0 || limit > maxItems {
		limit = maxItems
	}
	items, err := c.searchItems(ctx, query, limit, catalogResources)
	if err != nil {
		return nil, err
	}
	out := make([]merchant.Product, 0, len(items))
	for _, it := range items {
		if len(out) >= limit {
			break
		}
		p, ok := mapCatalogItem(it, math.Max(0.8-0.05*float64(len(out)), 0.3))
		if !ok {
			continue // an item with no priced offer can't be compared
		}
		out = append(out, p)
	}
	return out, nil
}

// searchItems runs one Creators API searchItems call and returns its items
// in Amazon's own ranking. No results is an empty slice, not an error.
func (c *Connector) searchItems(ctx context.Context, query string, limit int, resources []string) ([]catalogItem, error) {
	if !c.configured() {
		return nil, fmt.Errorf("%w: %s", shared.ErrNotImplemented, c.configErr)
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, errors.New("amazon: empty search query")
	}
	auth, err := c.authorization()
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]any{
		"keywords":    q,
		"partnerTag":  c.cfg.PartnerTag,
		"marketplace": c.cfg.Marketplace,
		"itemCount":   limit,
		"resources":   resources,
	})
	if err != nil {
		return nil, fmt.Errorf("amazon: encoding search request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.APIBase+"/searchItems", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("amazon: building search request: %w", err)
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("x-marketplace", c.cfg.Marketplace)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("amazon: search request failed: %w", err)
	}
	defer resp.Body.Close()
	var body searchResponse
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(&body)

	for _, e := range body.Errors {
		if e.Code == "NoResults" {
			return []catalogItem{}, nil
		}
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("amazon: Creators API rejected the credentials (HTTP %d)%s", resp.StatusCode, firstError(body.Errors))
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, errors.New("amazon: Creators API rate limit reached")
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("amazon: Creators API returned HTTP %d%s", resp.StatusCode, firstError(body.Errors))
	case decodeErr != nil:
		return nil, fmt.Errorf("amazon: decoding search response: %w", decodeErr)
	case body.SearchResult == nil:
		return []catalogItem{}, nil
	}
	return body.SearchResult.Items, nil
}

// mapCatalogItem converts one Creators API item into a merchant.Product.
// confidence is caller-assigned: a ranked search result's position, or 1.0
// for a direct ASIN lookup that isn't a guess. Reports false for an item
// with no priced offer — it can't be compared or bought.
func mapCatalogItem(it catalogItem, confidence float64) (merchant.Product, bool) {
	if it.ASIN == "" || it.ItemInfo.Title == nil || it.ItemInfo.Title.DisplayValue == "" || it.OffersV2 == nil {
		return merchant.Product{}, false
	}
	var minor int64
	var currency, availability string
	priced := false
	for _, l := range it.OffersV2.Listings {
		if l.Price == nil || l.Price.Money == nil {
			continue
		}
		if v, ok := toMinorUnits(*l.Price.Money); ok {
			minor, currency, priced = v, l.Price.Money.Currency, true
			if l.Availability != nil {
				availability = l.Availability.Type
			}
			break
		}
	}
	if !priced {
		return merchant.Product{}, false
	}
	brand := ""
	if b := it.ItemInfo.ByLineInfo; b != nil && b.Brand != nil {
		brand = b.Brand.DisplayValue
	}
	return merchant.Product{
		MerchantProductID: it.ASIN,
		Merchant:          Name,
		Brand:             sanitize.Text(brand, 80),
		Name:              sanitize.Text(it.ItemInfo.Title.DisplayValue, 200),
		PriceMinorUnits:   minor,
		Currency:          currency,
		Available:         strings.HasPrefix(strings.ToUpper(availability), "IN_STOCK"),
		URL:               it.DetailPageURL,
		Confidence:        confidence,
	}, true
}

func (c *Connector) authorization() (string, error) {
	tok, err := c.tokens.Token()
	if err != nil {
		return "", fmt.Errorf("amazon: could not obtain a Creators API token (check credential ID, secret and version): %w", err)
	}
	header := "Bearer " + tok.AccessToken
	if strings.HasPrefix(c.cfg.CredentialVersion, "2.") {
		header += ", Version " + c.cfg.CredentialVersion
	}
	return header, nil
}

func firstError(errs []apiError) string {
	if len(errs) == 0 {
		return ""
	}
	return ": " + sanitize.Text(errs[0].Code+" "+errs[0].Message, 200)
}

// toMinorUnits converts a Creators API money amount (major units, e.g.
// 59.49) to minor units. Only ISO currencies with two decimal places — or
// JPY, with none — are accepted.
func toMinorUnits(m money) (int64, bool) {
	if len(m.Currency) != 3 || math.IsNaN(m.Amount) || math.IsInf(m.Amount, 0) || m.Amount <= 0 || m.Amount > 100_000_000 {
		return 0, false
	}
	factor := 100.0
	if m.Currency == "JPY" {
		factor = 1
	}
	return int64(math.Round(m.Amount * factor)), true
}

func (c *Connector) Authenticate(context.Context, merchant.AuthRequest) (*merchant.AuthResult, error) {
	return &merchant.AuthResult{Authenticated: c.configured()}, nil
}

// GetProduct looks up one ASIN via Creators API getItems — the same base
// URL, auth and resource vocabulary as searchItems (Config.APIBase +
// "/getItems"). NOT run against a live credential in this environment; the
// request/response shape is extrapolated from searchItems' verified
// lowerCamelCase convention and PA-API 5's GetItems (its documented
// predecessor), not from a confirmed Creators API getItems example.
func (c *Connector) GetProduct(ctx context.Context, merchantProductID string) (*merchant.Product, error) {
	if !c.configured() {
		return nil, fmt.Errorf("%w: %s", shared.ErrNotImplemented, c.configErr)
	}
	asin := strings.ToUpper(strings.TrimSpace(merchantProductID))
	if !asinRE.MatchString(asin) {
		return nil, fmt.Errorf("amazon: %q is not a valid ASIN", merchantProductID)
	}
	auth, err := c.authorization()
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]any{
		"itemIds":     []string{asin},
		"itemIdType":  "ASIN",
		"partnerTag":  c.cfg.PartnerTag,
		"marketplace": c.cfg.Marketplace,
		"resources":   catalogResources,
	})
	if err != nil {
		return nil, fmt.Errorf("amazon: encoding getItems request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.APIBase+"/getItems", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("amazon: building getItems request: %w", err)
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("x-marketplace", c.cfg.Marketplace)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("amazon: getItems request failed: %w", err)
	}
	defer resp.Body.Close()
	var body itemsResponse
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(&body)

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("amazon: Creators API rejected the credentials (HTTP %d)%s", resp.StatusCode, firstError(body.Errors))
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, errors.New("amazon: Creators API rate limit reached")
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("amazon: Creators API returned HTTP %d%s", resp.StatusCode, firstError(body.Errors))
	case decodeErr != nil:
		return nil, fmt.Errorf("amazon: decoding getItems response: %w", decodeErr)
	}
	if body.ItemsResult == nil || len(body.ItemsResult.Items) == 0 {
		return nil, fmt.Errorf("%w: amazon %s%s", shared.ErrNotFound, asin, firstError(body.Errors))
	}
	p, ok := mapCatalogItem(body.ItemsResult.Items[0], 1.0)
	if !ok {
		return nil, fmt.Errorf("%w: amazon %s has no priced offer", shared.ErrNotFound, asin)
	}
	return &p, nil
}

// CartLine is one line of an Amazon Associates "Add to Cart" link.
type CartLine struct {
	ASIN     string
	Quantity int
}

// CartURL builds Amazon's Associates "Add to Cart" link
// (https://www.amazon.in/gp/aws/cart/add.html?AssociateTag=…&ASIN.1=…&Quantity.1=…),
// officially documented alongside PA-API 5 as a plain HTML form action, not
// a PA-API REST call — so it is not itself a PA-API operation and shouldn't
// be affected by the PA-API 5 catalog-API retirement. The user opens the
// link, reviews the prefilled Amazon cart, and pays on Amazon; Algebra never
// sees payment details or places the order — same non-custodial shape as
// HandoffURL, just prefilled. NOT confirmed against a live Amazon page in
// this environment: verify it still loads a populated cart on the target
// marketplace before relying on it (its documentation page now redirects
// to the PA-API 5 deprecation notice, so current behavior is unconfirmed).
func (c *Connector) CartURL(items []CartLine) (string, error) {
	if !c.configured() {
		return "", fmt.Errorf("%w: %s", shared.ErrNotImplemented, c.configErr)
	}
	if len(items) == 0 {
		return "", errors.New("amazon: CartURL needs at least one item")
	}
	if len(items) > maxCartItems {
		return "", fmt.Errorf("amazon: CartURL supports at most %d items, got %d", maxCartItems, len(items))
	}
	q := url.Values{"AssociateTag": {c.cfg.PartnerTag}}
	for i, it := range items {
		asin := strings.ToUpper(strings.TrimSpace(it.ASIN))
		if !asinRE.MatchString(asin) {
			return "", fmt.Errorf("amazon: %q is not a valid ASIN", it.ASIN)
		}
		if it.Quantity <= 0 {
			return "", fmt.Errorf("amazon: quantity for %s must be positive, got %d", asin, it.Quantity)
		}
		idx := strconv.Itoa(i + 1)
		q.Set("ASIN."+idx, asin)
		q.Set("Quantity."+idx, strconv.Itoa(it.Quantity))
	}
	return "https://" + c.cfg.Marketplace + "/gp/aws/cart/add.html?" + q.Encode(), nil
}

func noCart() error {
	return fmt.Errorf("%w: Amazon offers Associates no cart or order API", shared.ErrNotImplemented)
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
		Reason: "Amazon offers no third-party checkout API; the user must buy on Amazon through the product link",
	}, nil
}

func (c *Connector) GetOrder(context.Context, string) (*order.Order, error) { return nil, noCart() }
func (c *Connector) CancelOrder(context.Context, string) error              { return noCart() }

var (
	_ merchant.Connector      = (*Connector)(nil)
	_ merchant.StatusReporter = (*Connector)(nil)
	_ merchant.HandoffLinker  = (*Connector)(nil)
)
