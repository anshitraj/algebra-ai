package v1

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
	"github.com/project-algebra/algebra/internal/platform/wiring"
)

type memPasses map[string]*spendpass.Pass

func (m memPasses) Create(_ context.Context, p *spendpass.Pass) error { m[p.ID] = p; return nil }
func (m memPasses) Get(_ context.Context, id string) (*spendpass.Pass, error) {
	if p, ok := m[id]; ok {
		return p, nil
	}
	return nil, shared.ErrNotFound
}
func (m memPasses) GetByAgent(context.Context, string) (*spendpass.Pass, error) {
	return nil, shared.ErrNotFound
}
func (m memPasses) ListByUser(context.Context, string) ([]spendpass.Pass, error) { return nil, nil }
func (m memPasses) Revoke(context.Context, string, time.Time) error              { return nil }

// Only the console's own agent may run a call under a pass it names, and only
// a pass of the same person that can still spend. Any other agent spends under
// its own pass.
func TestExecutorFor(t *testing.T) {
	now := time.Now()
	revoked := now.Add(-time.Minute)
	passes := memPasses{
		"pass_mine":    {ID: "pass_mine", UserID: "user-1", AgentID: "agent_of_pass", ExpiresAt: now.Add(time.Hour)},
		"pass_theirs":  {ID: "pass_theirs", UserID: "user-2", AgentID: "agent_theirs", ExpiresAt: now.Add(time.Hour)},
		"pass_revoked": {ID: "pass_revoked", UserID: "user-1", AgentID: "agent_old", ExpiresAt: now.Add(time.Hour), RevokedAt: &revoked},
	}
	a := &API{b: &wiring.Bundle{SpendPasses: app.NewSpendPassService(passes, nil, nil)}}
	console := &agent.Identity{ID: "agent_console", UserID: "user-1", ClientID: app.ConsoleAgentClientID}
	other := &agent.Identity{ID: "agent_bot", UserID: "user-1", ClientID: "spend-pass:custom"}
	r := httptest.NewRequest(http.MethodPost, "/", nil)

	if id, err := a.executorFor(r, other, ""); err != nil || id != "agent_bot" {
		t.Errorf("no pass named: the agent itself: %q %v", id, err)
	}
	if id, err := a.executorFor(r, console, "pass_mine"); err != nil || id != "agent_of_pass" {
		t.Errorf("the console names the person's own pass: %q %v", id, err)
	}
	if _, err := a.executorFor(r, other, "pass_mine"); !errors.Is(err, shared.ErrUnauthorized) {
		t.Errorf("another agent can't borrow a pass: %v", err)
	}
	if _, err := a.executorFor(r, console, "pass_theirs"); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("someone else's pass doesn't exist for you: %v", err)
	}
	if _, err := a.executorFor(r, console, "pass_revoked"); !errors.Is(err, shared.ErrUnauthorized) {
		t.Errorf("a revoked pass can't spend: %v", err)
	}
	if _, err := a.executorFor(r, console, "pass_missing"); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("no such pass: %v", err)
	}
}

func TestRailsNeedSomebodyAndListBothClusters(t *testing.T) {
	if rec := serve(t, &wiring.Bundle{}, "GET", "/api/v1/rails", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d", rec.Code)
	}
}
