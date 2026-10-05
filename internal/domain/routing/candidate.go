package routing

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/econ"
)

// ExecutionType is how a candidate is invoked and paid.
type ExecutionType string

const (
	// ExecX402 is an HTTP resource paid per call through the x402 protocol.
	ExecX402 ExecutionType = "x402"
	// ExecMPP is a resource paid through MPP.
	ExecMPP ExecutionType = "mpp"
	// ExecSolanaSwap is an on-chain swap Algebra builds itself.
	ExecSolanaSwap ExecutionType = "solana_swap"
	// ExecHTTPAPI is a plain HTTP API.
	ExecHTTPAPI ExecutionType = "http_api"
	// ExecMCP is a tool on a Model Context Protocol server.
	ExecMCP ExecutionType = "mcp"
)

// Valid reports whether t is a known execution type.
func (t ExecutionType) Valid() bool {
	switch t {
	case ExecX402, ExecMPP, ExecSolanaSwap, ExecHTTPAPI, ExecMCP:
		return true
	}
	return false
}

// needsEndpoint: everything but a native swap is reached at a URL.
func (t ExecutionType) needsEndpoint() bool { return t != ExecSolanaSwap }

// DiscoverySource is where a candidate was found. Sources differ in how far
// they can be trusted, which policy ("unknown providers need approval") and
// the merge rules both use.
type DiscoverySource string

const (
	// SourceNative: an execution adapter Algebra ships and maintains.
	SourceNative DiscoverySource = "native"
	// SourceConfigured: an endpoint the operator pinned by hand.
	SourceConfigured DiscoverySource = "configured"
	// SourceCircle, SourcePaySh, SourceX402 and SourceMCP are structured
	// registries and discovery endpoints.
	SourceCircle DiscoverySource = "circle"
	SourcePaySh  DiscoverySource = "paysh"
	SourceX402   DiscoverySource = "x402"
	SourceMCP    DiscoverySource = "mcp"
	// SourceHistory: Algebra's own record of providers it has executed.
	SourceHistory DiscoverySource = "history"
	// SourceWeb: the open web. The long tail nobody has indexed, unverified.
	SourceWeb DiscoverySource = "web"
)

// rank orders sources from most to least trusted; unknown sources sort last.
func (s DiscoverySource) rank() int {
	switch s {
	case SourceNative:
		return 0
	case SourceConfigured:
		return 1
	case SourceCircle, SourcePaySh, SourceX402, SourceMCP:
		return 2
	case SourceHistory:
		return 3
	case SourceWeb:
		return 4
	}
	return 9
}

// Valid reports whether s is a known source.
func (s DiscoverySource) Valid() bool { return s.rank() < 9 }

// Structured reports whether s is a registry that lists providers, as
// opposed to the open web or Algebra's own records.
func (s DiscoverySource) Structured() bool { return s.rank() == 2 }

// Trust is how much Algebra knows about a candidate's provider, which is not
// the same as how good it is: an observed provider may be known to be bad.
type Trust string

const (
	// TrustNative: found by a native adapter or pinned by the operator.
	TrustNative Trust = "native"
	// TrustObserved: Algebra has executed it often enough to have a record.
	TrustObserved Trust = "observed"
	// TrustListed: appears in a structured registry; never executed here.
	TrustListed Trust = "listed"
	// TrustUnverified: found only on the open web; never executed here.
	TrustUnverified Trust = "unverified"
)

// ObservedMinCalls is how many executions turn a provider from listed or
// unverified into observed.
const ObservedMinCalls = 5

// History is Algebra's own record of a provider for one capability, built
// from execution telemetry. It is attached by the router; discovery never
// sets it, and a provider's marketing never counts as history.
type History struct {
	Calls int `json:"calls"`
	// SuccessRate is the share of attempts that ended in a verified delivery.
	SuccessRate float64 `json:"success_rate"`
	// ValidRate is the share of deliveries that passed schema validation.
	ValidRate    float64   `json:"valid_rate"`
	P50LatencyMS int       `json:"p50_latency_ms"`
	P95LatencyMS int       `json:"p95_latency_ms"`
	AvgQuality   float64   `json:"avg_quality"` // 0-100
	AvgCostMinor int64     `json:"avg_cost_minor"`
	LastCallAt   time.Time `json:"last_call_at,omitzero"`
}

// Validate refuses statistics that can't be real.
func (h History) Validate() error {
	switch {
	case h.Calls < 0:
		return errors.New("history calls can't be negative")
	case !unit(h.SuccessRate) || !unit(h.ValidRate):
		return errors.New("history rates must be between 0 and 1")
	case math.IsNaN(h.AvgQuality) || h.AvgQuality < 0 || h.AvgQuality > 100:
		return errors.New("history quality must be between 0 and 100")
	case h.P50LatencyMS < 0 || h.P95LatencyMS < h.P50LatencyMS:
		return errors.New("history latency must be non-negative with p95 no lower than p50")
	case h.AvgCostMinor < 0:
		return errors.New("history cost can't be negative")
	}
	return nil
}

