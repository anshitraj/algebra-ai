package mcpserver

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/routing"
)

func connect(t *testing.T, srv *Server) *gomcp.ClientSession {
	t.Helper()
	s := NewMCPServer(srv)
	st, ct := gomcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := s.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func TestExecutionToolsAreListedWithTheRightInputs(t *testing.T) {
	cs := connect(t, &Server{})
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var execute, status *gomcp.Tool
	for _, tool := range list.Tools {
		switch tool.Name {
		case "algebra.execute":
			execute = tool
		case "algebra.execution_status":
			status = tool
		}
	}
	if execute == nil || status == nil {
		t.Fatalf("both tools must be listed, got %d tools", len(list.Tools))
	}
	for _, want := range []string{"capability", "max_price_usdc"} {
		if !slices.Contains(requiredOf(t, execute), want) {
			t.Errorf("algebra.execute must require %s", want)
		}
	}
	if !slices.Contains(requiredOf(t, status), "intent_id") {
		t.Error("algebra.execution_status must require intent_id")
	}
	// The description is what an agent reads before deciding how to behave.
	for _, phrase := range []string{"never charged twice", "pending_reconciliation", "untrusted"} {
		if !strings.Contains(execute.Description, phrase) {
			t.Errorf("the description must tell the agent about %q", phrase)
		}
	}
}

func requiredOf(t *testing.T, tool *gomcp.Tool) []string {
	t.Helper()
	m, ok := tool.InputSchema.(map[string]any)
	if !ok {
		t.Fatalf("input schema is %T", tool.InputSchema)
	}
	var out []string
	if req, ok := m["required"].([]any); ok {
		for _, r := range req {
			out = append(out, r.(string))
		}
	}
	return out
}

func TestExecutionToolsReportWhenDisabled(t *testing.T) {
	cs := connect(t, &Server{})
	res, err := cs.CallTool(context.Background(), &gomcp.CallToolParams{
		Name:      "algebra.execute",
		Arguments: map[string]any{"capability": "solana.token-risk", "max_price_usdc": "0.05"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(textOf(res), "not enabled") {
		t.Errorf("a server without execution must say so: %+v", res)
	}
}

func textOf(res *gomcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*gomcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestDescribeExecutionError(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{&app.ReservationRejected{Reason: app.RejectApproval, State: econ.StateAwaitingApproval}, "has to approve"},
		{&app.ReservationRejected{Reason: app.RejectCommitted, State: econ.StateCommitted, Duplicate: true}, "never charges twice"},
		{&app.ReservationRejected{Reason: app.RejectUnknown, State: econ.StateReconciling}, "Do not retry"},
		{&app.ReservationRejected{Reason: app.RejectHeld, State: econ.StateReserved}, "Do not retry"},
		{&app.ReservationRejected{Reason: app.RejectClosed, State: econ.StateExpired}, "expired or was cancelled"},
		{&app.AuthorityDenied{ReasonCodes: []string{"PASS_BUDGET_EXCEEDED"}}, "PASS_BUDGET_EXCEEDED"},
		{&app.NoRoute{Rejected: []routing.Rejection{{Provider: "acme", Code: routing.RejectOverBudget, Detail: "costs 9000"}}}, "acme: over_budget (costs 9000)"},
		{&app.NoRoute{}, "pass `providers` or `candidates`"},
	}
	for _, tc := range cases {
		got := describeExecutionError(tc.err, nil)
		if got == nil || !strings.Contains(got.Error(), tc.want) {
			t.Errorf("%T: want %q in %v", tc.err, tc.want, got)
		}
	}
	// Rejections found before running are included.
	err := describeExecutionError(&app.NoRoute{}, []routing.Rejection{{Provider: "ghost", Code: routing.RejectUnquotable}})
	if err == nil || !strings.Contains(err.Error(), "ghost: no_quote") {
		t.Errorf("pre-run rejections are listed: %v", err)
	}
	// Anything else passes through unchanged.
	plain := errors.New("boom")
	if describeExecutionError(plain, nil) != plain {
		t.Error("unknown errors pass through")
	}
}

func TestClassesAndSimulateAreListedAndSteerAgentsToClasses(t *testing.T) {
	cs := connect(t, &Server{})
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]*gomcp.Tool{}
	for _, tool := range list.Tools {
		tools[tool.Name] = tool
	}
	for _, name := range []string{"algebra.classes", "algebra.simulate", "algebra.execute"} {
		if tools[name] == nil {
			t.Fatalf("%s must be listed", name)
		}
	}
	for _, want := range []string{"capability", "max_price_usdc"} {
		if !slices.Contains(requiredOf(t, tools["algebra.simulate"]), want) {
			t.Errorf("algebra.simulate must require %s", want)
		}
	}
	if len(requiredOf(t, tools["algebra.classes"])) != 0 {
		t.Error("algebra.classes lists every class without any input")
	}
	// An agent reads these before it picks a capability.
	for name, phrase := range map[string]string{
		"algebra.execute":  "algebra.classes",
		"algebra.classes":  "rather than for one provider",
		"algebra.simulate": "without paying anything",
	} {
		if !strings.Contains(tools[name].Description, phrase) {
			t.Errorf("%s: the description must say %q", name, phrase)
		}
	}
}

func TestClassesAndSimulateReportWhenTheyCannotRun(t *testing.T) {
	cs := connect(t, &Server{})
	for name, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"algebra.classes":  {map[string]any{}, "turned off"},
		"algebra.simulate": {map[string]any{"capability": "token.price", "max_price_usdc": "0.05"}, "not enabled"},
	} {
		res, err := cs.CallTool(context.Background(), &gomcp.CallToolParams{Name: name, Arguments: tc.args})
		if err != nil {
			t.Fatal(err)
		}
		if !res.IsError || !strings.Contains(textOf(res), tc.want) {
			t.Errorf("%s on a server without it must say %q: %+v", name, tc.want, res)
		}
	}
}

func TestDiscoverWebIsListedAndTellsAgentsWhatItDoesNotDo(t *testing.T) {
	cs := connect(t, &Server{})
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var tool *gomcp.Tool
	for _, x := range list.Tools {
		if x.Name == "algebra.discover_web" {
			tool = x
		}
	}
	if tool == nil {
		t.Fatal("algebra.discover_web must be listed")
	}
	if len(requiredOf(t, tool)) != 0 {
		t.Error("capability or query: neither is required on its own")
	}
	for _, phrase := range []string{"Nothing is paid or chosen", "never yours", "unverified web find", "untrusted", "Gemini key"} {
		if !strings.Contains(tool.Description, phrase) {
			t.Errorf("the description must say %q", phrase)
		}
	}

	res, err := cs.CallTool(context.Background(), &gomcp.CallToolParams{Name: "algebra.discover_web", Arguments: map[string]any{"query": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(textOf(res), "not enabled") {
		t.Errorf("a server without execution must say so: %+v", res)
	}
}
