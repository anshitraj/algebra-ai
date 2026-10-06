// Package circleagents reads Circle's Agent Marketplace (agents.circle.com):
// a public catalog of x402 services, served by Circle's discovery API
// (GET https://api.circle.com/v2/x402/discovery/resources).
//
// Unlike Pay.sh, Circle lists every endpoint with the payment terms its
// provider publishes (network, asset, amount, pay-to) and usually a JSON
// Schema for its input. Algebra reads only what it can pay: endpoints that
// accept Circle's USDC on Solana mainnet as plain ("vanilla") x402, and not
// the ones that need a browser sign-in (SIWX). It groups them by provider.
//
// The same rules as every catalog apply (providers/catalog): fetched through
// the SSRF-safe client with a body limit, every field validated and bounded,
// one malformed entry never sinks the rest, a listing is not an endorsement,
// and the terms Circle publishes are checked again against the provider's
// own 402 answer before anything is paid.
package circleagents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/platform/safehttp"
	"github.com/project-algebra/algebra/providers/catalog"
)

const (
	// Source names this catalog in listings.
	Source = "circle"
	// ProviderPrefix starts every provider ID this package makes.
	ProviderPrefix = "circle:"
	// CapabilityPrefix starts every capability this package makes, so a
	// Circle capability never collides with another catalog's.
	CapabilityPrefix = "circle."

	DefaultDiscoveryURL = "https://api.circle.com/v2/x402/discovery/resources"
	// MarketplaceURL is where a person browses the same catalog.
	MarketplaceURL = "https://agents.circle.com/services"

	// solanaMainnet is Solana mainnet's CAIP-2 ID, as Circle writes networks.
	solanaMainnet = "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp"

	pageSize      = 200
	maxItems      = 5000
	maxPerProv    = 600
	maxSchemaSize = 16 << 10
	retryAfter    = 30 * time.Second
)

// HTTPDoer is the HTTP client used. *safehttp.Client satisfies it.
type HTTPDoer interface {
	Do(req *http.Request) (*safehttp.Response, error)
}

// Config configures a Client.
type Config struct {
	// DiscoveryURL defaults to Circle's discovery API.
	DiscoveryURL string
	HTTP         HTTPDoer
	// TTL is how long a fetched copy is served; StaleFor how long past that
	// it is still served when Circle can't be reached.
	TTL      time.Duration
	StaleFor time.Duration
	Now      func() time.Time
}

// Client reads Circle's catalog. Safe for concurrent use.
type Client struct {
	cfg   Config
	cache *catalog.Cached[*snapshot]
}

type snapshot struct {
	providers []catalog.Provider
	endpoints map[string][]catalog.Endpoint // by provider ID
	byID      map[string]int
	bySlug    map[string]int
	generated time.Time
}

var _ catalog.Source = (*Client)(nil)

