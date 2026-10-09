package routing

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/project-algebra/algebra/internal/domain/econ"
)

// Kind groups capabilities by what "good" means for them, which decides the
// default evaluator and the shape of a cost estimate.
type Kind string

const (
	// KindData is a paid lookup: wallet analytics, token risk, a price feed.
	KindData Kind = "data"
	// KindInference is a model call whose output quality varies.
	KindInference Kind = "inference"
	// KindTrade is an on-chain exchange where output amount, slippage and
	// price impact are what matter.
	KindTrade Kind = "trade"
)

var evaluatorRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)

// maxSchemaBytes bounds a stored JSON Schema.
const maxSchemaBytes = 64 << 10

// Capability describes one kind of outcome an agent can ask for.
type Capability struct {
	ID          string `json:"id"`
	Kind        Kind   `json:"kind"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	// Evaluator names the evaluator that scores this capability's results
	// ("generic" when empty). The domain only records the name; the router
	// looks the implementation up.
	Evaluator string `json:"evaluator,omitempty"`
	// OutputSchema, when set, is the JSON Schema a response must satisfy to
	// count as schema-valid.
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
}

// Normalize validates a capability and puts it in canonical form.
func (c Capability) Normalize() (Capability, error) {
	id, err := econ.NormalizeCapability(c.ID)
	if err != nil {
		return c, err
	}
	c.ID = id
	switch c.Kind {
	case KindData, KindInference, KindTrade:
	case "":
		c.Kind = KindData
	default:
		return c, fmt.Errorf("capability kind must be data, inference or trade, not %q", c.Kind)
	}
	if c.Title = strings.TrimSpace(c.Title); len(c.Title) > 120 {
		return c, fmt.Errorf("capability title is longer than 120 characters")
	}
	if c.Description = strings.TrimSpace(c.Description); len(c.Description) > 1000 {
		return c, fmt.Errorf("capability description is longer than 1000 characters")
	}
	if c.Evaluator = strings.ToLower(strings.TrimSpace(c.Evaluator)); c.Evaluator != "" && !evaluatorRE.MatchString(c.Evaluator) {
		return c, fmt.Errorf("evaluator name %q isn't valid", c.Evaluator)
	}
	if len(c.OutputSchema) > 0 {
		if len(c.OutputSchema) > maxSchemaBytes {
			return c, fmt.Errorf("output schema is larger than %d KiB", maxSchemaBytes>>10)
		}
		if !json.Valid(c.OutputSchema) {
			return c, fmt.Errorf("output schema is not valid JSON")
		}
	}
	return c, nil
}
