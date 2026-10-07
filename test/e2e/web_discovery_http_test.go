package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/project-algebra/algebra/internal/app"
)

// staticFinder stands in for the model: the one endpoint it "found on the web"
// is the sandbox provider this server hosts, so everything after the search is
// real: the free probe, the candidate it returns, and the paid call.
type staticFinder []app.WebFound

func (f staticFinder) Find(context.Context, string, int) ([]app.WebFound, error) { return f, nil }

func TestWebDiscoveryOverHTTP_WithoutAGeminiKeyItSaysSo(t *testing.T) {
	b, base := serve(t)
	token := issuePass(t, b)
	status, _, body := call(t, "POST", base+"/api/v1/discover/web", token, map[string]any{"capability": "solana.token-risk"})
	if status != 501 || !strings.Contains(body["error"].(string), "GEMINI_API_KEY") {
		t.Errorf("off, and says what turns it on: %d %v", status, body)
	}
	if status, _, _ := call(t, "POST", base+"/api/v1/discover/web", "not-a-token", map[string]any{"query": "x"}); status != 401 {
		t.Errorf("no agent, no search: %d", status)
	}

	cs := mcpSession(t, base, token)
	if _, text, isErr := callTool(t, cs, "algebra.discover_web", map[string]any{"query": "x"}); !isErr || !strings.Contains(text, "GEMINI_API_KEY") {
		t.Errorf("the MCP tool says the same: %v %q", isErr, text)
	}
}

func TestWebDiscoveryOverHTTP_FindsProbesAndTheCandidateCanBePaid(t *testing.T) {
	b, base := serve(t)
	token := issuePass(t, b)
	b.Execution.SetWebFinder(staticFinder{
		{Name: "Token Doctor", URL: base + "/api/v1/sandbox/x402/token-risk", Method: "POST", Description: "a service found on the web"},
		{Name: "Nobody home", URL: base + "/api/v1/sandbox/x402/does-not-exist", Method: "POST"},
	}, nil)

	status, _, found := call(t, "POST", base+"/api/v1/discover/web", token, map[string]any{"capability": "solana.token-risk"})
	if status != 200 || found["endpoints_are_unverified_web_finds"] != true {
		t.Fatalf("discover: %d %v", status, found)
	}
	eps, _ := found["endpoints"].([]any)
	if len(eps) != 2 {
		t.Fatalf("endpoints: %v", found["endpoints"])
	}
	first, second := eps[0].(map[string]any), eps[1].(map[string]any)
	if first["verified"] != true || first["network"] != "sandbox" || first["price_minor"].(float64) <= 0 || first["name"] != "Token Doctor" {
		t.Errorf("the one that answers like x402 comes first, priced: %v", first)
	}
	if second["verified"] == true || second["not_verified_reason"] == nil {
		t.Errorf("the one that doesn't is reported, with a reason: %v", second)
	}
	candidate := first["candidate"]

	// The candidate it returns is what execute takes, and it is paid like any
	// other: through the Spend Pass.
	status, _, out := call(t, "POST", base+"/api/v1/execute", token, map[string]any{
		"capability": "solana.token-risk", "input": map[string]any{"mint": "So11111111111111111111111111111111111111112"},
		"budget_max_minor": 50_000, "window": "web-e2e", "candidates": []any{candidate},
	})
	if status != 200 || out["delivered"] != true || out["response"] == nil {
		t.Fatalf("execute with the web find: %d %v", status, out)
	}

	// And over MCP, the same search.
	cs := mcpSession(t, base, token)
	m, text, isErr := callTool(t, cs, "algebra.discover_web", map[string]any{"capability": "solana.token-risk"})
	if isErr || m["endpoints_are_unverified_web_finds"] != true || m["untrusted_text"] == nil {
		t.Fatalf("mcp discover_web: %v %s", m, text)
	}
	if eps, _ := m["endpoints"].([]any); len(eps) != 2 {
		t.Errorf("mcp endpoints: %v", m["endpoints"])
	}
}
