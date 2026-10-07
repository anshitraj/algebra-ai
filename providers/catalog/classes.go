package catalog

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/project-algebra/algebra/internal/domain/routing"
)

// ClassIndex groups every listed endpoint of every catalog by the class of
// work it does (routing.Class): the step that lets the router compare Birdeye
// with Allium and a dozen PayAI listings for "token.price", instead of only
// ever calling the one provider an agent happened to name.
//
// Building it reads every provider's endpoints once, which for a catalog that
// lists endpoints per provider (Pay.sh) is one request each, so it is built in
// the background of the first request and cached like the catalogs are.
type ClassIndex struct {
	m     *Multi
	cache *Cached[*classSnapshot]
}

// ClassMember is one endpoint of a class, as the index saw it.
type ClassMember struct {
	Provider     string `json:"provider"`
	ProviderName string `json:"provider_name"`
	LogoURL      string `json:"logo_url,omitempty"`
	Source       string `json:"source"`
	Method       string `json:"method"`
	URL          string `json:"url"`
	Description  string `json:"description,omitempty"`
	// PriceMinor and Network are the cheapest listed way to pay it.
	PriceMinor int64    `json:"price_minor"`
	Networks   []string `json:"networks"`
	// Routable: Algebra knows how to give it the class's input. A member that
	// isn't is listed for comparison only.
	Routable bool   `json:"routable"`
	Reason   string `json:"not_routable_reason,omitempty"`
}

// ClassSummary is one class with its members.
type ClassSummary struct {
	routing.Class
	Members  []ClassMember `json:"members"`
	Routable int           `json:"routable"`
	// MedianPriceMinor is the median listed price of its members: what this
	// kind of work usually costs, which is how an outlier is recognised.
	MedianPriceMinor int64 `json:"median_price_minor"`
}

type classSnapshot struct {
	classes    map[string]*ClassSummary
	candidates map[string][]routing.Candidate
	built      time.Time
}

// NewClassIndex indexes the catalogs m serves.
func NewClassIndex(m *Multi) *ClassIndex {
	return &ClassIndex{m: m, cache: &Cached[*classSnapshot]{
		TTL: 15 * time.Minute, StaleFor: 12 * time.Hour, RetryAfter: time.Minute, RefreshTimeout: 3 * time.Minute,
	}}
}

// ClassCandidates returns the routable candidates for a class: every network each
// member can be paid on, with the class as their capability and the input
// adapter that fits the provider.
func (x *ClassIndex) ClassCandidates(ctx context.Context, classID string) ([]routing.Candidate, error) {
	snap, _, _, err := x.cache.Get(ctx, x.build)
	if err != nil {
		return nil, err
	}
	return slices.Clone(snap.candidates[classID]), nil
}

// Summaries returns every class with its members, routable or not.
func (x *ClassIndex) Summaries(ctx context.Context) ([]ClassSummary, time.Time, error) {
	snap, _, _, err := x.cache.Get(ctx, x.build)
	if err != nil {
		return nil, time.Time{}, err
	}
	out := make([]ClassSummary, 0, len(snap.classes))
	for _, c := range routing.Classes() {
		if s, ok := snap.classes[c.ID]; ok {
			out = append(out, *s)
		} else {
			out = append(out, ClassSummary{Class: c, Members: []ClassMember{}})
		}
	}
	return out, snap.built, nil
}

// Summary returns one class.
func (x *ClassIndex) Summary(ctx context.Context, classID string) (*ClassSummary, error) {
	c, ok := routing.ClassByID(classID)
	if !ok {
		return nil, ErrNotFound
	}
	snap, _, _, err := x.cache.Get(ctx, x.build)
	if err != nil {
		return nil, err
	}
	if s, ok := snap.classes[c.ID]; ok {
		cp := *s
		return &cp, nil
	}
	return &ClassSummary{Class: c, Members: []ClassMember{}}, nil
}

const (
	indexPage        = 200
	indexConcurrency = 8
	indexDetailWait  = 20 * time.Second
)

func (x *ClassIndex) build(ctx context.Context) (*classSnapshot, error) {
	var providers []Provider
	for off := 0; ; off += indexPage {
		l, err := x.m.List(ctx, Filter{Limit: indexPage, Offset: off})
		if err != nil {
			if len(providers) > 0 {
				break
			}
			return nil, err
		}
		providers = append(providers, l.Providers...)
		if len(l.Providers) < indexPage || off+indexPage >= l.Total {
			break
		}
	}

	details := make([]*Detail, len(providers))
	sem := make(chan struct{}, indexConcurrency)
	var wg sync.WaitGroup
	for i, p := range providers {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			dctx, cancel := context.WithTimeout(ctx, indexDetailWait)
			defer cancel()
			if d, err := x.m.Detail(dctx, p.ID); err == nil {
				details[i] = d
			}
		}()
	}
	wg.Wait()

	snap := &classSnapshot{classes: map[string]*ClassSummary{}, candidates: map[string][]routing.Candidate{}, built: time.Now().UTC()}
	for _, d := range details {
		if d != nil {
			snap.add(d)
		}
	}
	for id, s := range snap.classes {
		s.MedianPriceMinor = medianPrice(s.Members)
		slices.SortStableFunc(s.Members, func(a, b ClassMember) int {
			if a.Routable != b.Routable {
				if a.Routable {
					return -1
				}
				return 1
			}
			if a.PriceMinor != b.PriceMinor {
				return cmpInt(a.PriceMinor, b.PriceMinor)
			}
			return strings.Compare(a.Provider, b.Provider)
		})
		cands, _ := routing.Dedupe(snap.candidates[id])
		snap.candidates[id] = cands
	}
	return snap, nil
}

