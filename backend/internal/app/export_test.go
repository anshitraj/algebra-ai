package app

import (
	"testing"

	"github.com/project-algebra/algebra/internal/domain/receipt"
)

// This file is compiled only for tests. It lets end-to-end tests in package
// app_test (which may import providers that themselves import app) reach the
// in-memory rig the package's own tests use.

// NewTestCoordinator builds an EconomicService over in-memory stores with
// `agents` executors (agent_1, agent_2, ...) acting for user_1, each holding a
// USDC Spend Pass of the given budget, and a receipt signer. A fake rail named
// "sandbox" is registered; a test registers the real one over it.
func NewTestCoordinator(t *testing.T, agents int, budget int64) (*EconomicService, *receipt.Signer) {
	t.Helper()
	rig := newEconRig(t, agents, budget)
	return rig.svc, rig.signer
}

// NewMemExecStore is an in-memory ExecutionStore.
func NewMemExecStore() ExecutionStore { return newMemExecStore() }
