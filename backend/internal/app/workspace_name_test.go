package app

import "testing"

func TestWorkspaceName(t *testing.T) {
	for in, want := range map[string]string{
		"Ada":             "Ada's workspace",
		"  Ada Lovelace ": "Ada Lovelace's workspace",
		"James":           "James' workspace",
		"Co's":            "Co's' workspace",
		"":                "My workspace",
		"   ":             "My workspace",
	} {
		if got := workspaceName(in); got != want {
			t.Errorf("workspaceName(%q) = %q, want %q", in, got, want)
		}
	}
}