func unit(v float64) bool { return !math.IsNaN(v) && v >= 0 && v <= 1 }

// Candidate is one provider that claims to do a capability, in the one shape
// every discovery source is normalised into. A registry entry, an x402
// endpoint found on the open web and a native swap adapter all become
// Candidates, so one router can rank them against each other.
//
// A Candidate holds what is advertised or known before anyone is asked to
// price a specific input. What it would cost right now is a Quote.
type Candidate struct {
	// ID is derived from what the candidate is (see Normalize), never taken
	// from the source, so the same offer found twice merges.
	ID         string `json:"id"`
	Capability string `json:"capability"`
	// Provider is the stable key policy and receipts use ("birdeye", or the
	// host for something found on the web).
	Provider      string        `json:"provider"`
	Name          string        `json:"name,omitempty"`
	ExecutionType ExecutionType `json:"execution_type"`
	Endpoint      string        `json:"endpoint,omitempty"`
	Method        string        `json:"method,omitempty"`

	// Advertised terms. Zero means "not stated".
	PriceMinor   int64  `json:"price_minor,omitempty"`
	Asset        string `json:"asset,omitempty"`
	AssetAddress string `json:"asset_address,omitempty"`
	Network      string `json:"network,omitempty"`

	EstimatedLatencyMS int `json:"estimated_latency_ms,omitempty"`
	// Trade-shaped estimates: output in atoms of the output asset, as a
	// decimal string so it can't overflow or lose precision.
	EstimatedOutput   string `json:"estimated_output,omitempty"`
	EstimatedFeeMinor int64  `json:"estimated_fee_minor,omitempty"`
	SlippageBps       int    `json:"slippage_bps,omitempty"`

	// History is attached by the router from execution telemetry.
	History *History `json:"history,omitempty"`

	// Sources lists every source that found this candidate, most trusted
	// first. SourceRef points at the entry in the primary source.
	Sources      []DiscoverySource `json:"discovery_sources"`
	SourceRef    string            `json:"discovery_ref,omitempty"`
	DiscoveredAt time.Time         `json:"discovered_at,omitzero"`

	// PaymentRequirements is the provider's own requirements, verbatim
	// (for x402, one entry of a 402 response's "accepts").
	PaymentRequirements json.RawMessage `json:"payment_requirements,omitempty"`
}

const (
	maxEndpoint  = 2048
	maxReqsBytes = 16 << 10
)

var (
	httpMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}
	atomsRE     = regexp.MustCompile(`^[0-9]{1,40}$`)
)

