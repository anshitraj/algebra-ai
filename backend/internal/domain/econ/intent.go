package econ

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/domain/chain"
)

// Intent is one economic outcome a principal wants accomplished. It is not
// a payment request: many executors, providers and rails may attempt it,
// and at most one commitment is released for it.
type Intent struct {
	ID          string `json:"id"`
	PrincipalID string `json:"-"`
	// PassID is the Spend Pass the intent was created under.
	PassID         string `json:"spend_pass_id,omitempty"`
	CreatedByAgent string `json:"created_by_agent,omitempty"`

	Capability string `json:"capability"`
	// InputHash is sha256 over the canonical form of the capability input.
	InputHash string `json:"input_hash"`
	Quantity  int    `json:"quantity"`
	// Window is the validity window the outcome belongs to ("2026-09-29T10",
	// "FY2026-Q2", or "once").
	Window string `json:"window"`
	// EffectKey is the deterministic economic identity:
	// capability:input_hash[:24]:window:quantity.
	EffectKey string `json:"effect_key"`
	// IntentHash commits to every immutable field (see Hash).
	IntentHash string `json:"intent_hash"`

	Currency       string          `json:"currency"`
	BudgetMaxMinor int64           `json:"budget_max_minor"`
	Constraints    Constraints     `json:"constraints"`
	ProviderPolicy ProviderPolicy  `json:"provider_policy"`
	Input          json.RawMessage `json:"input,omitempty"`

	State       State       `json:"state"`
	Commitment  Commitment  `json:"commitment"`
	Fulfillment Fulfillment `json:"fulfillment"`
	// CommittedMinor is what settled for this intent, from evidence.
	CommittedMinor      int64  `json:"committed_minor"`
	ActiveReservationID string `json:"active_reservation_id,omitempty"`
	Attempts            int    `json:"attempts"`
	// BlockedAttempts counts reservations refused because another executor
	// held the intent, it was frozen as UNKNOWN, or it had already
	// committed: duplicate commitments prevented.
	BlockedAttempts int `json:"duplicate_commit_attempts_blocked"`

	RequiresApproval bool       `json:"requires_approval"`
	ApprovedAt       *time.Time `json:"approved_at,omitempty"`

	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Constraints bound how an outcome may be produced: how fresh and fast it
// must be and, for routed outcomes, which providers, networks and assets
// qualify. Every field is optional; zero means no bound beyond the person's
// own policy. They narrow what the person's Spend Pass allows, never widen it.
// Fields added after the first release are omitted when empty, so an intent
// that doesn't use them keeps the hash it always had.
type Constraints struct {
	MaxAgeSeconds int `json:"max_age_seconds,omitempty"`
	MaxLatencyMS  int `json:"max_latency_ms,omitempty"`

	// MaxSlippageBps and MaxPriceImpactBps bound a trade, in basis points
	// (100 = 1%).
	MaxSlippageBps    int `json:"max_slippage_bps,omitempty"`
	MaxPriceImpactBps int `json:"max_price_impact_bps,omitempty"`

	// MinQuality and MinReliabilityPct (0-100) exclude providers whose own
	// record, as Algebra observed it, falls below them.
	MinQuality        int `json:"min_quality,omitempty"`
	MinReliabilityPct int `json:"min_reliability_pct,omitempty"`

	// AllowedNetworks and AllowedAssets, when set, restrict where and in what
	// the outcome may be paid.
	AllowedNetworks []string `json:"allowed_networks,omitempty"`
	AllowedAssets   []string `json:"allowed_assets,omitempty"`
}

// maxListEntries bounds every list a caller can attach to an intent.
const maxListEntries = 16

var (
	networkRE  = regexp.MustCompile(`^[a-z0-9][a-z0-9:._-]{0,63}$`)
	assetRE    = regexp.MustCompile(`^[A-Z0-9][A-Z0-9._-]{0,15}$`)
	providerRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._:-]{0,95}$`)
)

// Normalize validates the bounds and puts the lists in canonical form
// (canonical network names, upper-case assets, sorted, no duplicates) so two
// spellings of the same constraints produce the same intent hash.
func (c Constraints) Normalize() (Constraints, error) {
	if c.MaxAgeSeconds < 0 || c.MaxLatencyMS < 0 {
		return c, errors.New("constraints can't be negative")
	}
	if c.MaxSlippageBps < 0 || c.MaxSlippageBps > 10_000 || c.MaxPriceImpactBps < 0 || c.MaxPriceImpactBps > 10_000 {
		return c, errors.New("constraints slippage and price impact are basis points between 0 and 10000")
	}
	if c.MinQuality < 0 || c.MinQuality > 100 || c.MinReliabilityPct < 0 || c.MinReliabilityPct > 100 {
		return c, errors.New("constraints minimum quality and reliability are percentages between 0 and 100")
	}
	var err error
	if c.AllowedNetworks, err = canonList(c.AllowedNetworks, chain.NormalizeNetwork, networkRE, true); err != nil {
		return c, fmt.Errorf("constraints allowed networks: %w", err)
	}
	if c.AllowedAssets, err = canonList(c.AllowedAssets, chain.NormalizeAsset, assetRE, true); err != nil {
		return c, fmt.Errorf("constraints allowed assets: %w", err)
	}
	return c, nil
}

func lowerTrim(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// NormalizeProvider lower-cases and validates a provider ID: the one name an
// intent's allow-list, a reservation, a Spend Pass and a receipt all use for
// the same provider.
func NormalizeProvider(id string) (string, error) {
	id = lowerTrim(id)
	if !providerRE.MatchString(id) {
		return "", errors.New("provider must be 1-96 lowercase letters, digits, dots, colons, dashes or underscores, e.g. \"birdeye\" or \"x402:api.example.com\"")
	}
	return id, nil
}

// canonList normalises each entry, drops blanks and duplicates and checks the
// rest against re. Sorted lists compare and hash the same however they were
// typed; ordered lists (a ranking) keep their order.
func canonList(in []string, norm func(string) string, re *regexp.Regexp, sorted bool) ([]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = norm(v)
		if v == "" || slices.Contains(out, v) {
			continue
		}
		if !re.MatchString(v) {
			return nil, fmt.Errorf("%q isn't a valid name", v)
		}
		out = append(out, v)
	}
	if len(out) > maxListEntries {
		return nil, fmt.Errorf("at most %d entries are allowed", maxListEntries)
	}
	if sorted {
		slices.Sort(out)
	}
	return out, nil
}

// Provider selection strategies. An empty Strategy means StrategyAuto.
const (
	// StrategyAuto weighs quality, reliability, cost, speed and policy fit.
	StrategyAuto = "auto"
	// StrategyCheapest minimises total expected cost.
	StrategyCheapest = "cheapest"
	// StrategyFastest minimises expected time to a verified result.
	StrategyFastest = "fastest"
	// StrategyFixed limits execution to ProviderPolicy.Providers.
	StrategyFixed = "fixed"
	// StrategyBestExecution is the original name for StrategyAuto.
	StrategyBestExecution = "best_execution"
)

// ProviderPolicy steers provider selection. Algebra doesn't claim to have
// invented routing; this records what the principal asked for.
type ProviderPolicy struct {
	// Strategy is one of the Strategy constants.
	Strategy string `json:"strategy,omitempty"`
	// Providers, when set, is an allow-list: a reservation that names any
	// other provider is refused.
	Providers []string `json:"providers,omitempty"`
}

// Normalize validates the strategy (the old name best_execution becomes
// auto) and cleans the provider list, keeping its order.
func (p ProviderPolicy) Normalize() (ProviderPolicy, error) {
	p.Strategy = strings.ToLower(strings.TrimSpace(p.Strategy))
	switch p.Strategy {
	case "", StrategyAuto, StrategyCheapest, StrategyFastest, StrategyFixed:
	case StrategyBestExecution:
		p.Strategy = StrategyAuto
	default:
		return p, fmt.Errorf("provider policy strategy must be auto, cheapest, fastest or fixed, not %q", p.Strategy)
	}
	var err error
	if p.Providers, err = canonList(p.Providers, lowerTrim, providerRE, false); err != nil {
		return p, fmt.Errorf("provider policy providers: %w", err)
	}
	if p.Strategy == StrategyFixed && len(p.Providers) == 0 {
		return p, errors.New("provider policy strategy \"fixed\" needs at least one provider")
	}
	return p, nil
}

// Spec is what a caller asks for when creating an intent.
type Spec struct {
	Capability     string
	Input          json.RawMessage
	Quantity       int
	Window         string
	Currency       string
	BudgetMaxMinor int64
	Constraints    Constraints
	ProviderPolicy ProviderPolicy
	TTL            time.Duration
}

var (
	capabilityRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,63}$`)
	windowRE     = regexp.MustCompile(`^[A-Za-z0-9:._-]{1,64}$`)
)

