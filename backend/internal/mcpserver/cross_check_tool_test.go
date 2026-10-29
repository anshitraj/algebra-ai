package mcpserver

import (
	"context"
	"slices"
	"strings"
	"testing"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCrossCheckToolIsListedAndTellsAnAgentHowToUseIt(t *testing.T) {
	cs := connect(t, &Server{})
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var tool *gomcp.Tool
	for _, x := range list.Tools {
		if x.Name == "algebra_cross_check" {
			tool = x
		}
	}
	if tool == nil {
		t.Fatalf("algebra_cross_check must be listed, got %d tools", len(list.Tools))
	}
	if !slices.Contains(requiredOf(t, tool), "intent_ids") {
		t.Error("algebra_cross_check must require intent_ids")
	}
	// What an agent reads before it relies on the verdict.
	for _, phrase := range []string{"unclear", "never read unclear as agreement", "moves no money", "untrusted", "name one provider each"} {
		if !strings.Contains(tool.Description, phrase) {
			t.Errorf("the description must tell the agent about %q", phrase)
		}
	}
}

func TestCrossCheckToolReportsWhenExecutionIsOff(t *testing.T) {
	cs := connect(t, &Server{})
	res, err := cs.CallTool(context.Background(), &gomcp.CallToolParams{Name: "algebra_cross_check", Arguments: map[string]any{"intent_ids": []string{"a", "b"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(textOf(res), "not enabled") {
		t.Errorf("a server without execution must say so: %+v", res)
	}
}
