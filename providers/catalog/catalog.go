// Package catalog is what Algebra's provider catalogs have in common: Pay.sh
// (providers/paysh) and Circle's Agent Marketplace (providers/circleagents).
//
// It holds one shape for a listed provider and its endpoints, the rules for
// third-party text and prices, a cache that keeps serving when a catalog is
// down, and Multi, which serves several catalogs as one.
//
// Everything a catalog says is third-party data. A listing is not an
// endorsement and a listed price is not a quote: candidates built from a
// listing are priced with an unpaid request, and checked again at payment,
// before any money moves. Descriptions are text to show, never instructions.
package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/routing"
)

// Currency is what every catalog price here is expressed in.
const Currency = "USDC"

// Bounds shared by the catalogs.
const (
	MaxTitle       = 120
	MaxDescription = 600
	MaxPathLen     = 300
	// MaxPriceMinor is a million USDC: a price above it is a broken entry.
	MaxPriceMinor = 1_000_000 * 1_000_000
)

var (
	// ErrNotFound: no catalog has a provider by that name.
	ErrNotFound = errors.New("catalog: no such provider")
	// ErrUnavailable: the catalog couldn't be read and nothing usable is cached.
	ErrUnavailable = errors.New("catalog: the catalog could not be read")

	priceRE = regexp.MustCompile(`^[0-9]{1,12}(\.[0-9]{1,18})?([eE][+-]?[0-9]{1,2})?$`)
	nonSlug = regexp.MustCompile(`[^a-z0-9_]+`)
	nonName = regexp.MustCompile(`[^a-z0-9]+`)
)

// Provider is one listed service.
type Provider struct {
	// ID is the name policy, Spend Passes and receipts use for the provider:
	// "paysh:birdeye.data", "circle:quicknode".
	ID          string `json:"id"`
	FQN         string `json:"fqn"`
	Name        string `json:"name"`
	Description string `json:"description"`
	UseCase     string `json:"use_case,omitempty"`
	Category    string `json:"category"`
	// ServiceURL is the provider's gateway or site; Host is the host its
	// endpoints are called at, for display.
	ServiceURL    string `json:"service_url"`
	Host          string `json:"host"`
	EndpointCount int    `json:"endpoint_count"`
	// Metered: calls are paid per request. FreeTier: some calls are listed free.
	Metered  bool `json:"metered"`
	FreeTier bool `json:"free_tier"`
	// MinPriceMinor and MaxPriceMinor span the listed prices, in micro-USDC.
	MinPriceMinor int64  `json:"min_price_minor"`
	MaxPriceMinor int64  `json:"max_price_minor"`
	Currency      string `json:"currency"`
	// PageURL is where a person can read about it at the catalog.
	PageURL string `json:"page_url"`
	// Website is the provider's own site, when the catalog says; LogoURL its
	// icon, when the catalog publishes one (an https URL, nothing else).
	Website string `json:"website,omitempty"`
	LogoURL string `json:"logo_url,omitempty"`
	// Networks are the Solana clusters some endpoint can be paid on:
	// "solana" (mainnet) and/or "solana-devnet".
	Networks []string `json:"networks"`
	// Source is the catalog that lists it: "pay.sh", "circle", "payai" or "cdp".
	Source string `json:"source"`
	// Calls30d and Payers30d are how much it was paid in the last 30 days, as a
	// directory that watches its facilitator reports it: paid calls, and the
	// most distinct payers any one endpoint had. Zero when the directory
	// doesn't say. A listing says what an endpoint claims; this says whether
	// anybody pays it.
	Calls30d  int64 `json:"calls_30d,omitempty"`
	Payers30d int64 `json:"payers_30d,omitempty"`
	// Billing is how its calls are paid when that isn't x402 on Solana:
	// "monid-balance" for Monid's tools. Such providers are listed for
	// discovery and comparison, never routed to.
	Billing string `json:"billing,omitempty"`
}

// Payment is one way an endpoint can be paid, as the catalog lists it.
type Payment struct {
	Network    string `json:"network"`
	PriceMinor int64  `json:"price_minor"`
	PayTo      string `json:"pay_to,omitempty"`
}