// NormalizeCapability lower-cases and validates a capability ID, the
// vocabulary shared by intents, providers and the router.
func NormalizeCapability(id string) (string, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	if !capabilityRE.MatchString(id) {
		return "", errors.New("capability must be 2-64 lowercase letters, digits, dots, dashes or underscores, e.g. \"solana.token-risk\"")
	}
	return id, nil
}

// MaxTTL bounds how long an intent stays open; MinTTL keeps the
// second-precision deadline from landing in the past.
const (
	MaxTTL = 7 * 24 * time.Hour
	MinTTL = 10 * time.Second
)

// New validates a Spec and builds an OPEN intent with its deterministic
// identity. Approval, if needed, is applied by the caller.
func New(id, principalID, passID, agentID string, s Spec, now time.Time) (*Intent, error) {
	capability, err := NormalizeCapability(s.Capability)
	if err != nil {
		return nil, err
	}
	s.Capability = capability
	if s.Quantity == 0 {
		s.Quantity = 1
	}
	if s.Quantity < 1 || s.Quantity > 1_000_000 {
		return nil, errors.New("quantity must be between 1 and 1,000,000")
	}
	s.Window = strings.TrimSpace(s.Window)
	if s.Window == "" {
		s.Window = "once"
	}
	if !windowRE.MatchString(s.Window) {
		return nil, errors.New("window must be up to 64 letters, digits or : . _ -, e.g. \"2026-09-29T10\"")
	}
	s.Currency = strings.ToUpper(strings.TrimSpace(s.Currency))
	if s.Currency == "" {
		s.Currency = "USDC"
	}
	if s.BudgetMaxMinor <= 0 {
		return nil, errors.New("budget maximum must be more than zero")
	}
	if s.TTL <= 0 {
		s.TTL = time.Hour
	}
	if s.TTL > MaxTTL {
		return nil, errors.New("an intent can stay open at most 7 days")
	}
	if s.TTL < MinTTL {
		return nil, errors.New("an intent must stay open at least 10 seconds")
	}
	if s.Constraints, err = s.Constraints.Normalize(); err != nil {
		return nil, err
	}
	if s.ProviderPolicy, err = s.ProviderPolicy.Normalize(); err != nil {
		return nil, err
	}
	canon, err := Canonicalize(s.Input)
	if err != nil {
		return nil, fmt.Errorf("input: %w", err)
	}
	inputHash := sha256Hex(canon)
	in := &Intent{
		ID: id, PrincipalID: principalID, PassID: passID, CreatedByAgent: agentID,
		Capability: s.Capability, InputHash: inputHash, Quantity: s.Quantity, Window: s.Window,
		EffectKey: EffectKey(s.Capability, inputHash, s.Window, s.Quantity),
		Currency:  s.Currency, BudgetMaxMinor: s.BudgetMaxMinor,
		Constraints: s.Constraints, ProviderPolicy: s.ProviderPolicy, Input: canon,
		State: StateOpen, Commitment: CommitmentNone, Fulfillment: FulfillmentNone,
		Version: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC(), ExpiresAt: now.Add(s.TTL).UTC().Truncate(time.Second),
	}
	in.IntentHash = in.Hash()
	return in, nil
}

// Transition moves the intent's lifecycle state, or refuses an illegal move.
// Version is the store's: every save is a compare-and-swap on it.
func (in *Intent) Transition(to State, now time.Time) error {
	if !CanTransition(in.State, to) {
		return &ErrIllegalTransition{Kind: "intent", From: string(in.State), To: string(to)}
	}
	in.State = to
	in.UpdatedAt = now.UTC()
	return nil
}

// Expired reports whether the intent's own deadline has passed.
func (in *Intent) Expired(now time.Time) bool { return !now.Before(in.ExpiresAt) }
