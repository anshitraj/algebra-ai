package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

func TestAgentService_Revoke_OwnerSucceeds(t *testing.T) {
	store := newFakeAgentStore()
	store.put(&agent.Identity{ID: "agent_1", UserID: "user_1", ClientID: "test", Name: "test", CreatedAt: time.Now()})
	svc := NewAgentService(store)

	if err := svc.Revoke(context.Background(), "user_1", "agent_1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, err := store.Get(context.Background(), "agent_1")
	if err != nil {
		t.Fatalf("unexpected error re-fetching: %v", err)
	}
	if !got.IsRevoked() {
		t.Error("expected agent_1 to be revoked")
	}
}

// TestAgentService_Revoke_CrossUserDenied is the regression test for the
// bug found while wiring up a browser frontend: revokeAgent previously
// checked no identity at all, so anyone who knew (or guessed) an agent_id
// could revoke it. See internal/api/v1/agents.go and this method's doc
// comment.
func TestAgentService_Revoke_CrossUserDenied(t *testing.T) {
	store := newFakeAgentStore()
	store.put(&agent.Identity{ID: "agent_1", UserID: "user_1", ClientID: "test", Name: "test", CreatedAt: time.Now()})
	svc := NewAgentService(store)

	err := svc.Revoke(context.Background(), "user_2", "agent_1")
	if err == nil {
		t.Fatal("expected an error when user_2 tries to revoke user_1's agent")
	}
	if !errors.Is(err, shared.ErrUnauthorized) {
		t.Errorf("expected shared.ErrUnauthorized, got %v", err)
	}
	got, getErr := store.Get(context.Background(), "agent_1")
	if getErr != nil {
		t.Fatalf("unexpected error re-fetching: %v", getErr)
	}
	if got.IsRevoked() {
		t.Error("agent_1 must still be active — the cross-user revoke must not have taken effect")
	}
}

func TestAgentService_Revoke_UnknownAgentNotFound(t *testing.T) {
	store := newFakeAgentStore()
	svc := NewAgentService(store)

	err := svc.Revoke(context.Background(), "user_1", "does-not-exist")
	if !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("expected shared.ErrNotFound, got %v", err)
	}
}
