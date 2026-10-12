package tenancy

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/agent"
)

func TestEnvironmentsMapToOneNetworkEach(t *testing.T) {
	for env, want := range map[Environment]string{EnvSandbox: "sandbox", EnvDevnet: "solana-devnet", EnvMainnet: "solana"} {
		if got := env.Networks(); len(got) != 1 || got[0] != want {
			t.Errorf("%s networks = %v, want [%s]", env, got, want)
		}
		if back, ok := EnvironmentOfNetwork(want); !ok || back != env {
			t.Errorf("network %s maps back to %q", want, back)
		}
	}
	if Environment("prod").Valid() || Environment("prod").Networks() != nil {
		t.Error("an unknown environment must have no networks")
	}
	if _, ok := EnvironmentOfNetwork("base"); ok {
		t.Error("base belongs to no environment")
	}
}

func TestGeneratedKeysAreDistinctPrefixedAndOnlyTheHashIsKept(t *testing.T) {
	raw1, hash1, prefix1, err := GenerateKey(EnvSandbox)
	if err != nil {
		t.Fatal(err)
	}
	raw2, _, _, _ := GenerateKey(EnvSandbox)
	if raw1 == raw2 {
		t.Fatal("two keys must differ")
	}
	if !strings.HasPrefix(raw1, "alg_sk_sandbox_") || !IsAPIKey(raw1) {
		t.Errorf("key shape: %q", raw1[:16])
	}
	if hash1 != agent.HashToken(raw1) || strings.Contains(hash1, raw1) {
		t.Error("the stored hash must be the token hash of the key, never the key")
	}
	if !strings.HasPrefix(raw1, prefix1) || len(prefix1) != len("alg_sk_sandbox_")+4 {
		t.Errorf("prefix %q", prefix1)
	}
	if _, _, _, err := GenerateKey("prod"); err == nil {
		t.Error("an unknown environment must be refused")
	}
}

func TestKeyActivityAndScopes(t *testing.T) {
	now := time.Now()
	past, future := now.Add(-time.Minute), now.Add(time.Hour)
	k := APIKey{Scopes: []Scope{ScopeRead}}
	if !k.Active(now) || k.Has(ScopeExecute) || !k.Has(ScopeRead) {
		t.Error("fresh read-only key")
	}
	k.ExpiresAt = &past
	if k.Active(now) {
		t.Error("an expired key is not active")
	}
	k.ExpiresAt, k.RevokedAt = &future, &past
	if k.Active(now) {
		t.Error("a revoked key is not active")
	}
}

func TestScopesAreCleaned(t *testing.T) {
	got, err := CleanScopes(nil)
	if err != nil || !slices.Equal(got, AllScopes) {
		t.Errorf("empty means all: %v %v", got, err)
	}
	got, err = CleanScopes([]Scope{ScopeExecute, ScopeRead, ScopeRead})
	if err != nil || !slices.Equal(got, []Scope{ScopeRead, ScopeExecute}) {
		t.Errorf("dedupe and order: %v %v", got, err)
	}
	if _, err := CleanScopes([]Scope{"admin"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown scope: %v", err)
	}
}

func TestRoles(t *testing.T) {
	if !RoleOwner.CanManage() || !RoleAdmin.CanManage() || RoleDeveloper.CanManage() || RoleViewer.CanManage() {
		t.Error("only owners and admins manage")
	}
	if !RoleViewer.CanRead() || Role("stranger").CanRead() {
		t.Error("members read, strangers don't")
	}
}

func TestNames(t *testing.T) {
	if _, err := CleanName("name", "   "); err == nil {
		t.Error("blank")
	}
	if n, err := CleanName("name", "  Acme agents "); err != nil || n != "Acme agents" {
		t.Errorf("trim: %q %v", n, err)
	}
	if _, err := CleanName("name", strings.Repeat("x", 81)); err == nil {
		t.Error("too long")
	}
}
