// Package tenancy is Algebra's B2B tenancy model: organizations, the people in
// them, their projects, and the API keys applications use.
//
// Two authorities stay separate on purpose. An API key authenticates an
// application inside one project and one environment; it never authorises
// spending on its own. A Spend Pass of the same project and environment does
// that, and the economic coordinator checks the pass on every payment.
package tenancy

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/chain"
)

// Environment is where a key or a pass may move money.
type Environment string

const (
	// EnvSandbox is simulated money on Algebra's sandbox rail: no chain.
	EnvSandbox Environment = "sandbox"
	// EnvDevnet is real transactions on Solana devnet, with test tokens.
	EnvDevnet Environment = "devnet"
	// EnvMainnet is real money on Solana mainnet.
	EnvMainnet Environment = "mainnet"
)

// Environments in the order the console shows them.
var Environments = []Environment{EnvSandbox, EnvDevnet, EnvMainnet}

// Valid reports whether e is a known environment.
func (e Environment) Valid() bool { return slices.Contains(Environments, e) }

// Networks are the payment networks e may pay on. A sandbox key can never
// reach a chain, and a devnet key can never reach mainnet.
func (e Environment) Networks() []string {
	switch e {
	case EnvSandbox:
		return []string{chain.Sandbox}
	case EnvDevnet:
		// Testnet: every test network Algebra can pay on. Base Sepolia and Arc
		// testnet are paid only while their rail is ready (chain.Payable).
		return []string{chain.SolanaDevnet, chain.BaseSepolia, chain.ArcTestnet}
	case EnvMainnet:
		return []string{chain.Solana}
	}
	return nil
}

// EnvironmentOfNetwork is the environment a payment network belongs to.
func EnvironmentOfNetwork(network string) (Environment, bool) {
	switch chain.NormalizeNetwork(network) {
	case chain.Sandbox:
		return EnvSandbox, true
	case chain.SolanaDevnet, chain.BaseSepolia, chain.ArcTestnet:
		return EnvDevnet, true
	case chain.Solana:
		return EnvMainnet, true
	}
	return "", false
}

// Role is what a member may do in an organization.
type Role string

const (
	RoleOwner     Role = "owner"
	RoleAdmin     Role = "admin"
	RoleDeveloper Role = "developer"
	RoleViewer    Role = "viewer"
)

// CanManage: create projects, issue and revoke keys, change policy.
func (r Role) CanManage() bool { return r == RoleOwner || r == RoleAdmin }

// CanRead: see the organization's projects, keys (never their secrets) and usage.
func (r Role) CanRead() bool {
	return r == RoleOwner || r == RoleAdmin || r == RoleDeveloper || r == RoleViewer
}

// Organization is a customer: the root of everything it owns.
type Organization struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	OwnerUserID string     `json:"-"`
	Plan        string     `json:"plan"`
	TrialPlan   string     `json:"trial_plan,omitempty"`
	TrialEndsAt *time.Time `json:"trial_ends_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// Member is a person's place in an organization.
type Member struct {
	OrgID     string    `json:"org_id"`
	UserID    string    `json:"user_id"`
	Role      Role      `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

// Strategies a project may default to. Empty means: ask the caller when the
// choice changes the outcome.
var Strategies = []string{"", "cheapest", "fastest", "balanced"}

// Project groups an application's keys, passes, executions and usage.
type Project struct {
	ID              string     `json:"id"`
	OrgID           string     `json:"org_id"`
	Name            string     `json:"name"`
	DefaultStrategy string     `json:"default_strategy"`
	CreatedAt       time.Time  `json:"created_at"`
	ArchivedAt      *time.Time `json:"archived_at,omitempty"`
}

// Scope is what an API key may do.
type Scope string

const (
	// ScopeExecute: run paid requests (still only within a Spend Pass).
	ScopeExecute Scope = "execute"
	// ScopeSimulate: dry runs that pay nothing.
	ScopeSimulate Scope = "simulate"
	// ScopeRead: discover providers, read executions and receipts.
	ScopeRead Scope = "read"
)

// AllScopes is every scope, in display order.
var AllScopes = []Scope{ScopeRead, ScopeSimulate, ScopeExecute}

// APIKey is an application's credential for one project and environment. The
// secret is never stored: the agent row it signs in as holds its SHA-256 hash.
type APIKey struct {
	ID          string      `json:"id"`
	ProjectID   string      `json:"project_id"`
	Environment Environment `json:"environment"`
	Name        string      `json:"name"`
	// Prefix is the start of the secret, safe to show ("alg_sk_sandbox_Qm3x").
	Prefix      string     `json:"prefix"`
	AgentID     string     `json:"-"`
	Scopes      []Scope    `json:"scopes"`
	CreatedBy   string     `json:"-"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	RotatedFrom string     `json:"rotated_from,omitempty"`
}

// Active reports whether the key can be used at now.
func (k APIKey) Active(now time.Time) bool {
	return k.RevokedAt == nil && (k.ExpiresAt == nil || now.Before(*k.ExpiresAt))
}

// Has reports whether the key carries scope s.
func (k APIKey) Has(s Scope) bool { return slices.Contains(k.Scopes, s) }

// KeyPrefix starts every Algebra API key, so a leaked one is recognisable.
const KeyPrefix = "alg_sk_"

// GenerateKey returns a new secret for env (shown once), its hash (what is
// stored) and its displayable prefix. 32 bytes from the CSPRNG.
func GenerateKey(env Environment) (raw, hash, prefix string, err error) {
	if !env.Valid() {
		return "", "", "", fmt.Errorf("tenancy: unknown environment %q", env)
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", "", fmt.Errorf("tenancy: generating key: %w", err)
	}
	head := KeyPrefix + string(env) + "_"
	raw = head + base64.RawURLEncoding.EncodeToString(buf)
	return raw, agent.HashToken(raw), raw[:len(head)+4], nil
}

// IsAPIKey reports whether a bearer token has the shape of an Algebra API key.
func IsAPIKey(token string) bool { return strings.HasPrefix(token, KeyPrefix) }

// ErrInvalid is a request that names something malformed.
var ErrInvalid = errors.New("tenancy: invalid")

// CleanName trims a name and checks its length.
func CleanName(what, s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || len([]rune(s)) > 80 {
		return "", fmt.Errorf("%w: %s must be 1 to 80 characters", ErrInvalid, what)
	}
	return s, nil
}

// CleanScopes validates scopes, removes duplicates and keeps display order.
// Empty means every scope.
func CleanScopes(in []Scope) ([]Scope, error) {
	if len(in) == 0 {
		return slices.Clone(AllScopes), nil
	}
	out := []Scope{}
	for _, s := range AllScopes {
		if slices.Contains(in, s) {
			out = append(out, s)
		}
	}
	for _, s := range in {
		if !slices.Contains(AllScopes, s) {
			return nil, fmt.Errorf("%w: unknown scope %q", ErrInvalid, s)
		}
	}
	return out, nil
}
