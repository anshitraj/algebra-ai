package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/project-algebra/algebra/internal/domain/routing"
)

// EvalInput is what an evaluator judges.
type EvalInput struct {
	Response    []byte
	ContentType string
	// Delivered: a success response arrived. An evaluator is still called
	// when nothing did, so every attempt gets a quality record.
	Delivered bool
}

// Evaluator judges how good a delivered result is. A capability can name its
// own (a swap is judged on output amount and slippage, an email lookup on
// accuracy); the generic one checks what can be checked about any response.
type Evaluator interface {
	// Name includes a version, e.g. "generic@1", so records say who judged.
	Name() string
	Evaluate(ctx context.Context, c routing.Capability, in EvalInput) (routing.QualityResult, error)
}

// GenericEvaluatorName is the registry key of the evaluator used when a
// capability doesn't name one.
const GenericEvaluatorName = "generic"

// GenericEvaluator checks that a response is well-formed JSON and, when the
// capability declares an output schema, that it satisfies it. It makes no
// claim about whether the data is true: SemanticQuality and Freshness stay
// unjudged (nil) rather than guessed.
type GenericEvaluator struct{}

func (GenericEvaluator) Name() string { return "generic@1" }

func (g GenericEvaluator) Evaluate(_ context.Context, c routing.Capability, in EvalInput) (routing.QualityResult, error) {
	q := routing.QualityResult{Evaluator: g.Name()}
	if !in.Delivered {
		return q, q.Finalize(false)
	}
	var doc any
	wellFormed := len(bytes.TrimSpace(in.Response)) > 0 && json.Unmarshal(in.Response, &doc) == nil
	if len(c.OutputSchema) == 0 {
		// With no schema, all that can be checked is that there is a
		// well-formed, non-empty JSON response.
		q.SchemaValid = &wellFormed
		q.Flags = append(q.Flags, "no_schema")
		return q, q.Finalize(true)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(c.OutputSchema, &schema); err != nil {
		return q, fmt.Errorf("capability %s: the output schema is unusable: %w", c.ID, err)
	}
	// Resolve with no loader: a schema can't make Algebra fetch a remote $ref.
	rs, err := schema.Resolve(nil)
	if err != nil {
		return q, fmt.Errorf("capability %s: the output schema is unusable: %w", c.ID, err)
	}
	valid := wellFormed && rs.Validate(doc) == nil
	q.SchemaValid = &valid
	if obj, ok := doc.(map[string]any); ok && len(schema.Required) > 0 {
		present := 0
		for _, k := range schema.Required {
			if _, ok := obj[k]; ok {
				present++
			}
		}
		share := float64(present) / float64(len(schema.Required))
		q.Completeness = &share
	}
	return q, q.Finalize(true)
}

// CapabilityCatalog looks up what Algebra knows about a capability: its kind,
// its evaluator and its output schema. An unknown capability is still
// executable; it just gets the generic treatment.
type CapabilityCatalog interface {
	Capability(id string) (routing.Capability, bool)
}

// StaticCatalog is a fixed set of capabilities.
type StaticCatalog map[string]routing.Capability

func (c StaticCatalog) Capability(id string) (routing.Capability, bool) {
	cap, ok := c[id]
	return cap, ok
}

// NewStaticCatalog normalizes capabilities and checks that each output
// schema compiles. A schema that doesn't would otherwise fail at the worst
// time, after money moved, and blame whichever provider answered; so it is
// refused when the catalog is built.
func NewStaticCatalog(caps ...routing.Capability) (StaticCatalog, error) {
	out := make(StaticCatalog, len(caps))
	for _, c := range caps {
		n, err := c.Normalize()
		if err != nil {
			return nil, fmt.Errorf("capability %q: %w", c.ID, err)
		}
		if len(n.OutputSchema) > 0 {
			var s jsonschema.Schema
			if err := json.Unmarshal(n.OutputSchema, &s); err != nil {
				return nil, fmt.Errorf("capability %q: output schema: %w", n.ID, err)
			}
			if _, err := s.Resolve(nil); err != nil {
				return nil, fmt.Errorf("capability %q: output schema: %w", n.ID, err)
			}
		}
		if _, dup := out[n.ID]; dup {
			return nil, fmt.Errorf("capability %q is defined twice", n.ID)
		}
		out[n.ID] = n
	}
	return out, nil
}
