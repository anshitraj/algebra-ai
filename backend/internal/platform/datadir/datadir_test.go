package datadir

import (
	"path/filepath"
	"testing"
)

func TestDefaultsToDotDataInTheWorkingDirectory(t *testing.T) {
	t.Setenv(Env, "")
	if got := Dir(); got != ".data" {
		t.Fatalf("Dir() = %q, want .data", got)
	}
	if got, want := Path("demo-provider.json"), filepath.Join(".data", "demo-provider.json"); got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}
}

func TestTheEnvironmentMovesIt(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(Env, dir)
	if got := Dir(); got != dir {
		t.Fatalf("Dir() = %q, want %q", got, dir)
	}
	if got, want := Path("merchant-sessions"), filepath.Join(dir, "merchant-sessions"); got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}
}
