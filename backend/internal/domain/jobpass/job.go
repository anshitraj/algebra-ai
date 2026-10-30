// Package jobpass defines the Job Pass: a temporary budget for one task, set
// aside from a Spend Pass and shared by every agent that works on the task.
//
// The hierarchy is Vault → Spend Pass → Job Pass. The Vault holds the money;
// a Spend Pass is an agent's standing permission and budget; a Job Pass is a
// slice of that budget for one job, which the agent can hand to the
// sub-agents it delegates to (each holding the job's token), and which they
// may slice again for their own sub-tasks.
//
// A Job Pass is an allocation in Algebra's ledger, never separate money: no
// wallet, no deposit, nothing on chain. Creating one sets its budget aside
// from the parent at once, all or nothing, so a task never starts with money
// that another task may take. Closing it, revoking it or letting it expire
// returns what it didn't use to the parent. Every payment under it still has
// to clear the Spend Pass's own rules: a Job Pass narrows, it never widens.
package jobpass

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// State is a job's lifecycle state. Only ACTIVE authorizes anything, and only
// before ExpiresAt; the others are final.
type State string

const (
	Active State = "ACTIVE"
	// Closed: the task finished (or its agent gave the rest back).
	Closed State = "CLOSED"
	// Revoked: the person stopped it.
	Revoked State = "REVOKED"
	// Expired: its time ran out. A job past ExpiresAt is treated as expired
	// whether or not the sweeper has written it yet.
	Expired State = "EXPIRED"
)

// Reason codes a job adds to a refusal.
const (
	ReasonBudgetExceeded = "JOB_BUDGET_EXCEEDED"
	ReasonClosed         = "JOB_CLOSED"
	ReasonRevoked        = "JOB_REVOKED"
	ReasonExpired        = "JOB_EXPIRED"
	// ReasonParentShort: the parent (a Spend Pass or a job) hasn't that much
	// left to set aside.
	ReasonParentShort = "JOB_PARENT_SHORT"
)

// Limits.
const (
	// MaxDepth bounds delegation: a job, a sub-agent's job inside it, and one
	// more level.
	MaxDepth = 3
	// MaxActivePerPass bounds how many jobs a pass has open at once.
	MaxActivePerPass = 100
	// MinLifetime, MaxLifetime and DefaultLifetime: a job is for a task, so it
	// is short-lived; a standing budget is what a Spend Pass is for.
	MinLifetime     = time.Minute
	MaxLifetime     = 24 * time.Hour
	DefaultLifetime = time.Hour
)

// TokenPrefix marks a job token, so it can't be mistaken for an agent's.
const TokenPrefix = "ajob_"

// Job is one task's budget.
type Job struct {
	ID     string `json:"id"`
	UserID string `json:"-"`
	// PassID is the Spend Pass every payment under the job is made under,
	// at every depth.
	PassID string `json:"spend_pass_id"`
	// ParentID is the job this one was carved from; empty for a job set aside
	// directly from the pass.
	ParentID string `json:"parent_id,omitempty"`
	Depth    int    `json:"depth"`
	Label    string `json:"label"`
	Currency string `json:"currency"`
	// BudgetMinor is set aside from the parent for the job's whole life.
	BudgetMinor int64 `json:"budget_minor"`
	// ValueMinor is what the task is worth, as the agent or the person stated
	// it: the other side of its P&L. Nil when nobody said.
	ValueMinor *int64 `json:"value_minor,omitempty"`
	State      State  `json:"state"`
	// CreatedBy is the agent that opened the job, or "" for the person.
	CreatedBy   string     `json:"created_by,omitempty"`
	TokenHash   string     `json:"-"`
	CloseReason string     `json:"close_reason,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	ClosedAt    *time.Time `json:"closed_at,omitempty"`
}

// Live reports whether the job can authorize a payment at now.
func (j Job) Live(now time.Time) bool { return j.State == Active && now.Before(j.ExpiresAt) }

// StateAt is the job's state as it stands at now: an ACTIVE job past its
// expiry reads as EXPIRED.
func (j Job) StateAt(now time.Time) State {
	if j.State == Active && !now.Before(j.ExpiresAt) {
		return Expired
	}
	return j.State
}

// Refusal is the reason code for a job that can't authorize anything at now,
// or "" for a live one.
func (j Job) Refusal(now time.Time) string {
	switch j.StateAt(now) {
	case Active:
		return ""
	case Revoked:
		return ReasonRevoked
	case Expired:
		return ReasonExpired
	default:
		return ReasonClosed
	}
}

// Spec is what opening a job asks for.
type Spec struct {
	Label       string        `json:"label"`
	BudgetMinor int64         `json:"budget_minor"`
	Lifetime    time.Duration `json:"-"`
	// ValueMinor: what the task is worth, if the opener says.
	ValueMinor *int64 `json:"value_minor,omitempty"`
}

// MaxValueMinor bounds a stated value: 10 million USDC, a typo guard.
const MaxValueMinor = 10_000_000 * 1_000_000

// CheckValue refuses a value that can't be one.
func CheckValue(v int64) error {
	if v < 0 || v > MaxValueMinor {
		return errors.New("a task's value is between 0 and 10,000,000 USDC")
	}
	return nil
}

// Normalize cleans a spec and reports what's wrong with it.
func (s Spec) Normalize() (Spec, error) {
	s.Label = strings.Join(strings.Fields(s.Label), " ")
	if s.Label == "" {
		return s, errors.New("name the job: what the task is, in a few words")
	}
	if utf8.RuneCountInString(s.Label) > 80 {
		s.Label = string([]rune(s.Label)[:79]) + "…"
	}
	// Control and format characters let a name read as something else next to a payment.
	for _, r := range s.Label {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return s, errors.New("the job name can't contain control or direction-changing characters")
		}
	}
	if s.BudgetMinor <= 0 {
		return s, errors.New("a job's budget must be more than zero")
	}
	if s.ValueMinor != nil {
		if err := CheckValue(*s.ValueMinor); err != nil {
			return s, err
		}
	}
	if s.Lifetime == 0 {
		s.Lifetime = DefaultLifetime
	}
	if s.Lifetime < MinLifetime || s.Lifetime > MaxLifetime {
		return s, fmt.Errorf("a job lasts between %s and %s", MinLifetime, MaxLifetime)
	}
	return s, nil
}

// NewToken mints a job token and its hash. The token is shown once, to the
// agent that opened the job, and only its hash is kept.
func NewToken() (token, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	token = TokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token), nil
}

// HashToken is the stored form of a job token.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// IsToken reports whether a bearer credential is a job token.
func IsToken(s string) bool { return strings.HasPrefix(s, TokenPrefix) }
