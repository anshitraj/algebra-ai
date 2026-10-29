package mcpserver

import (
	"context"
	"regexp"
	"strings"
	"testing"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// What OpenAI's and Anthropic's APIs accept as a function name. A client
// passes MCP tools to its model as functions, so a name outside this is
// rewritten or dropped before the model ever sees it.
var functionName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// Tools added after the rename have no earlier, dotted name to keep answering.
var addedAfterTheRename = []string{"algebra_cross_check"}

func TestListedNamesNeedNoRewritingByAnyClient(t *testing.T) {
	cs := connect(t, &Server{})
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	today := map[string]bool{}
	for _, name := range earlierNames {
		today[name] = true
	}
	for _, name := range addedAfterTheRename {
		today[name] = true
	}
	for _, tool := range list.Tools {
		if !functionName.MatchString(tool.Name) {
			t.Errorf("%q is not a name every client accepts: letters, digits, _ and - only", tool.Name)
		}
		// Cursor refuses a tool whose server name and tool name together pass 60 characters.
		if len("algebra")+len(tool.Name) > 60 {
			t.Errorf("%q is too long once a client adds the server's name", tool.Name)
		}
		if !today[tool.Name] {
			t.Errorf("%q is listed but has no entry in earlierNames", tool.Name)
		}
		delete(today, tool.Name)
		// An agent reads the descriptions and the argument notes to learn
		// what to call next: they must use the names that are listed.
		schema, _ := tool.InputSchema.(map[string]any)
		for earlier := range earlierNames {
			if strings.Contains(tool.Description, earlier) {
				t.Errorf("%s: the description still says %s", tool.Name, earlier)
			}
			for field, p := range asMap(schema["properties"]) {
				if d, _ := asMap(p)["description"].(string); strings.Contains(d, earlier) {
					t.Errorf("%s.%s: the argument note still says %s", tool.Name, field, earlier)
				}
			}
		}
	}
	for name := range today {
		t.Errorf("earlierNames points at %q, which is not listed", name)
	}
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func TestEarlierDottedNamesAreAnsweredByTodaysTools(t *testing.T) {
	cs := connect(t, &Server{})
	ctx := context.Background()
	// Arguments that pass each tool's schema, so the call reaches its handler
	// and the answer is the handler's own ("not enabled" on this bare server).
	priced := map[string]any{"capability": "token.price", "max_price_usdc": "0.05"}
	args := map[string]map[string]any{
		"algebra_quote": priced, "algebra_simulate": priced, "algebra_execute": priced,
		"algebra_status": {"intent_id": "x"}, "algebra_receipt": {"intent_id": "x"},
		"algebra_discover_web": {"query": "x"},
	}
	for earlier, now := range earlierNames {
		in := args[now]
		if in == nil {
			in = map[string]any{}
		}
		want, err := cs.CallTool(ctx, &gomcp.CallToolParams{Name: now, Arguments: in})
		if err != nil {
			t.Fatalf("%s: %v", now, err)
		}
		if strings.Contains(textOf(want), "validating") {
			t.Fatalf("%s: the test's arguments must pass the schema: %s", now, textOf(want))
		}
		got, err := cs.CallTool(ctx, &gomcp.CallToolParams{Name: earlier, Arguments: in})
		if err != nil {
			t.Fatalf("%s must still be answered, by %s: %v", earlier, now, err)
		}
		if got.IsError != want.IsError || textOf(got) != textOf(want) {
			t.Errorf("%s answered differently from %s: %q, want %q", earlier, now, textOf(got), textOf(want))
		}
	}
	// A name that never existed is still refused.
	if _, err := cs.CallTool(ctx, &gomcp.CallToolParams{Name: "algebra.nothing", Arguments: map[string]any{}}); err == nil {
		t.Error("an unknown dotted name must be refused")
	}
}

func TestLegacyNamesAreNotRewritten(t *testing.T) {
	cs := connect(t, &Server{LegacyTools: true})
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, tool := range list.Tools {
		listed[tool.Name] = true
	}
	for _, name := range []string{"algebra.discover_providers", "algebra.execution_status", "algebra_discover", "algebra_status"} {
		if !listed[name] {
			t.Errorf("with the legacy tools on, %s is listed", name)
		}
	}
	for earlier := range earlierNames {
		if listed[earlier] {
			t.Errorf("%s is answered but must not be listed", earlier)
		}
	}
}
