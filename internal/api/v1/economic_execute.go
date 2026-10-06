package v1

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// --- Execution: Algebra does it for the agent ---
//
//	POST /execute                                 ask for an outcome and have it done
//	POST /economic-intents/{id}/execute           run an intent that already exists
//	GET  /economic-intents/{id}/executions        what was tried, what it cost, how good it was
//
// The agent never touches a provider or a payment: Algebra reserves the
// intent, re-checks the Spend Pass, prices each candidate, pays through its
// own rail, verifies the result and signs a receipt.

// executeTimeout bounds one request. The work after money moves continues
// on a detached context, so hanging up never loses an outcome.
const executeTimeout = 80 * time.Second

type executeRequest struct {
	economicSpecRequest
	// Providers names providers the operator has configured.
	Providers []string `json:"providers"`
	// Candidates are endpoints the agent found itself. They are treated as
	// found on the open web whatever the agent says about them.
	Candidates []app.CandidateInput `json:"candidates"`
	// StoreResult is whether Algebra may keep the provider's answer for a
	// while (sealed, 24 hours by default) so that asking again returns it
	// instead of "already committed". True unless the request says false.
	StoreResult *bool `json:"store_result"`
}

func (a *API) executionEnabled(w http.ResponseWriter) bool {
	if a.b.Execution == nil || a.b.Economic == nil {
		writeJSON(w, http.StatusNotImplemented, errorBody{Error: "execution is not enabled on this server"})
		return false
	}
	return true
}

// executeOutcome asks for an outcome and has it done.
func (a *API) executeOutcome(w http.ResponseWriter, r *http.Request) {
	if !a.executionEnabled(w) {
		return
	}
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req executeRequest
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
	candidates, rejected := a.b.Candidates.Resolve(r.Context(), capability, req.Providers, req.Candidates)

	ctx, cancel := context.WithTimeout(r.Context(), executeTimeout)
	defer cancel()
	res, err := a.b.Execution.Do(ctx, app.DoRequest{AgentID: executor, Spec: req.spec(), Candidates: candidates, DiscardResult: discardsResult(req.StoreResult)})
	var created *bool
	var rep *app.PlanReport
	if res != nil {
		created, rep = &res.Created, res.Report
	}
	// Waiting for the person: say which intent, so they (or the console)
	// can approve it and then run it.
	var rej *app.ReservationRejected
	if errors.As(err, &rej) && rej.Reason == app.RejectApproval && res != nil && res.Intent != nil {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": err.Error(), "reason": rej.Reason, "intent_state": rej.State, "intent_id": res.Intent.ID,
			"budget_max_minor": res.Intent.BudgetMaxMinor, "capability": res.Intent.Capability,
		})
		return
	}
	a.writeExecution(w, created, rep, rejected, err)
}

// executorFor is the agent an execution runs as. An agent spends under its
// own Spend Pass. The one exception is the console's own agent, acting for the
// signed-in person in their browser (the console's chat): it may name one of
// that person's active passes and then acts as that pass's agent, with exactly
// that pass's limits. No other agent may borrow a pass.
func (a *API) executorFor(r *http.Request, ag *agent.Identity, passID string) (string, error) {
	passID = strings.TrimSpace(passID)
	if passID == "" {
		return ag.ID, nil
	}
	if ag.ClientID != app.ConsoleAgentClientID || a.b.SpendPasses == nil {
		return "", fmt.Errorf("%w: an agent spends under its own Spend Pass and can't name another", shared.ErrUnauthorized)
	}
	p, err := a.b.SpendPasses.OwnedActive(r.Context(), ag.UserID, passID)
	if err != nil {
		return "", err
	}
	return p.AgentID, nil
}

// executeEconomicIntent runs an intent that already exists, for instance one
// the person has just approved.
func (a *API) executeEconomicIntent(w http.ResponseWriter, r *http.Request) {
	if !a.executionEnabled(w) {
		return
	}
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req struct {
		Providers  []string             `json:"providers"`
		Candidates []app.CandidateInput `json:"candidates"`
		// PassID: the console's agent running an intent under one of the
		// person's passes (see executorFor).
		PassID string `json:"spend_pass_id"`
		// StoreResult: see executeRequest.
		StoreResult *bool `json:"store_result"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid request body"})
		return
	}
	executor, err := a.executorFor(r, ag, req.PassID)
	if err != nil {
		writeError(w, err)
		return
	}
	view, err := a.b.Economic.ViewFor(r.Context(), ag.UserID, r.PathValue("id"), false)
	if err != nil {
		writeError(w, err)
		return
	}
	candidates, rejected := a.b.Candidates.Resolve(r.Context(), view.Capability, req.Providers, req.Candidates)

	ctx, cancel := context.WithTimeout(r.Context(), executeTimeout)
	defer cancel()
	rep, err := a.b.Execution.ExecuteCandidates(ctx, app.CandidatesRequest{AgentID: executor, IntentID: view.ID, Candidates: candidates, DiscardResult: discardsResult(req.StoreResult)})
	a.writeExecution(w, nil, rep, rejected, err)
}

// writeExecution turns a plan run into a response:
//
//	200  delivered
//	202  money may have moved and the outcome is still being established
//	      (or the result is in the body but couldn't be recorded yet)
//	502  every provider tried failed, and nothing was paid
//	422  no candidate could be tried within the intent's limits
//	409 / 403  the intent can't be attempted (held, committed, approval needed, denied)
func (a *API) writeExecution(w http.ResponseWriter, created *bool, rep *app.PlanReport, pre []routing.Rejection, err error) {
	var nr *app.NoRoute
	switch {
	case errors.As(err, &nr):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": err.Error(), "rejected": append(append([]routing.Rejection{}, pre...), nr.Rejected...),
		})
		return
	case err != nil && (rep == nil || len(rep.Attempts) == 0):
		writeEconError(w, err)
		return
	case err != nil:
		// An attempt ran and its result is in hand, but the bookkeeping
		// behind it failed. Give the caller what it paid for.
		out := app.OutcomeOf(created, rep)
		out.Rejected = append(append([]routing.Rejection{}, pre...), out.Rejected...)
		writeJSON(w, http.StatusAccepted, map[string]any{
			"outcome": out,
			"warning": "the result was received but the outcome could not be fully recorded yet; Algebra will reconcile it",
		})
		return
	}
	out := app.OutcomeOf(created, rep)
	out.Rejected = append(append([]routing.Rejection{}, pre...), out.Rejected...)
	status := http.StatusBadGateway
	switch {
	case out.Delivered:
		status = http.StatusOK
	case out.PendingReconciliation:
		status = http.StatusAccepted
	case out.Intent != nil && out.Intent.State == econ.StateCommitted:
		// Paid, but the result wasn't usable or never arrived: the intent is
		// final and a repeat is blocked, so this isn't a gateway failure to retry.
		status = http.StatusOK
	}
	writeJSON(w, status, out)
}

// listEconomicExecutions lists an intent's attempts with their quality
// verdicts, for the person or for one of their agents.
func (a *API) listEconomicExecutions(w http.ResponseWriter, r *http.Request) {
	if !a.executionEnabled(w) {
		return
	}
	v, ok := a.econOwner(w, r)
	if !ok {
		return
	}
	recs, err := a.b.Execution.Executions(r.Context(), v.PrincipalID, v.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	if recs == nil {
		recs = []app.StoredExecution{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"intent_id": v.ID, "executions": recs})
}
