// Package paysh reads the Pay.sh catalog and turns it into things Algebra can
// use: a browsable listing of paid APIs, and routing candidates the executor
// can price, pay and call.
//
// Pay.sh (https://pay.sh) is the Solana Foundation's and Google Cloud's
// gateway and registry of pay-per-call APIs, settled in USDC on Solana. Its
// catalog is a public JSON document, and every provider has a public page
// with a table of endpoints and prices. Both are third-party data, so:
//
//   - they are fetched through the SSRF-safe HTTP client, with a body limit;
//   - every field is validated and bounded, and text is stripped of control
//     characters before anything shows it;
//   - an entry that doesn't parse is dropped on its own and never fails the
//     rest of the catalog;
//   - a catalog entry is a listing, not a recommendation. Candidates built
//     from it carry the source "paysh" (trust "listed"); Algebra still prices
//     the endpoint with an unpaid request and checks the terms before any
//     money moves, exactly as it would for an endpoint found anywhere else;
//   - the catalog's own words are never treated as instructions.
//
// Prices in the catalog are decimal dollars. They become integer micro-USDC
// here (USDC is pegged one to one and has six decimals), rounded up: a listed
// price is never understated.
package paysh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/platform/safehttp"
	"github.com/project-algebra/algebra/providers/catalog"
)

const (
	// Source names this registry in listings.
	Source = "pay.sh"
	// ProviderPrefix starts every provider ID this package makes.
	ProviderPrefix = "paysh:"
	// Currency is what every price here is expressed in.
	Currency = catalog.Currency

	DefaultCatalogURL = "https://pay.sh/api/catalog"
	DefaultDocsURL    = "https://pay.sh/api"
)

// Bounds on what one fetch may hand us. They are far above what Pay.sh
// publishes today (75 providers, up to 137 endpoints each) and exist so a
// hostile or broken response can't make us hold or process an unbounded amount.
const (
	maxProviders   = 500
	maxEndpoints   = 600
	maxTitle       = catalog.MaxTitle
	maxDescription = catalog.MaxDescription
	maxPathLen     = catalog.MaxPathLen
	retryAfter     = 30 * time.Second
)

// The catalog's shapes, shared with the other catalogs.
type (
	Provider = catalog.Provider
	Category = catalog.Category
	Listing  = catalog.Listing
	Endpoint = catalog.Endpoint
	Detail   = catalog.Detail
	Filter   = catalog.Filter
)

var (
	// ErrNotFound: no provider by that name is in the catalog.
	ErrNotFound = catalog.ErrNotFound
	// ErrUnavailable: the catalog couldn't be read and nothing usable is cached.
	ErrUnavailable = catalog.ErrUnavailable

	fqnRE      = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}(/[a-z0-9][a-z0-9._-]{0,63}){0,3}$`)
	categoryRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
	pathRE     = regexp.MustCompile(`^[A-Za-z0-9._~!$&'()*+,;=:@%/{}-]+$`)
)

// HTTPDoer is the HTTP client used. *safehttp.Client satisfies it.
type HTTPDoer interface {
	Do(req *http.Request) (*safehttp.Response, error)
}

// Config configures a Client.
type Config struct {
	// CatalogURL and DocsURL default to Pay.sh's own. A provider's page is
	// {DocsURL}/{fqn}/index.md.
	CatalogURL string
	DocsURL    string
	HTTP       HTTPDoer
	// CatalogTTL and DetailTTL say how long a fetched copy is served without
	// asking Pay.sh again. Pay.sh itself caches the catalog for a minute.
	CatalogTTL time.Duration
	DetailTTL  time.Duration
	// StaleFor is how long past its TTL a copy may still be served when
	// refreshing it fails, so an outage at Pay.sh doesn't empty the page.
	StaleFor time.Duration
	// Now overrides the clock (tests).
	Now func() time.Time
}

// Client reads the catalog, caches it, and shares one fetch among callers
// that arrive at once. It is safe for concurrent use.
type Client struct {
	cfg     Config
	catalog *catalog.Cached[*snapshot]

	mu      sync.Mutex
	details map[string]*catalog.Cached[[]Endpoint]
}

type snapshot struct {
	providers []Provider
	byID      map[string]int
	byFQN     map[string]int
	generated time.Time
}

var _ catalog.Source = (*Client)(nil)

