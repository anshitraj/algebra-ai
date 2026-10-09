package x402client

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/project-algebra/algebra/internal/domain/routing"
)

func TestBuildSpecFillsPathParametersAndLeavesTheRestForTheQueryOrBody(t *testing.T) {
	// GET: the path takes mint and chain; what is left becomes the query.
	s, err := buildSpec("https://api.example.com/v1/{chain}/tokens/{mint}", "GET", json.RawMessage(`{"chain":"solana","mint":"So11111111111111111111111111111111111111112","limit":5}`))
	if err != nil || s.method != "GET" || s.url != "https://api.example.com/v1/solana/tokens/So11111111111111111111111111111111111111112?limit=5" {
		t.Fatalf("GET: %+v %v", s, err)
	}

	// The same endpoint as a normalised candidate carries its braces as %7B..%7D.
	s, err = buildSpec("https://api.example.com/v1/%7Bchain%7D/tokens/%7Bmint%7D", "GET", json.RawMessage(`{"chain":"solana","mint":"abc"}`))
	if err != nil || s.url != "https://api.example.com/v1/solana/tokens/abc" {
		t.Fatalf("escaped placeholders: %+v %v", s, err)
	}

	// POST: the path parameter leaves the body, which keeps the rest.
	s, err = buildSpec("https://gw.example.com/v3/projects/{projectsId}:translateText", "POST", json.RawMessage(`{"projectsId":"my-project-1","contents":["hello"],"targetLanguageCode":"fr"}`))
	if err != nil || s.method != "POST" || s.url != "https://gw.example.com/v3/projects/my-project-1:translateText" || string(s.body) != `{"contents":["hello"],"targetLanguageCode":"fr"}` {
		t.Fatalf("POST with a suffix after the placeholder: %+v %q %v", s, s.body, err)
	}

	// Nothing left over: a POST still sends an empty object, a GET with no
	// method given is a GET.
	s, _ = buildSpec("https://gw.example.com/v1/{id}", "POST", json.RawMessage(`{"id":7}`))
	if s.url != "https://gw.example.com/v1/7" || string(s.body) != `{}` {
		t.Errorf("numbers fill paths: %+v %q", s, s.body)
	}
	if s, _ = buildSpec("https://gw.example.com/v1/{id}", "", json.RawMessage(`{"id":"x"}`)); s.method != "GET" {
		t.Errorf("an input that is all path parameters is a GET when no method is given: %+v", s)
	}

	// The same placeholder twice takes the same value; request hashes follow the filled URL.
	a, _ := buildSpec("https://gw.example.com/{x}/a/{x}", "GET", json.RawMessage(`{"x":"1"}`))
	b, _ := buildSpec("https://gw.example.com/{x}/a/{x}", "GET", json.RawMessage(`{"x":"2"}`))
	if a.url != "https://gw.example.com/1/a/1" || a.hash() == b.hash() {
		t.Errorf("repeated placeholder: %s, hashes must differ: %v", a.url, a.hash() == b.hash())
	}

	// An endpoint with no placeholders is untouched, braces in the query and all.
	if s, _ = buildSpec("https://gw.example.com/plain?x=1", "GET", json.RawMessage(`{"y":2}`)); s.url != "https://gw.example.com/plain?x=1&y=2" {
		t.Errorf("no placeholders: %s", s.url)
	}
}

func TestBuildSpecEscapesPathValuesAsOneSegment(t *testing.T) {
	for in, want := range map[string]string{
		"has space":   "has%20space",
		"a:b":         "a:b",
		"café":        "caf%C3%A9",
		"100%":        "100%25",
		"semi;colon":  "semi%3Bcolon",
		"plus+and=eq": "plus+and=eq",
	} {
		raw, _ := json.Marshal(map[string]string{"id": in})
		s, err := buildSpec("https://gw.example.com/items/{id}", "GET", raw)
		if err != nil || s.url != "https://gw.example.com/items/"+want {
			t.Errorf("%q: %s (%v), want …/items/%s", in, s.url, err, want)
		}
	}
}

