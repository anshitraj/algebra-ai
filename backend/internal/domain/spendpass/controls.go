package spendpass

import (
	"fmt"
	"strings"
)

// Reason codes the controls add to a decision.
const (
	ReasonFrozen                = "PASS_FROZEN"
	ReasonRateLimited           = "PASS_RATE_LIMITED"
	ReasonProviderRateLimited   = "PASS_PROVIDER_RATE_LIMITED"
	ReasonNewProviderOverCap    = "PASS_NEW_PROVIDER_OVER_CAP"
	ReasonNewProviderApproval   = "PASS_NEW_PROVIDER_NEEDS_APPROVAL"
	ReasonNewProviderNotAllowed = "PASS_NEW_PROVIDER_BLOCKED"
	// The unverified-provider rule: the provider's identity hasn't been
	// verified in Algebra's registry (a listing, a quote or a payment that
	// worked never verifies it).
	ReasonUnverifiedNeedsApproval = "PASS_UNVERIFIED_PROVIDER_NEEDS_APPROVAL"
	ReasonUnverifiedNotAllowed    = "PASS_UNVERIFIED_PROVIDER_BLOCKED"
	// The web-found rule: an endpoint the agent found on the open web, which no
	// catalog lists and nothing here has executed.
	ReasonWebFoundNeedsApproval = "PASS_WEB_FOUND_NEEDS_APPROVAL"
	ReasonWebFoundNotAllowed    = "PASS_WEB_FOUND_BLOCKED"
)

// WebFoundRule is how a pass treats an endpoint its agent found on the open
// web and supplied itself. Nobody has reviewed it: an agent can't vouch for
// its own provider, and a page it read may have been written to be found.
type WebFoundRule string

const (
	// WebFoundApprove (the default): the first payment waits for the person.
	// Once an endpoint has been executed here often enough to have a record
	// it is no longer "found only on the web" and is paid like any other.
	WebFoundApprove WebFoundRule = "approve"
	// WebFoundAllow: a web find is paid like a listed provider.
	WebFoundAllow WebFoundRule = "allow"
	// WebFoundDeny: only providers in a catalog or configured by the operator
	// are paid.
	WebFoundDeny WebFoundRule = "deny"
)

// UnverifiedRule is how a pass treats a provider whose identity no person has
// verified in the provider registry. Almost every listed provider is
// unverified, so "approve" and "deny" are opt-in: they suit a pass that moves
// real money.
type UnverifiedRule string

const (
	// UnverifiedAllow (the default): verification is shown, not required.
	UnverifiedAllow UnverifiedRule = "allow"
	// UnverifiedApprove: paying an unverified provider waits for the person.
	UnverifiedApprove UnverifiedRule = "approve"
	// UnverifiedDeny: only identity-verified providers are paid.
	UnverifiedDeny UnverifiedRule = "deny"
)

// NewProviderRule is how a pass treats a provider its person has never paid.
// The catalogs list thousands of endpoints and anyone can publish one, so the
// first payment to a stranger is where an agent is most easily misled.
type NewProviderRule string

const (
	// NewProvidersAllow: a stranger is paid like anyone else.
	NewProvidersAllow NewProviderRule = "allow"
	// NewProvidersCap (the default): a stranger may be paid, but no more than
	// NewProviderCapMinor per call until it has been paid once.
	NewProvidersCap NewProviderRule = "cap"
	// NewProvidersApprove: paying a stranger waits for the person.
	NewProvidersApprove NewProviderRule = "approve"
)

// Defaults for a pass that doesn't set its own.
const (
	DefaultMaxCallsPerMinute            = 60
	DefaultMaxCallsPerProviderPerMinute = 20
	// DefaultNewProviderCapMinor is $0.05 in micro-USDC. A pass in another
	// currency gets the same number of its minor units.
	DefaultNewProviderCapMinor = 50_000
	maxCallsCeiling            = 10_000
)

// Controls are a pass's limits on how an agent spends, beyond how much.
type Controls struct {
	// MaxCallsPerMinute bounds paid attempts across all providers in any
	// minute: a looping agent is stopped by this long before it reaches its
	// budget. MaxCallsPerProviderPerMinute bounds them per provider.
	MaxCallsPerMinute            int `json:"max_calls_per_minute"`
	MaxCallsPerProviderPerMinute int `json:"max_calls_per_provider_per_minute"`
	// NewProviders and NewProviderCapMinor: see NewProviderRule.
	NewProviders        NewProviderRule `json:"new_providers"`
	NewProviderCapMinor int64           `json:"new_provider_cap_minor_units"`
	// UnverifiedProviders: see UnverifiedRule.
	UnverifiedProviders UnverifiedRule `json:"unverified_providers"`
	// WebFoundProviders: see WebFoundRule.
	WebFoundProviders WebFoundRule `json:"web_found_providers"`
}

