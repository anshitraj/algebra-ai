package app

import (
	"context"
	"fmt"

	"github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/intent"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// requirePermission loads the agent and checks it holds perm and is not
// revoked. Every mutating entry point in every service calls this before
// doing anything else — there is no code path that trusts a caller-supplied
// claim about what an agent may do.
func requirePermission(ctx context.Context, agents AgentStore, agentID string, perm agent.Permission) (*agent.Identity, error) {
	ag, err := agents.Get(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if !ag.HasPermission(perm) {
		return nil, fmt.Errorf("%w: agent %s lacks permission %s", shared.ErrUnauthorized, agentID, perm)
	}
	return ag, nil
}

// requireOwnedIntent is requirePermission plus ownership: the intent must
// belong to the agent's own user. Without it, any agent holding a shopping
// permission could read, quote, cancel or execute ANY user's intent just by
// knowing its ID. A foreign intent reports ErrNotFound, not ErrUnauthorized,
// so the check never confirms that the ID exists.
//
// Ownership is per user, not per agent: every agent a user runs (each
// signed-in browser's console agent, an MCP client) can manage that user's
// intents.
func requireOwnedIntent(ctx context.Context, agents AgentStore, intents IntentStore, agentID, intentID string, perm agent.Permission) (*agent.Identity, *intent.PurchaseIntent, error) {
	ag, err := requirePermission(ctx, agents, agentID, perm)
	if err != nil {
		return nil, nil, err
	}
	pi, err := intents.Get(ctx, intentID)
	if err != nil {
		return nil, nil, err
	}
	if pi.UserID != ag.UserID {
		return nil, nil, fmt.Errorf("%w: intent %s", shared.ErrNotFound, intentID)
	}
	return ag, pi, nil
}