func TestBuildSpecRefusesPathValuesThatCouldChangeWhatIsCalled(t *testing.T) {
	endpoint := "https://gw.example.com/items/{id}"
	for _, bad := range []string{"..", ".", "a/b", "a\\b", "x?y=1", "x#frag", "", "   ", "line\nbreak", "tab\there", "nul\x00", strings.Repeat("a", maxPathValue+1)} {
		raw, _ := json.Marshal(map[string]string{"id": bad})
		if s, err := buildSpec(endpoint, "GET", raw); err == nil {
			t.Errorf("%q must be refused, got %s", bad, s.url)
		}
	}
	for name, raw := range map[string]string{
		"missing":       `{"other":1}`,
		"not an object": `["a"]`,
		"null":          `null`,
		"nested":        `{"id":{"a":1}}`,
		"list":          `{"id":["a"]}`,
	} {
		if _, err := buildSpec(endpoint, "GET", json.RawMessage(raw)); err == nil {
			t.Errorf("%s: must be refused", name)
		}
	}
	// The error names the parameter, so an agent can fix its input.
	_, err := buildSpec("https://gw.example.com/{a}/{b}", "GET", json.RawMessage(`{"a":"x"}`))
	if err == nil || !strings.Contains(err.Error(), `"b"`) {
		t.Errorf("the missing parameter is named: %v", err)
	}
	// A placeholder the runner can't read is an error, never sent as literal braces.
	if _, err := buildSpec("https://gw.example.com/{a b}/x", "GET", json.RawMessage(`{}`)); err == nil {
		t.Error("an unreadable placeholder must be refused")
	}
	if _, err := buildSpec("https://gw.example.com/{+name}/x", "GET", json.RawMessage(`{"name":"v"}`)); err == nil {
		t.Error("a placeholder with other syntax must be refused")
	}
}

func TestATemplatedEndpointIsPricedAndPaidAtItsFilledUpURL(t *testing.T) {
	p := newProvider(t)
	c, err := routing.Candidate{
		Capability: "solana.token-risk", Provider: "acme", ExecutionType: routing.ExecX402, Endpoint: p.srv.URL + "/v1/{chain}/tokens/{mint}", Method: "GET",
		Sources: []routing.DiscoverySource{routing.SourceConfigured},
	}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	r := newRunner(5e9, 0)
	in := json.RawMessage(`{"chain":"solana","mint":"MINT1","limit":3}`)

	q, err := r.Quote(context.Background(), c, in)
	if err != nil {
		t.Fatal(err)
	}
	q.CandidateID, q.Capability, q.Provider, q.ExecutionType, q.Endpoint = c.ID, c.Capability, c.Provider, c.ExecutionType, c.Endpoint
	pay := &payCounter{}
	call := stepCall(q, pay.pay)
	call.Input = in
	obs := r.Run(context.Background(), call)
	if !obs.Delivered || pay.calls != 1 {
		t.Fatalf("delivered=%v pays=%d: %+v", obs.Delivered, pay.calls, obs)
	}
	var uris []string
	for _, rq := range p.requests() {
		uris = append(uris, rq.method+" "+rq.uri)
	}
	for _, u := range uris {
		if u != "GET /v1/solana/tokens/MINT1?limit=3" {
			t.Errorf("every request, priced or paid, went to the filled-in URL: %v", uris)
		}
	}
	// What the rail is told it is paying for is the URL that was called.
	if len(pay.reqs) != 1 || !strings.HasSuffix(pay.reqs[0].Resource, "/v1/solana/tokens/MINT1?limit=3") {
		t.Errorf("the payment names the real resource: %+v", pay.reqs)
	}

	// Without the parameter nothing is sent at all.
	before := len(p.requests())
	if _, err := r.Quote(context.Background(), c, json.RawMessage(`{"chain":"solana"}`)); err == nil || !strings.Contains(err.Error(), "mint") {
		t.Errorf("pricing without a parameter fails and names it: %v", err)
	}
	if len(p.requests()) != before {
		t.Error("a request with a missing path parameter must not reach the provider")
	}
	_ = http.StatusOK
}
