// Package bazaar reads x402 discovery directories ("bazaars"): public lists
// of paid x402 endpoints, each with the payment terms its provider publishes.
// Two are wired in: Circle's Agent Marketplace (agents.circle.com, served by
// GET https://api.circle.com/v2/x402/discovery/resources) and PayAI's
// facilitator bazaar (GET https://facilitator.payai.network/discovery/resources).
// Both speak the same discovery format, with small differences a Profile
// captures.
//
// Algebra keeps what it can pay: endpoints that accept Circle's real USDC on
// Solana, mainnet or devnet, as plain x402 (not Circle Gateway batching and
// not behind a browser sign-in). It groups them by provider and records, for
// each endpoint, the networks it can be paid on and the price on each.
//
// The rules every catalog follows apply (providers/catalog): fetched through
// the SSRF-safe client with a body limit, every field validated and bounded,
// one malformed entry never sinks the rest, a listing is not an endorsement,
// and the published terms are checked again against the provider's own 402
// answer before anything is paid.
package bazaar

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

// The Solana clusters, as discovery directories write them.
const (
	caipMainnet = "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp"
	caipDevnet  = "solana:EtWTRABZaYq6iMfeYKouRu166VU2xqa1"

	maxItems      = 6000
	maxPerProv    = 600
	maxSchemaSize = 16 << 10
	retryAfter    = 30 * time.Second
)

// Profile describes one directory.
type Profile struct {
	// Name is how listings call it; Prefix starts its provider IDs;
	// CapabilityPrefix starts its capability IDs, so they never collide with
	// another catalog's.
	Name, Prefix, CapabilityPrefix string
	URL                            string
	// PageURL is where a person browses the directory; empty uses each
	// provider's own website.
	PageURL string
	// Networks maps each Solana cluster to the network values to ask the
	// directory for (some list x402 v1 names and CAIP-2 IDs separately).
	Networks map[string][]string
	// Query is added to every request.
	Query url.Values
	// PageSize is the largest page the directory serves.
	PageSize int
	// Source is the discovery source candidates from it carry.
	Source routing.DiscoverySource
}

// Circle is Circle's Agent Marketplace. Only plain x402 without a browser
// sign-in: Circle Gateway batching and SIWX can't be paid by an agent here.
func Circle(discoveryURL string) Profile {
	if discoveryURL == "" {
		discoveryURL = "https://api.circle.com/v2/x402/discovery/resources"
	}
	return Profile{
		Name: "circle", Prefix: "circle:", CapabilityPrefix: "circle.", URL: discoveryURL,
		PageURL:  "https://agents.circle.com/services",
		Networks: map[string][]string{chain.Solana: {caipMainnet}, chain.SolanaDevnet: {caipDevnet}},
		Query:    url.Values{"supportsVanillax402": {"true"}, "siwx": {"false"}},
		PageSize: 200, Source: routing.SourceCircle,
	}
}

// PayAI is PayAI's facilitator bazaar, the open directory of x402 services
// that settle through PayAI on Solana (and other chains).
func PayAI(discoveryURL string) Profile {
	if discoveryURL == "" {
		discoveryURL = "https://facilitator.payai.network/discovery/resources"
	}
	return Profile{
		Name: "payai", Prefix: "payai:", CapabilityPrefix: "payai.", URL: discoveryURL,
		Networks: map[string][]string{chain.Solana: {caipMainnet, "solana"}, chain.SolanaDevnet: {caipDevnet, "solana-devnet"}},
		PageSize: 1000, Source: routing.SourceX402,
	}
}

// HTTPDoer is the HTTP client used. *safehttp.Client satisfies it.
type HTTPDoer interface {
	Do(req *http.Request) (*safehttp.Response, error)
}

// Config configures a Client.
type Config struct {
	Profile Profile
	HTTP    HTTPDoer
	// TTL is how long a fetched copy is served; StaleFor how long past that
	// it is still served when the directory can't be reached.
	TTL      time.Duration
	StaleFor time.Duration
	Now      func() time.Time
}