// New builds a Client. Without HTTP it can't fetch anything, so callers
// always pass one (production: a safehttp.Client).
func New(cfg Config) *Client {
	if cfg.CatalogURL == "" {
		cfg.CatalogURL = DefaultCatalogURL
	}
	if cfg.DocsURL == "" {
		cfg.DocsURL = DefaultDocsURL
	}
	cfg.DocsURL = strings.TrimRight(cfg.DocsURL, "/")
	if cfg.CatalogTTL <= 0 {
		cfg.CatalogTTL = 10 * time.Minute
	}
	if cfg.DetailTTL <= 0 {
		cfg.DetailTTL = 30 * time.Minute
	}
	if cfg.StaleFor <= 0 {
		cfg.StaleFor = 6 * time.Hour
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Client{
		cfg:     cfg,
		catalog: &catalog.Cached[*snapshot]{TTL: cfg.CatalogTTL, StaleFor: cfg.StaleFor, RetryAfter: retryAfter, Now: cfg.Now},
		details: map[string]*catalog.Cached[[]Endpoint]{},
	}
}

// Name is how listings call this catalog.
func (c *Client) Name() string { return Source }

// Prefix starts the name of every provider this catalog has.
func (c *Client) Prefix() string { return ProviderPrefix }

// ProviderID is the ID this package gives a provider by its FQN.
func ProviderID(fqn string) string {
	return ProviderPrefix + strings.ReplaceAll(fqn, "/", ".")
}

// List returns the catalog, filtered. It reads Pay.sh at most once per
// CatalogTTL, and serves an older copy, marked stale, when Pay.sh is down.
// A Limit of 0 returns every match.
func (c *Client) List(ctx context.Context, f Filter) (*Listing, error) {
	snap, fetched, stale, err := c.catalogNow(ctx)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	var matched []Provider
	for _, p := range snap.providers {
		// Categories count what can be paid on the network asked for, if any.
		if n := chain.NormalizeNetwork(f.Network); n != "" && !slices.Contains(p.Networks, n) {
			continue
		}
		counts[p.Category]++
		if f.Matches(p) {
			matched = append(matched, p)
		}
	}

	limit := f.Limit
	if limit <= 0 || limit > maxProviders {
		limit = maxProviders
	}
	off := max(f.Offset, 0)
	page := []Provider{}
	if off < len(matched) {
		page = slices.Clone(matched[off:min(off+limit, len(matched))])
	}

	out := &Listing{
		Source: Source, GeneratedAt: snap.generated, FetchedAt: fetched, Stale: stale,
		Total: len(matched), Count: len(page), Providers: page, Categories: []Category{},
	}
	for name, n := range counts {
		out.Categories = append(out.Categories, Category{Name: name, Count: n})
	}
	slices.SortFunc(out.Categories, func(a, b Category) int {
		if a.Count != b.Count {
			return b.Count - a.Count
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out, nil
}

// Detail returns one provider and its endpoints. id is the provider ID
// ("paysh:birdeye.data") or its FQN ("birdeye/data").
func (c *Client) Detail(ctx context.Context, id string) (*Detail, error) {
	snap, _, catStale, err := c.catalogNow(ctx)
	if err != nil {
		return nil, err
	}
	p, ok := snap.find(id)
	if !ok {
		return nil, ErrNotFound
	}
	eps, fetched, stale, err := c.detailCache(p.FQN).Get(ctx, func(ctx context.Context) ([]Endpoint, error) { return c.fetchDetail(ctx, p) })
	if err != nil {
		return nil, err
	}
	return &Detail{Provider: p, Endpoints: slices.Clone(eps), FetchedAt: fetched, Stale: stale || catStale}, nil
}

func (s *snapshot) find(id string) (Provider, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	if i, ok := s.byID[id]; ok {
		return s.providers[i], true
	}
	if i, ok := s.byFQN[id]; ok {
		return s.providers[i], true
	}
	return Provider{}, false
}

// --- fetching ---

func (c *Client) catalogNow(ctx context.Context) (*snapshot, time.Time, bool, error) {
	return c.catalog.Get(ctx, c.fetchCatalog)
}

func (c *Client) detailCache(fqn string) *catalog.Cached[[]Endpoint] {
	c.mu.Lock()
	defer c.mu.Unlock()
	d, ok := c.details[fqn]
	if !ok {
		d = &catalog.Cached[[]Endpoint]{TTL: c.cfg.DetailTTL, StaleFor: c.cfg.StaleFor, RetryAfter: retryAfter, Now: c.cfg.Now}
		c.details[fqn] = d
	}
	return d
}

func (c *Client) get(ctx context.Context, rawURL, accept string) ([]byte, error) {
	if c.cfg.HTTP == nil {
		return nil, errors.New("paysh: no HTTP client configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "algebra-paysh/1")
	resp, err := c.cfg.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.Status != http.StatusOK {
		return nil, fmt.Errorf("pay.sh answered HTTP %d", resp.Status)
	}
	return resp.Body, nil
}

func (c *Client) fetchCatalog(ctx context.Context) (*snapshot, error) {
	body, err := c.get(ctx, c.cfg.CatalogURL, "application/json")
	if err != nil {
		return nil, err
	}
	return parseCatalog(body, c.cfg.DocsURL)
}

func (c *Client) fetchDetail(ctx context.Context, p Provider) ([]Endpoint, error) {
	segs := strings.Split(p.FQN, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	body, err := c.get(ctx, c.cfg.DocsURL+"/"+strings.Join(segs, "/")+"/index.md", "text/markdown, text/plain;q=0.9")
	if err != nil {
		return nil, err
	}
	return buildEndpoints(p, parseEndpointTable(string(body))), nil
}

// --- parsing the catalog ---

type rawProvider struct {
	FQN           string `json:"fqn"`
	Title         string `json:"title"`
	Description   string `json:"description"`
	UseCase       string `json:"use_case"`
	Category      string `json:"category"`
	ServiceURL    string `json:"service_url"`
	EndpointCount int    `json:"endpoint_count"`
	HasMetering   bool   `json:"has_metering"`
	HasFreeTier   bool   `json:"has_free_tier"`
	// The prices are read as written, number or string, and parsed by
	// microUSDC, which refuses anything that isn't a plain decimal.
	MinPriceUSD json.RawMessage `json:"min_price_usd"`
	MaxPriceUSD json.RawMessage `json:"max_price_usd"`
}

// priceText is a price field as the text of a decimal, or "" for anything
// else: a JSON number is its own literal, a string is its contents.
func priceText(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(s, `"`) {
		return s
	}
	var q string
	if json.Unmarshal(raw, &q) != nil {
		return ""
	}
	return q
}

// parseCatalog reads Pay.sh's catalog document. Each entry is decoded and
// validated on its own, so one that is malformed is dropped without taking the
// rest with it. A catalog with entries but none usable is an error, because
// that means the format changed under us.
func parseCatalog(body []byte, docsURL string) (*snapshot, error) {
	var raw struct {
		GeneratedAt json.RawMessage   `json:"generated_at"`
		Providers   []json.RawMessage `json:"providers"`
	}
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("paysh: the catalog isn't valid JSON: %w", err)
	}
	snap := &snapshot{byID: map[string]int{}, byFQN: map[string]int{}}
	var generated string
	if json.Unmarshal(raw.GeneratedAt, &generated) == nil {
		if t, err := time.Parse(time.RFC3339, generated); err == nil {
			snap.generated = t.UTC()
		}
	}
	seen := map[string]bool{}
	for i, item := range raw.Providers {
		if i >= maxProviders {
			break
		}
		var rp rawProvider
		if err := json.Unmarshal(item, &rp); err != nil {
			continue
		}
		p, ok := toProvider(rp, docsURL)
		if !ok || seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		snap.providers = append(snap.providers, p)
	}
	if len(raw.Providers) > 0 && len(snap.providers) == 0 {
		return nil, errors.New("paysh: the catalog has providers but none of them is in a format this version understands")
	}
	slices.SortFunc(snap.providers, func(a, b Provider) int {
		if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
			return c
		}
		return strings.Compare(a.FQN, b.FQN)
	})
	for i, p := range snap.providers {
		snap.byID[p.ID], snap.byFQN[p.FQN] = i, i
	}
	return snap, nil
}

func toProvider(r rawProvider, docsURL string) (Provider, bool) {
	fqn := strings.ToLower(strings.TrimSpace(r.FQN))
	if !fqnRE.MatchString(fqn) {
		return Provider{}, false
	}
	u, err := url.Parse(strings.TrimSpace(r.ServiceURL))
	if err != nil || !catalog.PublicHTTPS(u) || u.RawQuery != "" || u.Fragment != "" {
		return Provider{}, false
	}
	category := strings.ToLower(strings.TrimSpace(r.Category))
	if !categoryRE.MatchString(category) {
		category = "other"
	}
	name := cleanText(r.Title, maxTitle)
	if name == "" {
		name = fqn
	}
	p := Provider{
		ID: ProviderID(fqn), FQN: fqn, Name: name,
		Description: cleanText(r.Description, maxDescription), UseCase: cleanText(r.UseCase, maxDescription),
		Category: category, ServiceURL: strings.TrimRight(u.String(), "/"), Host: strings.ToLower(u.Hostname()),
		EndpointCount: min(max(r.EndpointCount, 0), maxEndpoints),
		Metered:       r.HasMetering, FreeTier: r.HasFreeTier, Currency: Currency,
		PageURL: strings.TrimSuffix(docsURL, "/api") + "/api/" + fqn, Source: Source,
		// Pay.sh's gateways are paid on Solana mainnet (they answer with
		// mainnet terms only).
		Networks: []string{chain.Solana},
	}
	if lo, ok := microUSDC(priceText(r.MinPriceUSD)); ok {
		p.MinPriceMinor = lo
	}
	if hi, ok := microUSDC(priceText(r.MaxPriceUSD)); ok {
		p.MaxPriceMinor = hi
	}
	p.MaxPriceMinor = max(p.MaxPriceMinor, p.MinPriceMinor) // a range never ends below where it starts
	return p, true
}

func microUSDC(lit string) (int64, bool) { return catalog.MicroUSDC(lit) }
func cleanText(s string, max int) string { return catalog.CleanText(s, max) }
