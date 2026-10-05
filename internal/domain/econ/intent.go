package econ

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
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

// Constraints are the outcome's freshness and latency bounds.
type Constraints struct {
	MaxAgeSeconds int `json:"max_age_seconds,omitempty"`
	MaxLatencyMS  int `json:"max_latency_ms,omitempty"`
}

// ProviderPolicy steers provider selection. Algebra doesn't claim to have
// invented routing; this records what the principal asked for.
type ProviderPolicy struct {
	Strategy  string   `json:"strategy,omitempty"` // "best_execution" | "cheapest" | "fixed"
	Providers []string `json:"providers,omitempty"`
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

// MaxTTL bounds how long an intent stays open; MinTTL keeps the
// second-precision deadline from landing in the past.
const (
	MaxTTL = 7 * 24 * time.Hour
	MinTTL = 10 * time.Second
)

// New validates a Spec and builds an OPEN intent with its deterministic
// identity. Approval, if needed, is applied by the caller.
func New(id, principalID, passID, agentID string, s Spec, now time.Time) (*Intent, error) {
	s.Capability = strings.ToLower(strings.TrimSpace(s.Capability))
	if !capabilityRE.MatchString(s.Capability) {
		return nil, errors.New("capability must be 2-64 lowercase letters, digits, dots, dashes or underscores, e.g. \"solana.token-risk\"")
	}
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
	if s.Constraints.MaxAgeSeconds < 0 || s.Constraints.MaxLatencyMS < 0 {
		return nil, errors.New("constraints can't be negative")
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