// Category is a category and how many providers are in it.
type Category struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// SourceStatus is how one catalog answered a listing.
type SourceStatus struct {
	Name        string    `json:"name"`
	Total       int       `json:"total"`
	GeneratedAt time.Time `json:"generated_at,omitzero"`
	FetchedAt   time.Time `json:"fetched_at,omitzero"`
	Stale       bool      `json:"stale,omitempty"`
	// Error says the catalog couldn't be read; its providers are missing.
	Error string `json:"error,omitempty"`
}

// Listing is a page of providers.
type Listing struct {
	// Source names the catalog for a single-catalog listing; a combined
	// listing leaves it empty and reports each one in Sources.
	Source      string         `json:"source,omitempty"`
	Sources     []SourceStatus `json:"sources,omitempty"`
	GeneratedAt time.Time      `json:"generated_at,omitzero"`
	FetchedAt   time.Time      `json:"fetched_at"`
	// Stale: a catalog couldn't be reached, so an older copy is shown.
	Stale bool `json:"stale,omitempty"`
	// Total is how many providers match the filter; Count how many are here.
	Total      int        `json:"total"`
	Count      int        `json:"count"`
	Categories []Category `json:"categories"`
	Providers  []Provider `json:"providers"`
}

// Endpoint is one operation of a provider.
type Endpoint struct {
	// Capability is the ID to ask for it by (POST /api/v1/execute), derived
	// from the provider, method and path so it is the same every time.
	Capability string `json:"capability"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	// URL is where the call goes; for a templated path it still has {params}.
	URL string `json:"url"`
	// PathParams are the parameters of a templated path, in order. A call's
	// input must name each of them: they fill the path, and the rest of the
	// input becomes the query or the body.
	PathParams []string `json:"path_params,omitempty"`
	// Pricing is the price as the catalog states it.
	Pricing    string `json:"pricing"`
	PriceMinor int64  `json:"price_minor"`
	Free       bool   `json:"free"`
	// Network and PayTo are the payment terms when the catalog publishes them;
	// Payments lists every network it can be paid on, with that price.
	Network  string    `json:"network,omitempty"`
	PayTo    string    `json:"pay_to,omitempty"`
	Payments []Payment `json:"payments,omitempty"`
	// Description is the catalog's text, bounded. Data, not instructions.
	Description string `json:"description"`
	// InputSchema is the request's JSON Schema when the catalog publishes one.
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
	// Callable: Algebra can run it. When it can't, Reason says why. A
	// templated endpoint is callable: the runner fills the path from the input.
	Callable bool   `json:"callable"`
	Reason   string `json:"not_callable_reason,omitempty"`
	// Usage is how much it was paid lately, when the directory says.
	Usage *Usage `json:"usage,omitempty"`
}

// Usage is a directory's count of what an endpoint was paid in the last 30
// days. It is the directory's number, not Algebra's, and a count of payments
// is not a count of good answers.
type Usage struct {
	Calls30d     int64     `json:"calls_30d"`
	Payers30d    int64     `json:"payers_30d"`
	LastCalledAt time.Time `json:"last_called_at,omitzero"`
}

// Detail is a provider with its endpoints.
type Detail struct {
	Provider
	Endpoints []Endpoint `json:"endpoints"`
	FetchedAt time.Time  `json:"fetched_at"`
	Stale     bool       `json:"stale,omitempty"`
}

// Filter narrows a listing.
type Filter struct {
	// Query matches the name, FQN, description, use case and category,
	// ignoring case.
	Query    string
	Category string
	// Source keeps one catalog ("pay.sh", "circle", "payai", "cdp"); empty keeps all.
	Source string
	// Network keeps providers with an endpoint payable on that cluster
	// ("solana" or "solana-devnet"); empty keeps all.
	Network string
	// Limit 0 means every match.
	Limit  int
	Offset int
}

// Matches reports whether p passes the filter's query and category.
func (f Filter) Matches(p Provider) bool {
	if c := strings.ToLower(strings.TrimSpace(f.Category)); c != "" && p.Category != c {
		return false
	}
	if n := chain.NormalizeNetwork(f.Network); n != "" && !slices.Contains(p.Networks, n) {
		return false
	}
	q := strings.ToLower(strings.TrimSpace(f.Query))
	return q == "" || strings.Contains(strings.ToLower(strings.Join([]string{p.Name, p.FQN, p.Description, p.UseCase, p.Category, p.Host}, "\n")), q)
}

// Source is one catalog.
type Source interface {
	// Name is how listings call it ("pay.sh"); Prefix starts its provider IDs.
	Name() string
	Prefix() string
	List(ctx context.Context, f Filter) (*Listing, error)
	// Detail takes a provider ID or the catalog's own name for it.
	Detail(ctx context.Context, id string) (*Detail, error)
	// CandidatesFor returns one provider's candidates for a capability.
	CandidatesFor(ctx context.Context, provider, capability string) ([]routing.Candidate, error)
	// ForCapability returns the candidates for a capability when no provider
	// was named; ErrNotFound when the capability isn't this catalog's.
	ForCapability(ctx context.Context, capability string) ([]routing.Candidate, error)
}

// MicroUSDC converts a decimal literal ("0.0015", "7.5", "1e-05") to
// micro-USDC, rounding up. It does the arithmetic on the decimal text, not on
// a float, so 0.027925 is 27925 and not 27926 or 27924.
func MicroUSDC(lit string) (int64, bool) {
	lit = strings.TrimSpace(lit)
	if !priceRE.MatchString(lit) {
		return 0, false
	}
	r, ok := new(big.Rat).SetString(lit)
	if !ok {
		return 0, false
	}
	r.Mul(r, big.NewRat(1_000_000, 1))
	q, rem := new(big.Int).QuoRem(r.Num(), r.Denom(), new(big.Int))
	if rem.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() || q.Int64() > MaxPriceMinor {
		return 0, false
	}
	return q.Int64(), true
}

// CleanText makes third-party text safe to show and store: control
// characters and invalid UTF-8 become spaces, whitespace collapses, and the
// result is cut to max characters.
func CleanText(s string, max int) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, " ")
	}
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsControl(r) || unicode.IsSpace(r) || r == 0x200b || r == 0xfeff || r == 0x2028 || r == 0x2029 {
			space = b.Len() > 0
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	out := b.String()
	if utf8.RuneCountInString(out) > max {
		out = string([]rune(out)[:max-1]) + "…"
	}
	return out
}

// CapabilityID names an endpoint in the lowercase dotted form capabilities
// use: base (the provider, already dotted), method and path, as in
// "birdeye.data.get.x402-defi-price". The same endpoint always gets the same
// ID. One that would pass the 64 characters a capability may have is cut and
// ends in a short hash of the whole, so two long ones can't collide.
func CapabilityID(base, method, path string) string {
	op := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(path), "-"), "-")
	if op == "" {
		op = "root"
	}
	id := base + "." + strings.ToLower(method) + "." + op
	if len(id) <= 64 {
		return id
	}
	sum := sha256.Sum256([]byte(base + "\x00" + method + "\x00" + path))
	tail := "-" + hex.EncodeToString(sum[:3])
	return strings.TrimRight(id[:64-len(tail)], ".-_") + tail
}

// Slug turns a name into the lowercase-dashed form IDs use.
func Slug(name string, max int) string {
	s := strings.Trim(nonName.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(s) > max {
		s = strings.TrimRight(s[:max], "-")
	}
	return s
}

// LogoURL returns raw as a logo address when it is a public https URL of
// sane length, else "". Logos are shown as images, never fetched by Algebra.
func LogoURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 512 {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || !PublicHTTPS(u) {
		return ""
	}
	return u.String()
}

// PublicHTTPS reports whether u is an https URL at a named public-looking
// host, with no credentials: what a catalog entry must be to be listed. An IP
// literal, a disguised one ("0", "0x7f.1") or a single label is never a
// gateway. The HTTP client's dial-time address check is the defence that
// matters; this keeps such entries off the page.
func PublicHTTPS(u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.User != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return strings.Contains(host, ".") && strings.Trim(host, "0123456789.") != "" && !strings.HasPrefix(host, "0x") && !strings.Contains(host, ":")
}