// Normalize validates a candidate and puts it in canonical form, including
// its deterministic ID: a hash of capability, provider, execution type,
// network, method and endpoint. Two sources that list the same offer
// therefore produce the same ID and can be merged.
func (c Candidate) Normalize() (Candidate, error) {
	var err error
	if c.Capability, err = econ.NormalizeCapability(c.Capability); err != nil {
		return c, err
	}
	if c.Provider, err = econ.NormalizeProvider(c.Provider); err != nil {
		return c, err
	}
	if c.Name = strings.TrimSpace(c.Name); len(c.Name) > 120 {
		return c, errors.New("candidate name is longer than 120 characters")
	}
	c.ExecutionType = ExecutionType(strings.ToLower(strings.TrimSpace(string(c.ExecutionType))))
	if !c.ExecutionType.Valid() {
		return c, fmt.Errorf("execution type must be x402, mpp, solana_swap, http_api or mcp, not %q", c.ExecutionType)
	}
	if c.Endpoint, err = normalizeEndpoint(c.Endpoint); err != nil {
		return c, err
	}
	if c.Endpoint == "" && c.ExecutionType.needsEndpoint() {
		return c, fmt.Errorf("a %s candidate needs an endpoint", c.ExecutionType)
	}
	if c.Method = strings.ToUpper(strings.TrimSpace(c.Method)); c.Method != "" && !slices.Contains(httpMethods, c.Method) {
		return c, fmt.Errorf("method must be one of %s, not %q", strings.Join(httpMethods, ", "), c.Method)
	}

	c.Asset = chain.NormalizeAsset(c.Asset)
	c.AssetAddress = strings.TrimSpace(c.AssetAddress)
	c.Network = chain.NormalizeNetwork(c.Network)
	if len(c.Network) > 64 || strings.ContainsAny(c.Network, " \t\r\n") {
		return c, errors.New("network isn't a valid name")
	}
	if len(c.Asset) > 16 || len(c.AssetAddress) > 128 {
		return c, errors.New("asset or asset address is too long")
	}
	if c.PriceMinor < 0 {
		return c, errors.New("price can't be negative")
	}
	if c.PriceMinor > 0 && c.Asset == "" {
		return c, errors.New("a price needs the asset it is in")
	}
	// A token that calls itself USDC but lives at another address is a
	// different token. Refuse it here, before any rail is asked to pay it.
	if want, ok := chain.AssetAddress(c.Network, c.Asset); ok && c.AssetAddress != "" && !chain.SameAddress(c.Network, want, c.AssetAddress) {
		return c, fmt.Errorf("%s at %s on %s is not the real %s (%s): refusing a look-alike token", c.Asset, c.AssetAddress, c.Network, c.Asset, want)
	}

	if c.EstimatedLatencyMS < 0 || c.EstimatedFeeMinor < 0 {
		return c, errors.New("estimated latency and fee can't be negative")
	}
	if c.SlippageBps < 0 || c.SlippageBps > 10_000 {
		return c, errors.New("slippage is basis points between 0 and 10000")
	}
	if c.EstimatedOutput != "" && !atomsRE.MatchString(c.EstimatedOutput) {
		return c, errors.New("estimated output must be a whole number of atoms")
	}
	if c.History != nil {
		if err := c.History.Validate(); err != nil {
			return c, err
		}
		h := *c.History
		c.History = &h
	}

	if len(c.Sources) == 0 {
		return c, errors.New("a candidate must say where it was found")
	}
	srcs := make([]DiscoverySource, 0, len(c.Sources))
	for _, s := range c.Sources {
		s = DiscoverySource(strings.ToLower(strings.TrimSpace(string(s))))
		if !s.Valid() {
			return c, fmt.Errorf("unknown discovery source %q", s)
		}
		if !slices.Contains(srcs, s) {
			srcs = append(srcs, s)
		}
	}
	c.Sources = sortSources(srcs)
	if c.SourceRef = strings.TrimSpace(c.SourceRef); len(c.SourceRef) > maxEndpoint {
		return c, errors.New("discovery reference is too long")
	}

	if len(c.PaymentRequirements) > 0 {
		if len(c.PaymentRequirements) > maxReqsBytes {
			return c, fmt.Errorf("payment requirements are larger than %d KiB", maxReqsBytes>>10)
		}
		if !json.Valid(c.PaymentRequirements) {
			return c, errors.New("payment requirements are not valid JSON")
		}
		c.PaymentRequirements = slices.Clone(c.PaymentRequirements)
	}

	c.ID = c.computeID()
	return c, nil
}

func sortSources(s []DiscoverySource) []DiscoverySource {
	slices.SortFunc(s, func(a, b DiscoverySource) int {
		return cmp.Or(cmp.Compare(a.rank(), b.rank()), strings.Compare(string(a), string(b)))
	})
	return s
}

func (c Candidate) computeID() string {
	key := strings.Join([]string{c.Capability, c.Provider, string(c.ExecutionType), c.Network, c.Method, c.Endpoint}, "\x00")
	sum := sha256.Sum256([]byte(key))
	return "cand_" + hex.EncodeToString(sum[:8])
}

// Trust is how much Algebra knows about this candidate's provider.
func (c Candidate) Trust() Trust {
	for _, s := range c.Sources {
		if s == SourceNative || s == SourceConfigured {
			return TrustNative
		}
	}
	if c.History != nil && c.History.Calls >= ObservedMinCalls {
		return TrustObserved
	}
	for _, s := range c.Sources {
		if s.Structured() {
			return TrustListed
		}
	}
	return TrustUnverified
}

