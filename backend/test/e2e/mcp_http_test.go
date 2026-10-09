package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// bearer sends a Spend Pass token the way an MCP client configured with an
// Authorization header does: on every request, never through the model.
type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if b.token != "" {
		r.Header.Set("Authorization", "Bearer "+b.token)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func mcpSession(t *testing.T, base, token string) *gomcp.ClientSession {
	t.Helper()
	cs, err := gomcp.NewClient(&gomcp.Implementation{Name: "e2e", Version: "0"}, nil).Connect(context.Background(), &gomcp.StreamableClientTransport{
		Endpoint: base + "/mcp", HTTPClient: &http.Client{Transport: bearer{token}, Timeout: 30 * time.Second}, DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connecting to /mcp: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func callTool(t *testing.T, cs *gomcp.ClientSession, name string, args map[string]any) (map[string]any, string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &gomcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var text strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*gomcp.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	var out map[string]any
	if res.StructuredContent != nil {
		raw, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(raw, &out)
	}
	return out, text.String(), res.IsError
}

// The sandbox rail only exists in the process that hosts the sandbox provider,
// so MCP is served by the API: an agent connects to /mcp with its Spend Pass
// token, simulates, executes, asks again and reads the kept answer back, all
// over the one connection.
func TestMCPOverHTTP_AnAgentExecutesThroughTheSandboxWithItsPassToken(t *testing.T) {
	b, base := serve(t)
	token := issuePass(t, b)
	cs := mcpSession(t, base, token)

	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, tool := range tools.Tools {
		listed[tool.Name] = true
	}
	for _, want := range []string{"algebra.execute", "algebra.execution_status", "algebra.simulate", "algebra.classes", "algebra.discover_providers"} {
		if !listed[want] {
			t.Errorf("%s is not offered over /mcp", want)
		}
	}

	req := map[string]any{
		"capability": "solana.token-risk", "input": map[string]any{"mint": "So11111111111111111111111111111111111111112"},
		"max_price_usdc": "0.05", "window": "mcp-e2e-" + time.Now().Format("150405.000000"),
	}

	// A dry run: allowed, and who would be paid. Nothing is paid.
	sim, text, isErr := callTool(t, cs, "algebra.simulate", map[string]any{
		"capability": req["capability"], "input": req["input"], "max_price_usdc": req["max_price_usdc"], "live_quotes": true,
	})
	if isErr || sim["verdict"] != "ALLOW" {
		t.Fatalf("simulate: %v %s", sim, text)
	}
	if wp, _ := sim["would_pay"].(map[string]any); wp == nil || wp["provider"] != "sandbox:token-risk" {
		t.Errorf("the sandbox provider would be paid: %v", sim["would_pay"])
	}

	// Executing pays through the sandbox rail, which works here.
	first, text, isErr := callTool(t, cs, "algebra.execute", req)
	if isErr || first["delivered"] != true || first["response"] == nil || first["receipt"] == nil {
		t.Fatalf("execute: %v %s", first, text)
	}
	intent, _ := first["intent"].(map[string]any)
	if intent == nil || intent["committed_minor"] == nil {
		t.Fatalf("it was paid: %v", first["intent"])
	}

	// Asking again returns the kept answer and pays nothing.
	again, text, isErr := callTool(t, cs, "algebra.execute", req)
	if isErr || again["replayed"] != true || again["delivered"] != true {
		t.Fatalf("a repeat request is answered from what was kept: %v %s", again, text)
	}

	// And the answer can be read back.
	status, text, isErr := callTool(t, cs, "algebra.execution_status", map[string]any{"intent_id": intent["id"]})
	if isErr {
		t.Fatalf("execution_status: %s", text)
	}
	if kept, _ := status["result"].(map[string]any); kept == nil || kept["response_is_untrusted_provider_data"] != true {
		t.Errorf("the kept answer is returned, marked as the provider's data: %v", status["result"])
	}

	// Without catalogs there are no classes to list, and the tool says so.
	if _, text, isErr := callTool(t, cs, "algebra.classes", map[string]any{}); !isErr || !strings.Contains(text, "turned off") {
		t.Errorf("classes with the catalogs off: %v %q", isErr, text)
	}
}

func TestMCPOverHTTP_NoTokenNoPayment(t *testing.T) {
	_, base := serve(t)
	cs := mcpSession(t, base, "")
	_, text, isErr := callTool(t, cs, "algebra.execute", map[string]any{
		"capability": "solana.token-risk", "input": map[string]any{"mint": "So11111111111111111111111111111111111111112"}, "max_price_usdc": "0.05",
	})
	if !isErr || !strings.Contains(text, "agent_token is required") {
		t.Errorf("an unauthenticated call is refused: %v %q", isErr, text)
	}
	_, text, isErr = callTool(t, cs, "algebra.execute", map[string]any{
		"agent_token": "alg_not_a_real_token", "capability": "solana.token-risk", "max_price_usdc": "0.05",
	})
	if !isErr || !strings.Contains(text, "invalid agent_token") {
		t.Errorf("a made-up token is refused: %v %q", isErr, text)
	}
}
