package v1

import (
	"context"
	"net/http"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/econ"
)

// simulatePolicy: POST /api/v1/policy/simulate — "would this be allowed, and
// who would do it?". It takes exactly what POST /execute takes, plus
// live_quotes, and answers with the verdict (ALLOW, REQUIRE_APPROVAL or DENY),
// the reasons, the plan the router would follow and what each provider would
// meet. No intent is created, nothing is reserved and nothing is paid; with
// live_quotes the only thing that leaves Algebra is each provider's unpaid
// 402, the same request a quote is.
func (a *API) simulatePolicy(w http.ResponseWriter, r *http.Request) {
	if !a.executionEnabled(w) {
		return
	}
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req struct {
		executeRequest
		LiveQuotes bool `json:"live_quotes"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid request body"})
		return
	}
	capability, err := econ.NormalizeCapability(req.Capability)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: err.Error()})
		return
	}
	executor, err := a.executorFor(r, ag, req.PassID)
	if err != nil {
		writeError(w, err)
		return
	}
	if req.Window == "" {
		req.Window = "simulate"
	}
	candidates, rejected := a.b.Candidates.Resolve(r.Context(), capability, req.Providers, req.Candidates)
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	sim, err := a.b.Execution.Simulate(ctx, app.SimulateRequest{AgentID: executor, Spec: req.spec(), Candidates: candidates, LiveQuotes: req.LiveQuotes})
	if err != nil {
		writeError(w, err)
		return
	}
	sim.Rejected = append(rejected, sim.Rejected...)
	writeJSON(w, http.StatusOK, sim)
}
