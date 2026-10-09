package routing

import (
	"fmt"
	"math"
	"time"
)

// QualityResult is how good a delivered result was, as judged by an
// evaluator. Every score is optional: nil means the evaluator could not
// judge it, which is different from zero. An unknown never drags a
// provider's record down, and a guess never props it up.
type QualityResult struct {
	// Evaluator names who judged, with its version, e.g. "generic@1".
	Evaluator string `json:"evaluator"`

	// SchemaValid: the response satisfies the capability's output schema.
	SchemaValid *bool `json:"schema_valid,omitempty"`
	// SemanticQuality, Freshness and Completeness are each 0 to 1.
	SemanticQuality *float64 `json:"semantic_quality,omitempty"`
	Freshness       *float64 `json:"freshness,omitempty"`
	Completeness    *float64 `json:"completeness,omitempty"`

	// FinalQuality is 0 to 100, set by Finalize. Nil means a result was
	// delivered but nothing about it could be judged.
	FinalQuality *float64 `json:"final_quality,omitempty"`

	// Flags are short machine-readable notes ("not_delivered", "schema_invalid").
	Flags       []string  `json:"flags,omitempty"`
	EvaluatedAt time.Time `json:"evaluated_at,omitzero"`
}

// Component weights for FinalQuality. Only the components that were judged
// take part, and their weights are renormalised to sum to one.
const (
	weightSchema       = 0.25
	weightSemantic     = 0.40
	weightCompleteness = 0.20
	weightFreshness    = 0.15

	// schemaInvalidCap is the highest FinalQuality a response that fails its
	// schema can get, whatever else is true of it.
	schemaInvalidCap = 40.0
)

// Finalize computes FinalQuality. A result that wasn't delivered scores 0,
// known and final. A delivered one scores the weighted mean of whatever was
// judged, capped when it fails its schema. If nothing was judged,
// FinalQuality stays nil.
func (q *QualityResult) Finalize(delivered bool) error {
	for _, s := range []struct {
		name string
		v    *float64
	}{{"semantic quality", q.SemanticQuality}, {"freshness", q.Freshness}, {"completeness", q.Completeness}} {
		if s.v != nil && !unit(*s.v) {
			return fmt.Errorf("%s must be between 0 and 1", s.name)
		}
	}
	q.FinalQuality = nil
	if !delivered {
		q.FinalQuality = ptr(0.0)
		q.Flags = addFlag(q.Flags, "not_delivered")
		return nil
	}
	var sum, weight float64
	add := func(w float64, v float64) {
		sum += w * v
		weight += w
	}
	if q.SchemaValid != nil {
		if *q.SchemaValid {
			add(weightSchema, 1)
		} else {
			add(weightSchema, 0)
			q.Flags = addFlag(q.Flags, "schema_invalid")
		}
	}
	if q.SemanticQuality != nil {
		add(weightSemantic, *q.SemanticQuality)
	}
	if q.Completeness != nil {
		add(weightCompleteness, *q.Completeness)
	}
	if q.Freshness != nil {
		add(weightFreshness, *q.Freshness)
	}
	if weight == 0 {
		return nil
	}
	final := 100 * sum / weight
	if q.SchemaValid != nil && !*q.SchemaValid {
		final = math.Min(final, schemaInvalidCap)
	}
	q.FinalQuality = ptr(final)
	return nil
}

func addFlag(flags []string, f string) []string {
	for _, have := range flags {
		if have == f {
			return flags
		}
	}
	return append(flags, f)
}
