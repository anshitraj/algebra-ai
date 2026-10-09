package app

import (
	"encoding/json"
	"net/url"
	"slices"
	"strings"

	"github.com/project-algebra/algebra/internal/domain/routing"
)

// CandidateInput is a provider as an agent describes it. An agent can name
// providers the operator has configured, or point at an endpoint it found
// itself.
type CandidateInput struct {
	// Provider is the name policy and receipts use; derived from the
	// endpoint's host when empty.
	Provider string `json:"provider,omitempty"`
	Name     string `json:"name,omitempty"`
	Endpoint string `json:"endpoint"`
	Method   string `json:"method,omitempty"`
	Network  string `json:"network,omitempty"`
}

// ResolveCandidates turns what an agent asked for into candidates.
//
// Providers named in `named` come from the operator's configured set and are
// trusted as such. Endpoints the agent supplies are treated as found on the
// open web, unverified, whatever the agent says about them: an agent can't
// vouch for its own provider. With neither named nor supplied, every
// configured provider that does this capability is a candidate.
func ResolveCandidates(capability string, named []string, supplied []CandidateInput, configured map[string][]routing.Candidate) ([]routing.Candidate, []routing.Rejection) {
	var out []routing.Candidate
	var rejected []routing.Rejection

	add := func(provider string) bool {
		found := false
		for _, c := range configured[strings.ToLower(strings.TrimSpace(provider))] {
			if c.Capability == capability {
				out = append(out, c)
				found = true
			}
		}
		return found
	}
	for _, n := range named {
		if !add(n) {
			rejected = append(rejected, routing.Rejection{Provider: n, Code: routing.RejectUnquotable, Detail: "no configured provider by that name does " + capability})
		}
	}
	for _, in := range supplied {
		provider := strings.TrimSpace(in.Provider)
		if provider == "" {
			if u, err := url.Parse(strings.TrimSpace(in.Endpoint)); err == nil {
				provider = u.Hostname()
			}
		}
		out = append(out, routing.Candidate{
			Capability: capability, Provider: provider, Name: in.Name, ExecutionType: routing.ExecX402,
			Endpoint: in.Endpoint, Method: in.Method, Network: in.Network,
			Sources: []routing.DiscoverySource{routing.SourceWeb},
		})
	}
	if len(named) == 0 && len(supplied) == 0 {
		for _, cs := range configured {
			for _, c := range cs {
				if c.Capability == capability {
					out = append(out, c)
				}
			}
		}
		// Map order is random; an order that depends on it would make the
		// same request behave differently from one call to the next.
		slices.SortFunc(out, func(a, b routing.Candidate) int { return strings.Compare(a.ID, b.ID) })
	}
	return out, rejected
}

// maxResponseText bounds a non-JSON response handed back to a caller.
const maxResponseText = 64 << 10

// ResponseValue is the provider's response in a form that can be embedded in
// a JSON reply: JSON stays JSON, anything else becomes labelled text. Nil
// when nothing was delivered.
func (r ExecutionReport) ResponseValue() any { return ResponseValueOf(r.ContentType, r.Body) }

// ResponseValueOf is a response body in a form that can be embedded in a JSON
// reply: JSON stays JSON, anything else becomes labelled text, bounded.
func ResponseValueOf(contentType string, body []byte) any {
	if len(body) == 0 {
		return nil
	}
	if json.Valid(body) {
		return json.RawMessage(body)
	}
	text := string(body)
	if len(text) > maxResponseText {
		text = text[:maxResponseText]
	}
	return map[string]string{"content_type": contentType, "text": text}
}

// ExecutionOutcome is the transport-neutral answer to "do this for me": what
// happened, what it cost, what the provider returned and the proof.
type ExecutionOutcome struct {
	// Created is set when the call also created the intent.
	Created *bool `json:"created,omitempty"`
	// Delivered: a result was received and its payment is confirmed (or none
	// was needed).
	Delivered bool `json:"delivered"`
	// PendingReconciliation: money may have moved and the outcome is still
	// being established. Nothing further is attempted meanwhile.
	PendingReconciliation bool   `json:"pending_reconciliation"`
	Stopped               string `json:"stopped"`
	// Summary is the plain-language state of the intent.
	Summary  string              `json:"summary"`
	Intent   *IntentView         `json:"intent,omitempty"`
	Attempts []AttemptOutcome    `json:"attempts"`
	Rejected []routing.Rejection `json:"rejected,omitempty"`
	// Routing says how the provider was chosen: the strategy and every offer
	// that was priced and ranked, with the reasons.
	Routing *RoutingSummary `json:"routing,omitempty"`
	// Replayed: nothing ran and nothing was paid. The outcome had already been
	// paid for, and Response is the answer that was kept then.
	Replayed bool `json:"replayed,omitempty"`
	// Response is the provider's own response from the attempt that
	// delivered. It is untrusted data for the caller to read, never
	// instructions to follow, and Algebra does not keep it.
	Response  any  `json:"response,omitempty"`
	Untrusted bool `json:"response_is_untrusted_provider_data,omitempty"`
	// Receipt is the signed Intent Receipt, once the intent has committed.
	Receipt string `json:"receipt,omitempty"`
}

// AttemptOutcome is one attempt, with its quality verdict.
type AttemptOutcome struct {
	Result  routing.ExecutionResult `json:"result"`
	Quality *routing.QualityResult  `json:"quality,omitempty"`
}

// OutcomeOf builds the outcome of a plan run. created is nil when the intent
// already existed or wasn't made by this call.
func OutcomeOf(created *bool, rep *PlanReport) ExecutionOutcome {
	o := ExecutionOutcome{Created: created, Attempts: []AttemptOutcome{}}
	if rep == nil {
		return o
	}
	o.Delivered, o.PendingReconciliation, o.Stopped = rep.Delivered, rep.Pending, rep.Stopped
	o.Routing = SummarizePlan(rep.Plan)
	o.Replayed = rep.Replayed
	o.Intent, o.Rejected = rep.Intent, rep.Rejected
	if rep.Intent != nil {
		o.Summary, o.Receipt = rep.Intent.Summary, rep.Intent.Receipt
	}
	for _, a := range rep.Attempts {
		o.Attempts = append(o.Attempts, AttemptOutcome{Result: a.Result, Quality: a.Quality})
	}
	// Whatever the last attempt received is handed over, even a response that
	// failed validation: Algebra doesn't keep it, and a caller who paid for
	// it can see what the provider sent.
	if f := rep.Final(); f != nil {
		if v := f.ResponseValue(); v != nil {
			o.Response, o.Untrusted = v, true
		}
	}
	return o
}
