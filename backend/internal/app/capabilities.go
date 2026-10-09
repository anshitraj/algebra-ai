package app

import (
	"encoding/json"

	"github.com/project-algebra/algebra/internal/domain/routing"
)

// DefaultCapabilities are the capabilities Algebra ships a description and an
// output schema for. Any capability ID can still be asked for: one with no
// entry here is executed and judged by the generic evaluator, with no schema.
func DefaultCapabilities() []routing.Capability {
	return []routing.Capability{
		{
			ID: "solana.token-risk", Kind: routing.KindData, Title: "Solana token risk score",
			Description: "A safety or risk assessment of one SPL token mint.",
			// Providers differ in what they report (a score, a risk level, a list
			// of flags), so the shipped schema asks only for what any answer
			// about a token must contain: which token it is about. A stricter
			// schema judges every provider by one provider's shape and would
			// call a good answer unusable after the money was spent.
			OutputSchema: json.RawMessage(`{"type":"object","required":["mint"],"properties":{"mint":{"type":"string"}}}`),
		},
	}
}
