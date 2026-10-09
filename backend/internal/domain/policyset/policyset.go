// Package policyset persists a tenant's policy.Rules with versioning. Today
// a ruleset is either hardcoded (policy.DefaultRules()) or supplied inline
// per request (app.TransactionPolicyService) — neither lets a business set
// its policy once and have it apply to every subsequent AgenticPaymentIntent
// its agents create. The policy package itself (Provider/LocalProvider/
// Rules) is untouched by this — a PolicySet is just storage and versioning
// wrapped around the same policy.Rules struct.
package policyset

import (
	"time"

	"github.com/project-algebra/algebra/policy"
)

// PolicySet is one version of a tenant's policy. A nil UserID means "the
// tenant's default, applies to every end user"; a set UserID overrides that
// default for one specific end user.
type PolicySet struct {
	ID       string
	TenantID string
	UserID   *string
	Rules    policy.Rules
	Version  int

	CreatedAt    time.Time
	SupersededAt *time.Time
}

// IsActive reports whether this is the current version — superseded
// versions are kept (never deleted) so a policy decision made under an
// older version stays explainable.
func (p *PolicySet) IsActive() bool { return p.SupersededAt == nil }
