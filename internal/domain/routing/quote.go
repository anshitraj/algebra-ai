package routing

import (
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/econ"
)

// Cost is what one execution is expected to cost, in minor units of the
// settlement asset (micro-USDC for USDC). Comparing providers on the price
// they advertise alone is how an agent ends up with the "cheap" API that
// needs a bridge and loses a percent to slippage: every component counts.
type Cost struct {
	// ProviderMinor is the provider's own price, or for a trade the amount
	// put in.
	ProviderMinor   int64 `json:"provider_minor"`
	NetworkFeeMinor int64 `json:"network_fee_minor,omitempty"`
	BridgeFeeMinor  int64 `json:"bridge_fee_minor,omitempty"`
	// SlippageMinor is the expected loss against the mid price.
	SlippageMinor int64 `json:"expected_slippage_minor,omitempty"`
}

// Total is everything the execution is expected to cost. It saturates rather
// than overflows, and counts a negative component as zero (Validate refuses
// them; Total just must never wrap).
func (c Cost) Total() int64 {
	var t int64
	for _, v := range []int64{c.ProviderMinor, c.NetworkFeeMinor, c.BridgeFeeMinor, c.SlippageMinor} {
		if v <= 0 {
			continue
		}
		if v > math.MaxInt64-t {
			return math.MaxInt64
		}
		t += v
	}
	return t
}

// Validate refuses negative components.
func (c Cost) Validate() error {
	if c.ProviderMinor < 0 || c.NetworkFeeMinor < 0 || c.BridgeFeeMinor < 0 || c.SlippageMinor < 0 {
		return errors.New("cost components can't be negative")
	}
	return nil
}

// Quote is a candidate's live, time-bounded offer for one specific input:
// what it would actually cost and take right now. A candidate's advertised
// price is never authoritative; a quote fetched from the provider shortly
// before paying is, the same rule Algebra applies to merchant checkout
// quotes.
type Quote struct {
	ID            string        `json:"id"`
	CandidateID   string        `json:"candidate_id"`
	Capability    string        `json:"capability"`
	Provider      string        `json:"provider"`
	ExecutionType ExecutionType `json:"execution_type"`
	Endpoint      string        `json:"endpoint,omitempty"`
	// Method is the HTTP method the priced request used; paying replays it.
	Method string `json:"method,omitempty"`

	Cost         Cost   `json:"cost"`
	Asset        string `json:"asset,omitempty"`
	AssetAddress string `json:"asset_address,omitempty"`
	Network      string `json:"network,omitempty"`
	// PayTo is the payee the provider named.
	PayTo     string         `json:"pay_to,omitempty"`
	Semantics econ.Semantics `json:"settlement_semantics,omitempty"`

	EstimatedLatencyMS int `json:"estimated_latency_ms,omitempty"`
	// Trade-shaped terms: expected output in atoms, as a decimal string.
	ExpectedOutput string `json:"expected_output,omitempty"`
	SlippageBps    int    `json:"slippage_bps,omitempty"`
	PriceImpactBps int    `json:"price_impact_bps,omitempty"`

	// Requirements is what the rail needs to pay: for x402, the provider's
	// payment requirements verbatim.
	Requirements json.RawMessage `json:"requirements,omitempty"`

	QuotedAt   time.Time `json:"quoted_at"`
	ValidUntil time.Time `json:"valid_until"`
	// Test marks a quote on a test network or from a sandbox provider.
	Test bool `json:"test,omitempty"`
}

// Expired reports whether the quote can no longer be paid at its terms.
func (q Quote) Expired(now time.Time) bool { return !now.Before(q.ValidUntil) }

// Validate checks that a quote is coherent enough to plan with.
func (q Quote) Validate() error {
	switch {
	case q.ID == "" || q.CandidateID == "":
		return errors.New("a quote needs its own id and its candidate's")
	case q.Provider == "":
		return errors.New("a quote needs a provider")
	case !q.ExecutionType.Valid():
		return errors.New("a quote needs a known execution type")
	case q.Cost.Validate() != nil:
		return q.Cost.Validate()
	case q.Cost.Total() > 0 && (q.Asset == "" || q.Network == ""):
		return errors.New("a priced quote names its asset and network")
	case !q.ValidUntil.After(q.QuotedAt):
		return errors.New("a quote must stay valid after it is made")
	case q.EstimatedLatencyMS < 0:
		return errors.New("estimated latency can't be negative")
	case q.SlippageBps < 0 || q.SlippageBps > 10_000 || q.PriceImpactBps < 0 || q.PriceImpactBps > 10_000:
		return errors.New("slippage and price impact are basis points between 0 and 10000")
	case q.ExpectedOutput != "" && !atomsRE.MatchString(q.ExpectedOutput):
		return errors.New("expected output must be a whole number of atoms")
	case len(q.Requirements) > maxReqsBytes:
		return errors.New("payment requirements are too large")
	case len(q.Requirements) > 0 && !json.Valid(q.Requirements):
		return errors.New("payment requirements are not valid JSON")
	}
	if id, err := econ.NormalizeCapability(q.Capability); err != nil || id != q.Capability {
		return errors.New("a quote's capability must be a normalised capability ID")
	}
	if q.Semantics != "" && !q.Semantics.Valid() {
		return errors.New("a quote's settlement semantics are unknown")
	}
	if want, ok := chain.AssetAddress(q.Network, q.Asset); ok && q.AssetAddress != "" && !chain.SameAddress(q.Network, want, q.AssetAddress) {
		return errors.New("a quote's asset is a look-alike of the real one")
	}
	return nil
}

// Hash commits to the terms of the offer: who, what, how much, where to pay
// and what the provider required. It leaves out the quote's own ID and its
// timestamps, so a re-quote with identical terms hashes the same and a
// change in any term, however small, doesn't.
func (q Quote) Hash() string {
	reqs, err := econ.Canonicalize(q.Requirements)
	if err != nil || len(q.Requirements) == 0 {
		reqs = nil
	}
	terms := struct {
		V            int             `json:"v"`
		CandidateID  string          `json:"candidate_id"`
		Capability   string          `json:"capability"`
		Provider     string          `json:"provider"`
		Type         ExecutionType   `json:"execution_type"`
		Endpoint     string          `json:"endpoint"`
		Method       string          `json:"method"`
		Cost         Cost            `json:"cost"`
		Asset        string          `json:"asset"`
		AssetAddress string          `json:"asset_address"`
		Network      string          `json:"network"`
		PayTo        string          `json:"pay_to"`
		Semantics    econ.Semantics  `json:"semantics"`
		Output       string          `json:"expected_output"`
		SlippageBps  int             `json:"slippage_bps"`
		ImpactBps    int             `json:"price_impact_bps"`
		Requirements json.RawMessage `json:"requirements"`
	}{1, q.CandidateID, q.Capability, q.Provider, q.ExecutionType, q.Endpoint, q.Method, q.Cost, q.Asset, q.AssetAddress, q.Network,
		q.PayTo, q.Semantics, q.ExpectedOutput, q.SlippageBps, q.PriceImpactBps, reqs}
	if terms.Requirements == nil {
		terms.Requirements = json.RawMessage("null")
	}
	b, _ := json.Marshal(terms)
	return econ.HashBytes(b)
}
