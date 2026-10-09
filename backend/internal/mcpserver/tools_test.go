package mcpserver

import "testing"

// Registering a tool derives its input/output JSON schemas and panics on a
// shape the MCP spec doesn't allow (e.g. a non-object output), so building
// the server is itself the check.
func TestNewMCPServerRegistersEveryTool(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("tool registration panicked: %v", r)
		}
	}()
	if NewMCPServer(&Server{}) == nil {
		t.Fatal("nil server")
	}
}