// Normalize fills in the defaults and refuses limits that make no sense.
func (c Controls) Normalize(currency string) (Controls, error) {
	c = c.Effective()
	if c.MaxCallsPerMinute < 1 || c.MaxCallsPerMinute > maxCallsCeiling ||
		c.MaxCallsPerProviderPerMinute < 1 || c.MaxCallsPerProviderPerMinute > maxCallsCeiling {
		return c, fmt.Errorf("calls per minute must be between 1 and %d", maxCallsCeiling)
	}
	if c.MaxCallsPerProviderPerMinute > c.MaxCallsPerMinute {
		c.MaxCallsPerProviderPerMinute = c.MaxCallsPerMinute
	}
	switch c.NewProviders {
	case NewProvidersAllow, NewProvidersCap, NewProvidersApprove:
	default:
		return c, fmt.Errorf("new providers must be allow, cap or approve, not %q", c.NewProviders)
	}
	if c.NewProviderCapMinor < 0 {
		return c, fmt.Errorf("the new-provider cap can't be negative")
	}
	switch c.UnverifiedProviders {
	case UnverifiedAllow, UnverifiedApprove, UnverifiedDeny:
	default:
		return c, fmt.Errorf("unverified providers must be allow, approve or deny, not %q", c.UnverifiedProviders)
	}
	switch c.WebFoundProviders {
	case WebFoundAllow, WebFoundApprove, WebFoundDeny:
	default:
		return c, fmt.Errorf("web-found providers must be allow, approve or deny, not %q", c.WebFoundProviders)
	}
	return c, nil
}

// Effective is the controls with defaults in place of unset values, which is
// how a pass created before controls existed is read.
func (c Controls) Effective() Controls {
	if c.MaxCallsPerMinute == 0 {
		c.MaxCallsPerMinute = DefaultMaxCallsPerMinute
	}
	if c.MaxCallsPerProviderPerMinute == 0 {
		c.MaxCallsPerProviderPerMinute = min(DefaultMaxCallsPerProviderPerMinute, c.MaxCallsPerMinute)
	}
	c.NewProviders = NewProviderRule(strings.ToLower(strings.TrimSpace(string(c.NewProviders))))
	if c.NewProviders == "" {
		c.NewProviders = NewProvidersCap
	}
	if c.NewProviderCapMinor == 0 && c.NewProviders == NewProvidersCap {
		c.NewProviderCapMinor = DefaultNewProviderCapMinor
	}
	c.UnverifiedProviders = UnverifiedRule(strings.ToLower(strings.TrimSpace(string(c.UnverifiedProviders))))
	if c.UnverifiedProviders == "" {
		c.UnverifiedProviders = UnverifiedAllow
	}
	c.WebFoundProviders = WebFoundRule(strings.ToLower(strings.TrimSpace(string(c.WebFoundProviders))))
	if c.WebFoundProviders == "" {
		c.WebFoundProviders = WebFoundApprove
	}
	return c
}

// WebFound decides a call to an endpoint found only on the open web (never
// executed here); foundOnWeb says it is one, approved that the person already
// said yes to this outcome. It returns "" when the call may go ahead,
// otherwise the reason and whether a person's approval would let it.
func (c Controls) WebFound(foundOnWeb, approved bool) (reason string, approvable bool) {
	c = c.Effective()
	if !foundOnWeb {
		return "", false
	}
	switch c.WebFoundProviders {
	case WebFoundApprove:
		if approved {
			return "", false
		}
		return ReasonWebFoundNeedsApproval, true
	case WebFoundDeny:
		return ReasonWebFoundNotAllowed, false
	}
	return "", false
}

// Unverified decides a call to a provider whose identity is or isn't
// verified; approved says the person already said yes to this outcome. It
// returns "" when the call may go ahead, otherwise the reason and whether a
// person's approval would let it.
func (c Controls) Unverified(verified, approved bool) (reason string, approvable bool) {
	c = c.Effective()
	if verified {
		return "", false
	}
	switch c.UnverifiedProviders {
	case UnverifiedApprove:
		if approved {
			return "", false
		}
		return ReasonUnverifiedNeedsApproval, true
	case UnverifiedDeny:
		return ReasonUnverifiedNotAllowed, false
	}
	return "", false
}

// Velocity checks the calls made in the last minute, across the pass and to
// the one provider, against the limits. It returns "" when the next call is
// within them.
func (c Controls) Velocity(callsLastMinute, providerCallsLastMinute int) string {
	c = c.Effective()
	switch {
	case callsLastMinute >= c.MaxCallsPerMinute:
		return ReasonRateLimited
	case providerCallsLastMinute >= c.MaxCallsPerProviderPerMinute:
		return ReasonProviderRateLimited
	}
	return ""
}

// NewProvider decides a call to a provider: paidBefore says whether the
// person has ever paid it, approved whether the person said yes to this
// particular outcome. It returns "" when the call may go ahead, otherwise the
// reason and whether a person's approval would let it.
func (c Controls) NewProvider(amountMinor int64, paidBefore, approved bool) (reason string, approvable bool) {
	c = c.Effective()
	if paidBefore || approved {
		return "", false
	}
	switch c.NewProviders {
	case NewProvidersCap:
		if amountMinor > c.NewProviderCapMinor {
			return ReasonNewProviderOverCap, true
		}
	case NewProvidersApprove:
		return ReasonNewProviderApproval, true
	}
	return "", false
}
