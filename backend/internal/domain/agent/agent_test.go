package agent

import (
	"strings"
	"testing"
	"time"
)

func TestGenerateToken_VerifiesAndRejectsWrongToken(t *testing.T) {
	raw, hash, err := GenerateToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(raw, tokenPrefix) {
		t.Errorf("expected token to start with %q, got %q", tokenPrefix, raw)
	}
	if !VerifyToken(raw, hash) {
		t.Error("expected raw token to verify against its own hash")
	}
	other, _, _ := GenerateToken()
	if VerifyToken(other, hash) {
		t.Error("expected a different token to NOT verify against this hash")
	}
}

func TestGenerateToken_Unique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		raw, _, err := GenerateToken()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if seen[raw] {
			t.Fatal("generated duplicate token")
		}
		seen[raw] = true
	}
}

func TestHasPermission(t *testing.T) {
	a := &Identity{
		ID:          "agent_1",
		Permissions: []Permission{PermShoppingRead, PermShoppingCreateIntent},
		CreatedAt:   time.Now(),
	}
	if !a.HasPermission(PermShoppingRead) {
		t.Error("expected granted permission to be present")
	}
	if a.HasPermission(PermShoppingExecute) {
		t.Error("expected ungranted permission to be absent")
	}
}

func TestHasPermission_RevokedAgentHasNone(t *testing.T) {
	revokedAt := time.Now()
	a := &Identity{
		ID:          "agent_1",
		Permissions: []Permission{PermShoppingRead, PermShoppingExecute},
		CreatedAt:   time.Now().Add(-time.Hour),
		RevokedAt:   &revokedAt,
	}
	if a.HasPermission(PermShoppingRead) {
		t.Error("a revoked agent must not retain any permission")
	}
	if !a.IsRevoked() {
		t.Error("expected IsRevoked to be true")
	}
}

func TestPermissionValid(t *testing.T) {
	if !PermShoppingExecute.Valid() {
		t.Error("expected known permission to be valid")
	}
	if Permission("shopping.delete_everything").Valid() {
		t.Error("expected unknown permission to be invalid")
	}
}
