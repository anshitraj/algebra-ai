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
	return c
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