// normalizeEndpoint is a syntax and hygiene check, not an SSRF defence: it
// keeps credentials, odd schemes and literal private addresses out of
// candidates, but a hostname can still resolve to a private address, so the
// code that actually calls an endpoint must validate the resolved IP at
// connection time.
func normalizeEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if len(raw) > maxEndpoint {
		return "", fmt.Errorf("endpoint is longer than %d characters", maxEndpoint)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Opaque != "" {
		return "", errors.New("endpoint isn't an absolute URL")
	}
	if u.User != nil {
		return "", errors.New("endpoint must not carry credentials")
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", errors.New("endpoint has no host")
	}
	switch scheme {
	case "https":
	case "http":
		if !isLoopbackHost(host) {
			return "", errors.New("endpoint must be https (plain http is accepted only for localhost)")
		}
	default:
		return "", fmt.Errorf("endpoint scheme must be https, not %q", u.Scheme)
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		addr = addr.Unmap()
		if !addr.IsLoopback() && (addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsUnspecified() || addr.IsMulticast()) {
			return "", errors.New("endpoint is a private or link-local address")
		}
	} else if strings.Trim(host, "0123456789.") == "" || strings.HasPrefix(host, "0x") {
		return "", errors.New("endpoint host looks like a numeric IP in a disguised form")
	}

	u.Scheme = scheme
	port := u.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	switch {
	case port != "":
		u.Host = net.JoinHostPort(host, port)
	case strings.Contains(host, ":"):
		u.Host = "[" + host + "]"
	default:
		u.Host = host
	}
	u.Fragment, u.RawFragment = "", ""
	if u.Path == "/" {
		u.Path = ""
	} else if len(u.Path) > 1 {
		u.Path = strings.TrimRight(u.Path, "/")
		u.RawPath = ""
	}
	return u.String(), nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.Unmap().IsLoopback()
}

// Dropped is a candidate that couldn't be normalised, and why. Discovery from
// the open web is noisy; one bad entry never sinks the rest.
type Dropped struct {
	Provider string `json:"provider,omitempty"`
	Reason   string `json:"reason"`
}

// Dedupe normalises every candidate, drops the ones that fail (reporting
// why) and merges those that name the same offer. The result is ordered by
// ID so the same input gives the same output however it arrived.
func Dedupe(in []Candidate) ([]Candidate, []Dropped) {
	groups := make(map[string][]Candidate, len(in))
	var dropped []Dropped
	for _, raw := range in {
		c, err := raw.Normalize()
		if err != nil {
			dropped = append(dropped, Dropped{Provider: raw.Provider, Reason: err.Error()})
			continue
		}
		groups[c.ID] = append(groups[c.ID], c)
	}
	out := make([]Candidate, 0, len(groups))
	for _, g := range groups {
		// Best-sourced first, by a total order, so the winner and the order
		// gaps are filled in never depend on the order sources answered.
		slices.SortFunc(g, compareCandidates)
		merged := g[0]
		for _, c := range g[1:] {
			merged = mergeInto(merged, c)
		}
		out = append(out, merged)
	}
	slices.SortFunc(out, func(a, b Candidate) int { return strings.Compare(a.ID, b.ID) })
	return out, dropped
}

// compareCandidates orders candidates with the same ID, best-sourced first:
// by source trust, then source name, then registry reference, then (to make
// the order total) their JSON.
func compareCandidates(a, b Candidate) int {
	if c := cmp.Compare(a.Sources[0].rank(), b.Sources[0].rank()); c != 0 {
		return c
	}
	if c := strings.Compare(string(a.Sources[0]), string(b.Sources[0])); c != 0 {
		return c
	}
	if c := strings.Compare(a.SourceRef, b.SourceRef); c != 0 {
		return c
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return bytes.Compare(ja, jb)
}

// mergeInto folds b into a, the better-sourced candidate: a wins every
// conflict and b only fills gaps. A price travels with its asset; they are
// never mixed from two sources.
func mergeInto(a, b Candidate) Candidate {
	out := a
	out.Sources = slices.Compact(sortSources(slices.Concat(a.Sources, b.Sources)))
	if out.Name == "" {
		out.Name = b.Name
	}
	if out.PriceMinor == 0 && b.PriceMinor > 0 {
		out.PriceMinor, out.Asset, out.AssetAddress = b.PriceMinor, b.Asset, b.AssetAddress
	}
	if out.AssetAddress == "" && out.Asset == b.Asset {
		out.AssetAddress = b.AssetAddress
	}
	if out.EstimatedLatencyMS == 0 {
		out.EstimatedLatencyMS = b.EstimatedLatencyMS
	}
	if out.EstimatedOutput == "" {
		out.EstimatedOutput = b.EstimatedOutput
	}
	if out.EstimatedFeeMinor == 0 {
		out.EstimatedFeeMinor = b.EstimatedFeeMinor
	}
	if out.SlippageBps == 0 {
		out.SlippageBps = b.SlippageBps
	}
	if b.History != nil && (out.History == nil || b.History.Calls > out.History.Calls) {
		h := *b.History
		out.History = &h
	}
	if out.SourceRef == "" {
		out.SourceRef = b.SourceRef
	}
	if out.DiscoveredAt.IsZero() || (!b.DiscoveredAt.IsZero() && b.DiscoveredAt.Before(out.DiscoveredAt)) {
		out.DiscoveredAt = b.DiscoveredAt
	}
	if len(out.PaymentRequirements) == 0 {
		out.PaymentRequirements = slices.Clone(b.PaymentRequirements)
	}
	return out
}
