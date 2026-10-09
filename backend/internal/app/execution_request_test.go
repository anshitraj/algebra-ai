package app

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/routing"
)

func configuredCand(t *testing.T, capability, provider, endpoint string) routing.Candidate {
	t.Helper()
	c, err := routing.Candidate{
		Capability: capability, Provider: provider, ExecutionType: routing.ExecX402, Endpoint: endpoint, Network: "solana",
		Sources: []routing.DiscoverySource{routing.SourceConfigured},
	}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestResolveCandidates(t *testing.T) {
	configured := map[string][]routing.Candidate{
		"acme":  {configuredCand(t, "solana.token-risk", "acme", "https://api.acme.example/risk"), configuredCand(t, "wallet.analytics", "acme", "https://api.acme.example/wallet")},
		"other": {configuredCand(t, "solana.token-risk", "other", "https://other.example/risk")},
	}

	// Named: only the configured ones that do this capability, trusted as such.
	got, rej := ResolveCandidates("solana.token-risk", []string{" ACME "}, nil, configured)
	if len(got) != 1 || got[0].Endpoint != "https://api.acme.example/risk" || got[0].Trust() != routing.TrustNative || len(rej) != 0 {
		t.Errorf("named: %+v %+v", got, rej)
	}
	// A name the operator never configured is a rejection, not a guess.
	_, rej = ResolveCandidates("solana.token-risk", []string{"nobody"}, nil, configured)
	if len(rej) != 1 || rej[0].Provider != "nobody" || rej[0].Code != routing.RejectUnquotable {
		t.Errorf("unknown name: %+v", rej)
	}
	// Known provider, wrong capability: also a rejection.
	if _, rej = ResolveCandidates("swap.spot", []string{"acme"}, nil, configured); len(rej) != 1 {
		t.Errorf("acme doesn't do swaps: %+v", rej)
	}

	// Supplied by the agent: unverified, whatever it claims.
	got, _ = ResolveCandidates("solana.token-risk", nil, []CandidateInput{
		{Endpoint: "https://found.example/api", Network: "solana", Name: "I found this"},
		{Provider: "native", Endpoint: "https://also-found.example/api"},
	}, configured)
	if len(got) != 2 || got[0].Provider != "found.example" || got[1].Provider != "native" {
		t.Fatalf("supplied: %+v", got)
	}
	for _, c := range got {
		n, err := c.Normalize()
		if err != nil {
			t.Fatal(err)
		}
		if n.Trust() != routing.TrustUnverified || !slices.Equal(n.Sources, []routing.DiscoverySource{routing.SourceWeb}) {
			t.Errorf("an agent can't vouch for its own provider (even one it calls %q): %s %v", c.Provider, n.Trust(), n.Sources)
		}
	}

	// Neither: every configured provider that does this capability, in a stable order.
	first, _ := ResolveCandidates("solana.token-risk", nil, nil, configured)
	for i := 0; i < 20; i++ {
		again, _ := ResolveCandidates("solana.token-risk", nil, nil, configured)
		if !slices.EqualFunc(first, again, func(a, b routing.Candidate) bool { return a.ID == b.ID }) {
			t.Fatal("the default candidate order must not depend on map iteration")
		}
	}
	if len(first) != 2 {
		t.Errorf("default: %+v", first)
	}
	if none, _ := ResolveCandidates("swap.spot", nil, nil, configured); len(none) != 0 {
		t.Errorf("nothing configured does swaps: %+v", none)
	}
}

func TestResponseValue(t *testing.T) {
	if (ExecutionReport{}).ResponseValue() != nil {
		t.Error("nothing delivered, nothing to show")
	}
	v := ExecutionReport{Body: []byte(`{"risk_score":12}`)}.ResponseValue()
	if raw, ok := v.(json.RawMessage); !ok || string(raw) != `{"risk_score":12}` {
		t.Errorf("JSON stays JSON: %#v", v)
	}
	text := ExecutionReport{Body: []byte("<html>hello</html>"), ContentType: "text/html"}.ResponseValue()
	if m, ok := text.(map[string]string); !ok || m["text"] != "<html>hello</html>" || m["content_type"] != "text/html" {
		t.Errorf("anything else is labelled text: %#v", text)
	}
	big := ExecutionReport{Body: make([]byte, maxResponseText+10)}.ResponseValue()
	if m := big.(map[string]string); len(m["text"]) != maxResponseText {
		t.Errorf("text is bounded: %d", len(m["text"]))
	}
}

func TestOutcomeOf(t *testing.T) {
	if o := OutcomeOf(nil, nil); o.Delivered || len(o.Attempts) != 0 || o.Attempts == nil {
		t.Errorf("an empty outcome still has a (empty) attempts list for clients: %+v", o)
	}
	yes := true
	view := &IntentView{Summary: "Committed: paid once and the result was received.", Receipt: "jws"}
	rep := &PlanReport{
		Delivered: true, Stopped: "committed", Intent: view,
		Rejected: []routing.Rejection{{Provider: "x", Code: routing.RejectOverBudget}},
		Attempts: []ExecutionReport{{
			Result: routing.ExecutionResult{Provider: "alpha", Payment: routing.PaymentSettled, Delivery: econ.FulfillmentFulfilled},
			Body:   []byte(`{"ok":true}`),
		}},
	}
	o := OutcomeOf(&yes, rep)
	if !o.Delivered || o.Created == nil || !*o.Created || o.Summary != view.Summary || o.Receipt != "jws" || len(o.Attempts) != 1 || len(o.Rejected) != 1 {
		t.Errorf("outcome: %+v", o)
	}
	if !o.Untrusted || o.Response == nil {
		t.Error("the response is handed over, labelled as untrusted provider data")
	}
	b, _ := json.Marshal(o)
	if !json.Valid(b) {
		t.Errorf("must serialize: %s", b)
	}
	// A failed attempt with no body carries no response.
	rep.Attempts[0].Body = nil
	if o := OutcomeOf(nil, rep); o.Response != nil || o.Untrusted {
		t.Errorf("no body, no response: %+v", o)
	}
}
