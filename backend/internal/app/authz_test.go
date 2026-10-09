package app

import (
	"context"
	"errors"
	"testing"
	"time"

	agentpkg "github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/intent"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// Regression: intent-scoped calls used to check only that the calling agent
// held a permission, never that the intent was its own user's — any agent
// could read, cancel or execute any user's intent by ID.
func TestIntentAccess_IsScopedToTheAgentsUser(t *testing.T) {
	ctx := context.Background()
	intents := newFakeIntentStore()
	agents := newFakeAgentStore()
	all := []agentpkg.Permission{agentpkg.PermShoppingRead, agentpkg.PermShoppingExecute, agentpkg.PermOrdersRead, agentpkg.PermPolicyRead}
	agents.put(&agentpkg.Identity{ID: "agent_alice", UserID: "user_alice", Permissions: all})
	agents.put(&agentpkg.Identity{ID: "agent_alice_mcp", UserID: "user_alice", Permissions: all})
	agents.put(&agentpkg.Identity{ID: "agent_mallory", UserID: "user_mallory", Permissions: all})
	now := time.Now()
	_ = intents.Create(ctx, &intent.PurchaseIntent{
		ID: "intent_1", UserID: "user_alice", AgentID: "agent_alice", Status: intent.StateDraft,
		Items: []intent.Item{{Query: "coke", Quantity: 1}}, CreatedAt: now, UpdatedAt: now,
	})
	svc := NewIntentService(intents, agents, newFakeAuditLogger())

	if _, err := svc.GetIntent(ctx, "agent_mallory", "intent_1"); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("another user's agent read the intent: err=%v", err)
	}
	if _, err := svc.CancelIntent(ctx, "agent_mallory", "intent_1"); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("another user's agent cancelled the intent: err=%v", err)
	}
	if pi, _ := intents.Get(ctx, "intent_1"); pi.Status != intent.StateDraft {
		t.Fatalf("intent state changed by a foreign agent: %s", pi.Status)
	}
	// Any of the owner's own agents (another browser session, an MCP client) may.
	if _, err := svc.GetIntent(ctx, "agent_alice_mcp", "intent_1"); err != nil {
		t.Errorf("owner's second agent was refused: %v", err)
	}

	quotes := NewQuoteService(intents, agents, newFakeQuoteStore(), NewConnectorRegistry())
	if _, err := quotes.GetQuotes(ctx, "agent_mallory", "intent_1"); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("another user's agent listed quotes: err=%v", err)
	}
}