// add files one provider's endpoints under their classes.
func (s *classSnapshot) add(d *Detail) {
	for _, e := range d.Endpoints {
		classID, adapter, reason := classOf(e)
		if classID == "" {
			continue
		}
		c, _ := routing.ClassByID(classID)
		sum := s.classes[classID]
		if sum == nil {
			sum = &ClassSummary{Class: c}
			s.classes[classID] = sum
		}
		m := ClassMember{
			Provider: d.ID, ProviderName: d.Name, LogoURL: d.LogoURL, Source: d.Source, Method: e.Method, URL: e.URL,
			Description: e.Description, PriceMinor: e.PriceMinor, Networks: networksOf(e),
		}
		switch {
		case !e.Callable:
			m.Reason = e.Reason
		case adapter == nil:
			m.Reason = reason
		default:
			m.Routable = true
			sum.Routable++
		}
		sum.Members = append(sum.Members, m)
		if !m.Routable {
			continue
		}
		one := *d
		one.Endpoints = []Endpoint{e}
		cands, _ := Candidates(&one, sourceOf(d.Source), func(cand *routing.Candidate, _ Endpoint) {
			cand.Capability = classID
			cand.Input = adapter
		})
		s.candidates[classID] = append(s.candidates[classID], cands...)
	}
}

// classOf decides an endpoint's class and how to give it the class's input:
// a curated entry first, then the catalog's own words and parameter names.
func classOf(e Endpoint) (string, *routing.InputAdapter, string) {
	if cur, ok := curatedFor(e); ok {
		return cur.class, cur.adapter, ""
	}
	id, ok := routing.Classify(e.Path, e.Description)
	if !ok {
		return "", nil, ""
	}
	c, _ := routing.ClassByID(id)
	params, required, known := SchemaParams(e.InputSchema)
	for _, p := range PathParams(e.Path) {
		if !slices.Contains(params, p) {
			params = append(params, p)
		}
		if !slices.Contains(required, p) {
			required = append(required, p)
		}
	}
	if !known && len(params) == 0 {
		return id, nil, "the catalog doesn't say what it takes"
	}
	a, ok := c.Adapt(params, required)
	if !ok {
		return id, nil, "its parameters don't match the class's input"
	}
	return id, a, ""
}

// curated is what Algebra knows about an endpoint that its catalog doesn't
// say: big providers whose listings carry no input description.
type curated struct {
	host, path, method string
	class              string
	adapter            *routing.InputAdapter
}

var curatedAdapters = []curated{
	{"public-api.birdeye.so", "/x402/defi/price", "GET", "token.price", &routing.InputAdapter{Rename: map[string]string{"mint": "address"}}},
	{"public-api.birdeye.so", "/x402/defi/token_security", "GET", "solana.token-risk", &routing.InputAdapter{Rename: map[string]string{"mint": "address"}}},
	{"public-api.birdeye.so", "/x402/defi/token_overview", "GET", "token.price", &routing.InputAdapter{Rename: map[string]string{"mint": "address"}}},
	{"x402.alchemy.com", "/prices/v1/tokens/by-address", "POST", "token.price",
		&routing.InputAdapter{Template: json.RawMessage(`{"addresses":[{"network":"solana-mainnet","address":"{{mint}}"}]}`)}},
	{"x402.alchemy.com", "/data/v1/assets/tokens/by-address", "POST", "wallet.balances",
		&routing.InputAdapter{Template: json.RawMessage(`{"addresses":[{"address":"{{wallet}}","networks":["solana-mainnet"]}]}`)}},
}

func curatedFor(e Endpoint) (curated, bool) {
	u, err := url.Parse(e.URL)
	if err != nil {
		return curated{}, false
	}
	path := "/" + strings.Trim(u.Path, "/")
	for _, c := range curatedAdapters {
		if strings.EqualFold(u.Hostname(), c.host) && path == c.path && strings.EqualFold(e.Method, c.method) {
			return c, true
		}
	}
	return curated{}, false
}

func networksOf(e Endpoint) []string {
	var out []string
	for _, p := range e.Payments {
		if !slices.Contains(out, p.Network) {
			out = append(out, p.Network)
		}
	}
	if len(out) == 0 && e.Network != "" {
		out = append(out, e.Network)
	}
	return out
}

// sourceOf is the discovery source a catalog's candidates carry.
func sourceOf(name string) routing.DiscoverySource {
	switch name {
	case "pay.sh":
		return routing.SourcePaySh
	case "circle":
		return routing.SourceCircle
	}
	return routing.SourceX402
}

// medianPrice is the median listed price of the members that have one.
func medianPrice(ms []ClassMember) int64 {
	var ps []int64
	for _, m := range ms {
		if m.PriceMinor > 0 {
			ps = append(ps, m.PriceMinor)
		}
	}
	if len(ps) == 0 {
		return 0
	}
	slices.Sort(ps)
	return ps[len(ps)/2]
}

func cmpInt(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