// Client reads one directory. Safe for concurrent use.
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
	if cfg.TTL <= 0 {
		cfg.TTL = 10 * time.Minute
	}
	if cfg.StaleFor <= 0 {
		cfg.StaleFor = 6 * time.Hour
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Profile.PageSize <= 0 {
		cfg.Profile.PageSize = 200
	}
	return &Client{cfg: cfg, cache: &catalog.Cached[*snapshot]{
		TTL: cfg.TTL, StaleFor: cfg.StaleFor, RetryAfter: retryAfter, Now: cfg.Now, RefreshTimeout: 90 * time.Second,
	}}
}

// Name is how listings call this directory.
func (c *Client) Name() string { return c.cfg.Profile.Name }

// Prefix starts the name of every provider this directory has.
func (c *Client) Prefix() string { return c.cfg.Profile.Prefix }

// List returns the providers, filtered. Limit 0 returns every match.
func (c *Client) List(ctx context.Context, f catalog.Filter) (*catalog.Listing, error) {
	snap, fetched, stale, err := c.cache.Get(ctx, c.fetch)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	var matched []catalog.Provider
	for _, p := range snap.providers {
		if n := chain.NormalizeNetwork(f.Network); n != "" && !slices.Contains(p.Networks, n) {
			continue
		}
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
		Source: c.cfg.Profile.Name, GeneratedAt: snap.generated, FetchedAt: fetched, Stale: stale,
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

// Detail returns one provider and its endpoints, by provider ID or slug.
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

// CandidatesFor returns one provider's candidates for a capability, one per
// network the endpoint can be paid on.
func (c *Client) CandidatesFor(ctx context.Context, provider, capability string) ([]routing.Candidate, error) {
	d, err := c.Detail(ctx, provider)
	if err != nil {
		return nil, err
	}
	cands, _ := catalog.Candidates(d, c.cfg.Profile.Source, nil)
	return slices.DeleteFunc(cands, func(x routing.Candidate) bool { return x.Capability != capability }), nil
}

// ForCapability finds the candidates for one of this directory's capabilities
// without a provider named.
func (c *Client) ForCapability(ctx context.Context, capability string) ([]routing.Candidate, error) {
	capability = strings.ToLower(strings.TrimSpace(capability))
	if !strings.HasPrefix(capability, c.cfg.Profile.CapabilityPrefix) {
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

// --- fetching ---

// netItem is a discovery entry and the cluster it was listed under.
type netItem struct {
	network string
	raw     json.RawMessage
}

func (c *Client) fetch(ctx context.Context) (*snapshot, error) {
	if c.cfg.HTTP == nil {
		return nil, errors.New("bazaar: no HTTP client configured")
	}
	var items []netItem
	clusters := []string{chain.Solana, chain.SolanaDevnet}
	for _, cluster := range clusters {
		for _, value := range c.cfg.Profile.Networks[cluster] {
			for offset := 0; len(items) < maxItems; offset += c.cfg.Profile.PageSize {
				page, total, err := c.page(ctx, value, offset)
				if err != nil {
					return nil, err
				}
				for _, raw := range page {
					items = append(items, netItem{network: cluster, raw: raw})
				}
				if len(page) == 0 || offset+c.cfg.Profile.PageSize >= total {
					break
				}
			}
		}
	}
	return parse(c.cfg.Profile, items, c.cfg.Now())
}

func (c *Client) page(ctx context.Context, network string, offset int) ([]json.RawMessage, int, error) {
	q := url.Values{}
	for k, v := range c.cfg.Profile.Query {
		q[k] = v
	}
	q.Set("network", network)
	q.Set("limit", strconv.Itoa(c.cfg.Profile.PageSize))
	q.Set("offset", strconv.Itoa(offset))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.Profile.URL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "algebra-bazaar/1")
	resp, err := c.cfg.HTTP.Do(req)
	if err != nil {
		return nil, 0, err
	}
	if resp.Status != http.StatusOK {
		return nil, 0, fmt.Errorf("%s answered HTTP %d", c.cfg.Profile.Name, resp.Status)
	}
	var doc struct {
		Items      []json.RawMessage `json:"items"`
		Pagination struct {
			Total int `json:"total"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(resp.Body, &doc); err != nil {
		return nil, 0, fmt.Errorf("bazaar: %s's directory isn't valid JSON: %w", c.cfg.Profile.Name, err)
	}
	return doc.Items, doc.Pagination.Total, nil
}

// --- parsing ---

type rawItem struct {
	Resource    string      `json:"resource"`
	Type        string      `json:"type"`
	LastUpdated string      `json:"lastUpdated"`
	Accepts     []rawAccept `json:"accepts"`
	Metadata    rawMeta     `json:"metadata"`
	// Some directories put these at the top level instead of in metadata.
	ServiceName string          `json:"serviceName"`
	Description string          `json:"description"`
	IconURL     string          `json:"iconUrl"`
	Method      string          `json:"method"`
	Tags        []string        `json:"tags"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Extensions  struct {
		Bazaar struct {
			Info struct {
				Input json.RawMessage `json:"input"`
			} `json:"info"`
		} `json:"bazaar"`
	} `json:"extensions"`
}

type rawAccept struct {
	Scheme  string          `json:"scheme"`
	Network string          `json:"network"`
	Asset   string          `json:"asset"`
	PayTo   string          `json:"payTo"`
	Amount  json.RawMessage `json:"amount"`
	MaxReq  json.RawMessage `json:"maxAmountRequired"`
}

type rawMeta struct {
	Provider struct {
		Name        string   `json:"name"`
		Website     string   `json:"website"`
		Description string   `json:"description"`
		Category    string   `json:"category"`
		Tags        []string `json:"tags"`
	} `json:"provider"`
	ServiceName string          `json:"serviceName"`
	IconURL     string          `json:"iconUrl"`
	Method      string          `json:"method"`
	Description string          `json:"description"`
	Tags        []string        `json:"tags"`
	Input       json.RawMessage `json:"input"`
	SIWX        bool            `json:"siwx"`
	Vanilla     *bool           `json:"supportsVanillax402"`
}

var methods = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}

// circleCategories maps Circle's categories onto the words the other catalogs use.
var circleCategories = map[string]string{
	"FINANCIAL_ANALYSIS":  "finance",
	"PREDICTION_MARKETS":  "prediction_markets",
	"WEB_SEARCH_RESEARCH": "search",
	"DATA_ENRICHMENT":     "data",
	"CREATIVE":            "media",
	"SOCIAL_INTELLIGENCE": "social",
	"INFRASTRUCTURE":      "infrastructure",
}

// tagCategory guesses a category from tags when the directory has none.
func tagCategory(tags []string) string {
	for _, t := range tags {
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "defi", "crypto", "trading", "finance", "market-data", "prices", "price", "token", "tokens", "onchain", "on-chain", "dex", "wallet", "nft", "stocks", "solana":
			return "finance"
		case "search", "web-search", "research", "news":
			return "search"
		case "ai", "llm", "inference", "ml", "image-generation", "agents", "agent", "chat":
			return "ai_ml"
		case "data", "analytics", "enrichment", "scraping", "api":
			return "data"
		case "social", "twitter", "x", "farcaster":
			return "social"
		case "email", "sms", "messaging", "phone":
			return "messaging"
		case "weather", "maps", "geo", "location", "travel":
			return "maps"
		case "image", "video", "audio", "media", "music":
			return "media"
		}
	}
	return "other"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// methodOf finds an entry's HTTP method wherever the directory put it.
func methodOf(it rawItem) string {
	m := firstNonEmpty(it.Metadata.Method, it.Method)
	for _, in := range []json.RawMessage{it.Metadata.Input, it.Extensions.Bazaar.Info.Input} {
		if m != "" {
			break
		}
		var x struct {
			Method string `json:"method"`
		}
		if json.Unmarshal(in, &x) == nil {
			m = x.Method
		}
	}
	if m == "" {
		m = http.MethodGet
	}
	return strings.ToUpper(m)
}

// inputOf is the entry's input description, if a directory published one.
func inputOf(it rawItem) json.RawMessage {
	for _, in := range []json.RawMessage{it.Metadata.Input, it.InputSchema, it.Extensions.Bazaar.Info.Input} {
		if s := strings.TrimSpace(string(in)); s != "" && s != "null" && len(in) <= maxSchemaSize && json.Valid(in) {
			return slices.Clone(in)
		}
	}
	return nil
}

// amountOf reads an accept's price, v2 ("amount") or v1 ("maxAmountRequired").
func amountOf(a rawAccept) (int64, bool) {
	raw := a.Amount
	if len(raw) == 0 || string(raw) == "null" {
		raw = a.MaxReq
	}
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 || n > catalog.MaxPriceMinor {
		return 0, false
	}
	return n, true
}

type endpointRec struct {
	provider string // slug
	name     string
	website  string
	logo     string
	desc     string
	category string
	host     string
	updated  time.Time
	ep       catalog.Endpoint
}

// parse turns directory entries into providers and their endpoints. Each
// entry is decoded and checked on its own; one that can't be paid in USDC on
// the Solana cluster it was listed under, or doesn't validate, is dropped
// without affecting the rest. The same endpoint listed for mainnet and devnet
// becomes one endpoint payable on both.
func parse(p Profile, items []netItem, now time.Time) (*snapshot, error) {
	recs := map[string]*endpointRec{}
	var order []string
	for _, ni := range items {
		var it rawItem
		if json.Unmarshal(ni.raw, &it) != nil || (it.Type != "" && it.Type != "http") || it.Metadata.SIWX || (it.Metadata.Vanilla != nil && !*it.Metadata.Vanilla) {
			continue
		}
		u, err := url.Parse(strings.TrimSpace(it.Resource))
		if err != nil || !catalog.PublicHTTPS(u) || u.Fragment != "" || len(it.Resource) > 2048 {
			continue
		}
		method := methodOf(it)
		if !slices.Contains(methods, method) {
			continue
		}
		// Express-style ":param" segments become {param}: one placeholder form
		// is all the runner has to fill in.
		if t := catalog.BraceTemplate(u.Path); t != u.Path {
			u.Path, u.RawPath = t, ""
		}
		host := strings.ToLower(u.Hostname())
		name := catalog.CleanText(firstNonEmpty(it.Metadata.Provider.Name, it.ServiceName, it.Metadata.ServiceName, host), catalog.MaxTitle)
		slug := catalog.Slug(name, 48)
		if slug == "" {
			continue
		}
		usdc, ok := chain.AssetAddress(ni.network, "USDC")
		if !ok {
			continue
		}
		pay := catalog.Payment{Network: ni.network, PriceMinor: -1}
		for _, a := range it.Accepts {
			if !strings.EqualFold(a.Scheme, "exact") || chain.NormalizeNetwork(a.Network) != ni.network || !chain.SameAddress(ni.network, a.Asset, usdc) {
				continue
			}
			n, ok := amountOf(a)
			// Several options in USDC on this cluster: list the cheapest, as
			// the runner will pick it at quote time.
			if ok && (pay.PriceMinor < 0 || n < pay.PriceMinor) {
				pay.PriceMinor, pay.PayTo = n, catalog.CleanText(a.PayTo, 64)
			}
		}
		if pay.PriceMinor < 0 {
			continue // not payable in Circle's USDC on this cluster
		}
		// A URL writes braces as %7B and %7D; a person reading the path doesn't
		// want to see that.
		path := catalog.UnescapeBraces(strings.TrimPrefix(u.EscapedPath(), "/"))
		if u.RawQuery != "" {
			path += "?" + u.RawQuery
		}
		if len(path) > catalog.MaxPathLen {
			continue
		}

		key := method + " " + u.String()
		r, seen := recs[key]
		if !seen {
			category := circleCategories[strings.ToUpper(strings.TrimSpace(it.Metadata.Provider.Category))]
			if category == "" {
				category = tagCategory(append(append(slices.Clone(it.Metadata.Provider.Tags), it.Metadata.Tags...), it.Tags...))
			}
			website := ""
			if w, err := url.Parse(strings.TrimSpace(it.Metadata.Provider.Website)); err == nil && catalog.PublicHTTPS(w) {
				website = strings.TrimRight(w.String(), "/")
			}
			r = &endpointRec{
				provider: slug, name: name, website: website, host: host, category: category,
				logo: catalog.LogoURL(firstNonEmpty(it.IconURL, it.Metadata.IconURL)),
				desc: catalog.CleanText(it.Metadata.Provider.Description, catalog.MaxDescription),
				ep: catalog.Endpoint{
					Capability: catalog.CapabilityID(p.CapabilityPrefix+slug, method, path),
					Method:     method, Path: path, URL: catalog.UnescapeBraces(u.String()), PathParams: catalog.PathParams(path),
					Description: catalog.CleanText(firstNonEmpty(it.Metadata.Description, it.Description), catalog.MaxDescription),
					InputSchema: inputOf(it), Callable: true,
				},
			}
			recs[key] = r
			order = append(order, key)
		}
		if t, err := time.Parse(time.RFC3339, it.LastUpdated); err == nil && t.After(r.updated) {
			r.updated = t
		}
		if !slices.ContainsFunc(r.ep.Payments, func(x catalog.Payment) bool { return x.Network == pay.Network }) {
			r.ep.Payments = append(r.ep.Payments, pay)
		}
	}
	if len(items) > 0 && len(recs) == 0 {
		return nil, fmt.Errorf("bazaar: %s's directory has entries but none payable in USDC on Solana that this version understands", p.Name)
	}
	return group(p, recs, order, now), nil
}

// group gathers endpoints by provider and fills in each endpoint's headline
// terms: mainnet's when it has them, else devnet's.
func group(p Profile, recs map[string]*endpointRec, order []string, now time.Time) *snapshot {
	snap := &snapshot{endpoints: map[string][]catalog.Endpoint{}, byID: map[string]int{}, bySlug: map[string]int{}}
	byID := map[string]*catalog.Provider{}
	hosts := map[string]map[string]int{}
	var ids []string
	for _, key := range order {
		r := recs[key]
		ep := r.ep
		slices.SortFunc(ep.Payments, func(a, b catalog.Payment) int { return strings.Compare(a.Network, b.Network) }) // "solana" first
		head := ep.Payments[0]
		ep.Network, ep.PriceMinor, ep.PayTo, ep.Free = head.Network, head.PriceMinor, head.PayTo, head.PriceMinor == 0
		ep.Pricing = chain.FormatUnits(head.PriceMinor, chain.USDCDecimals) + " USDC"

		id := p.Prefix + r.provider
		prov, ok := byID[id]
		if !ok {
			prov = &catalog.Provider{
				ID: id, FQN: r.provider, Name: r.name, Description: r.desc, Category: r.category,
				Metered: true, Currency: catalog.Currency, Source: p.Name, Website: r.website, LogoURL: r.logo,
				MinPriceMinor: ep.PriceMinor, MaxPriceMinor: ep.PriceMinor, Networks: []string{},
			}
			byID[id], hosts[id] = prov, map[string]int{}
			ids = append(ids, id)
		}
		if len(snap.endpoints[id]) >= maxPerProv {
			continue
		}
		snap.endpoints[id] = append(snap.endpoints[id], ep)
		hosts[id][r.host]++
		prov.EndpointCount++
		prov.MinPriceMinor = min(prov.MinPriceMinor, ep.PriceMinor)
		prov.MaxPriceMinor = max(prov.MaxPriceMinor, ep.PriceMinor)
		prov.FreeTier = prov.FreeTier || ep.Free
		if prov.Description == "" {
			prov.Description = ep.Description
		}
		if prov.LogoURL == "" {
			prov.LogoURL = r.logo
		}
		for _, pay := range ep.Payments {
			if !slices.Contains(prov.Networks, pay.Network) {
				prov.Networks = append(prov.Networks, pay.Network)
			}
		}
		if r.updated.After(snap.generated) {
			snap.generated = r.updated.UTC()
		}
	}
	for _, id := range ids {
		prov := byID[id]
		best, n := "", 0
		for h, c := range hosts[id] {
			if c > n || (c == n && h < best) {
				best, n = h, c
			}
		}
		prov.Host = best
		if prov.Website == "" {
			prov.Website = "https://" + best
		}
		prov.ServiceURL = prov.Website
		prov.PageURL = firstNonEmpty(p.PageURL, prov.Website)
		slices.Sort(prov.Networks)
		slices.SortFunc(snap.endpoints[id], func(a, b catalog.Endpoint) int { return strings.Compare(a.Path+" "+a.Method, b.Path+" "+b.Method) })
		snap.providers = append(snap.providers, *prov)
	}
	slices.SortFunc(snap.providers, func(a, b catalog.Provider) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	for i, prov := range snap.providers {
		snap.byID[prov.ID], snap.bySlug[prov.FQN] = i, i
	}
	if snap.generated.IsZero() {
		snap.generated = now.UTC()
	}
	return snap
}
