package app

import (
	"context"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/agent"
)

func newCommerceProfileTestService() (*CommerceProfileService, *fakeAgentStore) {
	agents := newFakeAgentStore()
	store := newFakeCommerceProfileStore()
	return NewCommerceProfileService(store, agents), agents
}

func seedAgentWithPerms(agents *fakeAgentStore, id, userID string, perms ...agent.Permission) {
	agents.put(&agent.Identity{ID: id, UserID: userID, ClientID: "test", Name: "test", Permissions: perms, CreatedAt: time.Now()})
}

func TestCommerceProfileService_GetOrEmpty_NewUserReturnsEmptyNotError(t *testing.T) {
	svc, agents := newCommerceProfileTestService()
	seedAgentWithPerms(agents, "agent_1", "user_1", agent.PermProfilesRead)

	p, err := svc.GetOrEmpty(context.Background(), "agent_1")
	if err != nil {
		t.Fatalf("GetOrEmpty: %v", err)
	}
	if p.UserID != "user_1" {
		t.Errorf("UserID = %q, want user_1", p.UserID)
	}
	if len(p.Preferences) != 0 {
		t.Errorf("expected an empty profile, got %v", p.Preferences)
	}
}

func TestCommerceProfileService_GetOrEmpty_RequiresPermission(t *testing.T) {
	svc, agents := newCommerceProfileTestService()
	seedAgentWithPerms(agents, "agent_1", "user_1") // no permissions granted

	if _, err := svc.GetOrEmpty(context.Background(), "agent_1"); err == nil {
		t.Fatal("expected an error for an agent without profiles.read")
	}
}

func TestCommerceProfileService_SetPreferences_RequiresWritePermission(t *testing.T) {
	svc, agents := newCommerceProfileTestService()
	seedAgentWithPerms(agents, "agent_1", "user_1", agent.PermProfilesRead) // read only, no write

	if _, err := svc.SetPreferences(context.Background(), "agent_1", "clothing", map[string]any{"usual_size": "L"}); err == nil {
		t.Fatal("expected an error for an agent without profiles.write")
	}
}

func TestCommerceProfileService_SetPreferences_ThenGetReflectsIt(t *testing.T) {
	svc, agents := newCommerceProfileTestService()
	seedAgentWithPerms(agents, "agent_1", "user_1", agent.PermProfilesRead, agent.PermProfilesWrite)

	if _, err := svc.SetPreferences(context.Background(), "agent_1", "clothing", map[string]any{"usual_size": "L"}); err != nil {
		t.Fatalf("SetPreferences: %v", err)
	}
	p, err := svc.GetOrEmpty(context.Background(), "agent_1")
	if err != nil {
		t.Fatalf("GetOrEmpty: %v", err)
	}
	if got := p.Preferences["clothing"]["usual_size"]; got != "L" {
		t.Errorf("usual_size = %v, want L", got)
	}
}

func TestCommerceProfileService_SetPreferences_MergesAcrossCalls(t *testing.T) {
	svc, agents := newCommerceProfileTestService()
	seedAgentWithPerms(agents, "agent_1", "user_1", agent.PermProfilesRead, agent.PermProfilesWrite)

	if _, err := svc.SetPreferences(context.Background(), "agent_1", "clothing", map[string]any{"usual_size": "L"}); err != nil {
		t.Fatalf("first SetPreferences: %v", err)
	}
	if _, err := svc.SetPreferences(context.Background(), "agent_1", "clothing", map[string]any{"preferred_colors": []string{"black", "navy"}}); err != nil {
		t.Fatalf("second SetPreferences: %v", err)
	}

	p, err := svc.GetOrEmpty(context.Background(), "agent_1")
	if err != nil {
		t.Fatalf("GetOrEmpty: %v", err)
	}
	if got := p.Preferences["clothing"]["usual_size"]; got != "L" {
		t.Errorf("expected usual_size to survive a later merge into the same category, got %v", got)
	}
}

func TestCommerceProfileService_SetDefaultAliases(t *testing.T) {
	svc, agents := newCommerceProfileTestService()
	seedAgentWithPerms(agents, "agent_1", "user_1", agent.PermProfilesRead, agent.PermProfilesWrite)

	shipping := "shipping:home"
	if _, err := svc.SetDefaultAliases(context.Background(), "agent_1", &shipping, nil); err != nil {
		t.Fatalf("SetDefaultAliases: %v", err)
	}
	p, err := svc.GetOrEmpty(context.Background(), "agent_1")
	if err != nil {
		t.Fatalf("GetOrEmpty: %v", err)
	}
	if p.DefaultShippingAlias != "shipping:home" {
		t.Errorf("DefaultShippingAlias = %q, want shipping:home", p.DefaultShippingAlias)
	}
	if p.DefaultPaymentAlias != "" {
		t.Errorf("expected DefaultPaymentAlias to stay empty (nil pointer = unchanged), got %q", p.DefaultPaymentAlias)
	}
}

func TestCommerceProfileService_TwoUsersDoNotShareAProfile(t *testing.T) {
	svc, agents := newCommerceProfileTestService()
	seedAgentWithPerms(agents, "agent_1", "user_1", agent.PermProfilesRead, agent.PermProfilesWrite)
	seedAgentWithPerms(agents, "agent_2", "user_2", agent.PermProfilesRead, agent.PermProfilesWrite)

	if _, err := svc.SetPreferences(context.Background(), "agent_1", "clothing", map[string]any{"usual_size": "L"}); err != nil {
		t.Fatalf("SetPreferences for user_1: %v", err)
	}
	p2, err := svc.GetOrEmpty(context.Background(), "agent_2")
	if err != nil {
		t.Fatalf("GetOrEmpty for user_2: %v", err)
	}
	if _, ok := p2.Preferences["clothing"]; ok {
		t.Error("user_2's profile leaked user_1's clothing preferences")
	}
}
