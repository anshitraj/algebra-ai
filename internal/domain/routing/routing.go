// Package routing is the vocabulary Algebra uses to turn "what an agent wants
// done" into "who does it, for how much, and how well it went":
//
//	Capability       a kind of outcome that can be bought ("solana.token-risk")
//	Candidate        one provider that claims to do it, normalised from whichever
//	                 source found it: a registry, the open web, a native adapter
//	Quote            a candidate's live, time-bounded price for one input
//	ExecutionPlan    ranked quotes: a primary and fallbacks, with the reasons
//	ExecutionResult  what actually happened to one attempt
//	QualityResult    how good the delivered result was
//
// It is plain data and validation: no I/O, no clock (callers pass now) and no
// randomness (callers pass IDs). Discovery, scoring and execution live above
// it. Only the economic coordinator (internal/domain/econ) decides whether
// money may move; nothing in this package can.
//
// Money is always integer minor units of the settlement asset (micro-USDC for
// USDC), never floats, so a comparison between providers is exact.
package routing

import (
	"fmt"
	"strings"

	"github.com/project-algebra/algebra/internal/domain/econ"
)

// Mode is how the router trades cost, speed and quality against each other.
type Mode string

const (
	// ModeCheapest minimises total expected cost: the provider's price plus
	// network and bridge fees plus expected slippage, not the advertised
	// price alone.
	ModeCheapest Mode = "CHEAPEST"
	// ModeFastest minimises expected time to a verified completion, judged
	// from observed latency rather than a provider's claim.
	ModeFastest Mode = "FASTEST"
	// ModeAuto weighs quality, reliability, cost, latency and policy fit.
	ModeAuto Mode = "AUTO"
)

// Valid reports whether m is a known mode.
func (m Mode) Valid() bool {
	return m == ModeCheapest || m == ModeFastest || m == ModeAuto
}

// ParseMode maps an intent's provider-selection strategy to a router mode.
// An empty strategy, "auto", the original name "best_execution" and "fixed"
// are all AUTO: a fixed provider list restricts which providers qualify, not
// how they rank.
func ParseMode(strategy string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case "", econ.StrategyAuto, econ.StrategyBestExecution, econ.StrategyFixed:
		return ModeAuto, nil
	case econ.StrategyCheapest:
		return ModeCheapest, nil
	case econ.StrategyFastest:
		return ModeFastest, nil
	}
	return "", fmt.Errorf("routing: unknown strategy %q", strategy)
}

func ptr[T any](v T) *T { return &v }