// New builds a Client.
func New(cfg Config) *Client {
	if cfg.DiscoveryURL == "" {
		cfg.DiscoveryURL = DefaultDiscoveryURL
	}
	if cfg.TTL <= 0 {
		cfg.TTL = 10 * time.Minute
	}
	if cfg.StaleFor <= 0 {
		cfg.StaleFor = 6 * time.Hour
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Client{cfg: cfg, cache: &catalog.Cached[*snapshot]{
		TTL: cfg.TTL, StaleFor: cfg.StaleFor, RetryAfter: retryAfter, Now: cfg.Now, RefreshTimeout: 60 * time.Second,
	}}
}

// Name is how listings call this catalog.
func (c *Client) Name() string { return Source }

// Prefix starts the name of every provider this catalog has.
func (c *Client) Prefix() string { return ProviderPrefix }

// List returns the catalog's providers, filtered. Limit 0 returns every match.
func (c *Client) List(ctx context.Context, f catalog.Filter) (*catalog.Listing, error) {
	snap, fetched, stale, err := c.cache.Get(ctx, c.fetch)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	var matched []catalog.Provider
	for _, p := range snap.providers {
		counts[p.Category]++
		if f.Matches(p) {
			matched = append(matched, p)
		}
	}
	off := max(f.Offset, 0)
	limit := f.Limit
	if limit <= 0 {
		limit = len(matched)
	}
	page := []catalog.Provider{}
	if off < len(matched) {
		page = slices.Clone(matched[off:min(off+limit, len(matched))])
	}
	out := &catalog.Listing{
		Source: Source, GeneratedAt: snap.generated, FetchedAt: fetched, Stale: stale,
		Total: len(matched), Count: len(page), Providers: page, Categories: []catalog.Category{},
	}
	for name, n := range counts {
		out.Categories = append(out.Categories, catalog.Category{Name: name, Count: n})
	}
	slices.SortFunc(out.Categories, func(a, b catalog.Category) int {
		if a.Count != b.Count {
			return b.Count - a.Count
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out, nil
}

// Detail returns one provider and its endpoints, by provider ID
// ("circle:birdeye") or slug ("birdeye").
func (c *Client) Detail(ctx context.Context, id string) (*catalog.Detail, error) {
	snap, fetched, stale, err := c.cache.Get(ctx, c.fetch)
	if err != nil {
		return nil, err
	}
	id = strings.ToLower(strings.TrimSpace(id))
	i, ok := snap.byID[id]
	if !ok {
		i, ok = snap.bySlug[id]
	}
	if !ok {
		return nil, catalog.ErrNotFound
	}
	p := snap.providers[i]
	return &catalog.Detail{Provider: p, Endpoints: slices.Clone(snap.endpoints[p.ID]), FetchedAt: fetched, Stale: stale}, nil
}

// CandidatesFor returns one provider's candidates for a capability.
func (c *Client) CandidatesFor(ctx context.Context, provider, capability string) ([]routing.Candidate, error) {
	d, err := c.Detail(ctx, provider)
	if err != nil {
		return nil, err
	}
	cands, _ := candidatesOf(d)
	return slices.DeleteFunc(cands, func(x routing.Candidate) bool { return x.Capability != capability }), nil
}

// ForCapability finds the candidates for a Circle capability
// ("circle.birdeye.get.x402-defi-price") without a provider named.
func (c *Client) ForCapability(ctx context.Context, capability string) ([]routing.Candidate, error) {
	capability = strings.ToLower(strings.TrimSpace(capability))
	if !strings.HasPrefix(capability, CapabilityPrefix) {
		return nil, catalog.ErrNotFound
	}
	snap, _, _, err := c.cache.Get(ctx, c.fetch)
	if err != nil {
		return nil, err
	}
	for _, p := range snap.providers {
		for _, e := range snap.endpoints[p.ID] {
			if e.Capability == capability {
				return c.CandidatesFor(ctx, p.ID, capability)
			}
		}
	}
	return nil, catalog.ErrNotFound
}

// candidatesOf builds candidates found on Circle's marketplace, carrying the
// payment terms Circle published for each endpoint.
func candidatesOf(d *catalog.Detail) ([]routing.Candidate, []routing.Dropped) {
	usdc, _ := chain.AssetAddress(chain.Solana, "USDC")
	return catalog.Candidates(d, routing.SourceCircle, func(c *routing.Candidate, _ catalog.Endpoint) {
		c.AssetAddress = usdc
	})
}

// --- fetching and parsing ---

func (c *Client) fetch(ctx context.Context) (*snapshot, error) {
	if c.cfg.HTTP == nil {
		return nil, errors.New("circleagents: no HTTP client configured")
	}
	var items []json.RawMessage
	for offset := 0; offset < maxItems; offset += pageSize {
		page, total, err := c.page(ctx, offset)
		if err != nil {
			return nil, err
		}
		items = append(items, page...)
		if len(page) == 0 || offset+pageSize >= total {
			break
		}
	}
	return parse(items, c.cfg.Now())
}

func (c *Client) page(ctx context.Context, offset int) ([]json.RawMessage, int, error) {
	q := url.Values{
		"network": {solanaMainnet}, "supportsVanillax402": {"true"}, "siwx": {"false"},
		"limit": {strconv.Itoa(pageSize)}, "offset": {strconv.Itoa(offset)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.DiscoveryURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "algebra-circleagents/1")
	resp, err := c.cfg.HTTP.Do(req)
	if err != nil {
		return nil, 0, err
	}
	if resp.Status != http.StatusOK {
		return nil, 0, fmt.Errorf("circle answered HTTP %d", resp.Status)
	}
	var doc struct {
		Items      []json.RawMessage `json:"items"`
		Pagination struct {
			Total int `json:"total"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(resp.Body, &doc); err != nil {
		return nil, 0, fmt.Errorf("circleagents: the catalog isn't valid JSON: %w", err)
	}
	return doc.Items, doc.Pagination.Total, nil
}

type rawItem struct {
	Resource    string      `json:"resource"`
	Type        string      `json:"type"`
	LastUpdated string      `json:"lastUpdated"`
	Accepts     []rawAccept `json:"accepts"`
	Metadata    rawMeta     `json:"metadata"`
}

type rawAccept struct {
	Scheme  string          `json:"scheme"`
	Network string          `json:"network"`
	Asset   string          `json:"asset"`
	PayTo   string          `json:"payTo"`
	Amount  json.RawMessage `json:"amount"`
}

type rawMeta struct {
	Provider struct {
		Name        string   `json:"name"`
		Website     string   `json:"website"`
		Description string   `json:"description"`
		Category    string   `json:"category"`
		Tags        []string `json:"tags"`
	} `json:"provider"`
	Method      string          `json:"method"`
	Description string          `json:"description"`
	Input       json.RawMessage `json:"input"`
	SIWX        bool            `json:"siwx"`
	Vanilla     *bool           `json:"supportsVanillax402"`
}

var methods = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}

// categories maps Circle's categories onto the words the other catalogs use,
// so a filter means the same thing across catalogs.
var categories = map[string]string{
	"FINANCIAL_ANALYSIS":  "finance",
	"PREDICTION_MARKETS":  "prediction_markets",
	"WEB_SEARCH_RESEARCH": "search",
	"DATA_ENRICHMENT":     "data",
	"CREATIVE":            "media",
	"SOCIAL_INTELLIGENCE": "social",
	"INFRASTRUCTURE":      "infrastructure",
}

type parsedEndpoint struct {
	provider string // slug
	meta     rawMeta
	ep       catalog.Endpoint
	host     string
	updated  time.Time
}

// parse turns discovery items into providers and their endpoints. Each item
// is decoded and checked on its own; one that isn't payable in USDC on Solana,
// or doesn't validate, is dropped without affecting the rest.
func parse(items []json.RawMessage, now time.Time) (*snapshot, error) {
	usdc, _ := chain.AssetAddress(chain.Solana, "USDC")
	var parsed []parsedEndpoint
	seen := map[string]bool{}
	for _, raw := range items {
		var it rawItem
		if json.Unmarshal(raw, &it) != nil || it.Type != "http" || it.Metadata.SIWX || (it.Metadata.Vanilla != nil && !*it.Metadata.Vanilla) {
			continue
		}
		u, err := url.Parse(strings.TrimSpace(it.Resource))
		if err != nil || !catalog.PublicHTTPS(u) || u.Fragment != "" || len(it.Resource) > 2048 {
			continue
		}
		method := strings.ToUpper(strings.TrimSpace(it.Metadata.Method))
		if !slices.Contains(methods, method) {
			continue
		}
		name := catalog.CleanText(it.Metadata.Provider.Name, catalog.MaxTitle)
		slug := catalog.Slug(name, 48)
		if slug == "" {
			continue
		}
		var price int64 = -1
		var payTo string
		for _, a := range it.Accepts {
			if a.Scheme != "exact" || chain.NormalizeNetwork(a.Network) != chain.Solana || !chain.SameAddress(chain.Solana, a.Asset, usdc) {
				continue
			}
			amt := strings.Trim(strings.TrimSpace(string(a.Amount)), `"`)
			n, err := strconv.ParseInt(amt, 10, 64)
			if err != nil || n < 0 || n > catalog.MaxPriceMinor {
				continue
			}
			// Several options in USDC on Solana: list the cheapest, as the
			// runner will pick it at quote time.
			if price < 0 || n < price {
				price, payTo = n, catalog.CleanText(a.PayTo, 64)
			}
		}
		if price < 0 {
			continue // not payable in Circle's USDC on Solana mainnet
		}
		path := strings.TrimPrefix(u.EscapedPath(), "/")
		if u.RawQuery != "" {
			path += "?" + u.RawQuery
		}
		if len(path) > catalog.MaxPathLen {
			continue
		}
		key := method + " " + it.Resource
		if seen[key] {
			continue
		}
		seen[key] = true

		ep := catalog.Endpoint{
			Capability: catalog.CapabilityID(CapabilityPrefix+slug, method, path),
			Method:     method, Path: path, URL: u.String(),
			Pricing: chain.FormatUnits(price, chain.USDCDecimals) + " USDC", PriceMinor: price, Free: price == 0,
			Network: chain.Solana, PayTo: payTo,
			Description: catalog.CleanText(it.Metadata.Description, catalog.MaxDescription), Callable: true,
		}
		if strings.ContainsAny(u.Path, "{}") {
			ep.Callable, ep.Reason = false, "the path has parameters, which Algebra can't fill in yet"
		}
		if len(it.Metadata.Input) > 0 && len(it.Metadata.Input) <= maxSchemaSize && json.Valid(it.Metadata.Input) && string(it.Metadata.Input) != "null" {
			ep.InputSchema = slices.Clone(it.Metadata.Input)
		}
		updated, _ := time.Parse(time.RFC3339, it.LastUpdated)
		parsed = append(parsed, parsedEndpoint{provider: slug, meta: it.Metadata, ep: ep, host: strings.ToLower(u.Hostname()), updated: updated})
	}
	if len(items) > 0 && len(parsed) == 0 {
		return nil, errors.New("circleagents: the catalog has entries but none payable in USDC on Solana that this version understands")
	}
	return group(parsed, now), nil
}

// group gathers endpoints by provider.
func group(parsed []parsedEndpoint, now time.Time) *snapshot {
	snap := &snapshot{endpoints: map[string][]catalog.Endpoint{}, byID: map[string]int{}, bySlug: map[string]int{}}
	hosts := map[string]map[string]int{}
	order := []string{}
	byID := map[string]*catalog.Provider{}
	for _, pe := range parsed {
		id := ProviderPrefix + pe.provider
		p, ok := byID[id]
		if !ok {
			cat := categories[strings.ToUpper(strings.TrimSpace(pe.meta.Provider.Category))]
			if cat == "" {
				cat = "other"
			}
			p = &catalog.Provider{
				ID: id, FQN: pe.provider, Name: catalog.CleanText(pe.meta.Provider.Name, catalog.MaxTitle),
				Description: catalog.CleanText(pe.meta.Provider.Description, catalog.MaxDescription),
				Category:    cat, Metered: true, Currency: catalog.Currency, PageURL: MarketplaceURL, Source: Source,
				MinPriceMinor: pe.ep.PriceMinor, MaxPriceMinor: pe.ep.PriceMinor,
			}
			if w, err := url.Parse(strings.TrimSpace(pe.meta.Provider.Website)); err == nil && catalog.PublicHTTPS(w) {
				p.ServiceURL = strings.TrimRight(w.String(), "/")
			}
			byID[id] = p
			hosts[id] = map[string]int{}
			order = append(order, id)
		}
		if len(snap.endpoints[id]) >= maxPerProv {
			continue
		}
		snap.endpoints[id] = append(snap.endpoints[id], pe.ep)
		hosts[id][pe.host]++
		p.EndpointCount++
		p.MinPriceMinor = min(p.MinPriceMinor, pe.ep.PriceMinor)
		p.MaxPriceMinor = max(p.MaxPriceMinor, pe.ep.PriceMinor)
		p.FreeTier = p.FreeTier || pe.ep.Free
		if pe.updated.After(snap.generated) {
			snap.generated = pe.updated.UTC()
		}
	}
	for _, id := range order {
		p := byID[id]
		best, n := "", 0
		for h, c := range hosts[id] {
			if c > n || (c == n && h < best) {
				best, n = h, c
			}
		}
		p.Host = best
		if p.ServiceURL == "" {
			p.ServiceURL = "https://" + best
		}
		slices.SortFunc(snap.endpoints[id], func(a, b catalog.Endpoint) int { return strings.Compare(a.Path+" "+a.Method, b.Path+" "+b.Method) })
		snap.providers = append(snap.providers, *p)
	}
	slices.SortFunc(snap.providers, func(a, b catalog.Provider) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	for i, p := range snap.providers {
		snap.byID[p.ID], snap.bySlug[p.FQN] = i, i
	}
	if snap.generated.IsZero() {
		snap.generated = now.UTC()
	}
	return snap
}
