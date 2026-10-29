package v1

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/jobpass"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// Job Passes: a task's budget, set aside from a Spend Pass and shared by the
// agents that work on it (internal/app/job_passes.go).
//
// An agent opens a job with its own token and gets the job's token back,
// once. Handing that token to a sub-agent lets the sub-agent pay under the
// job, open smaller jobs inside it and close what it opened; nothing else.
// The console opens and ends jobs for the signed-in person.

func (a *API) jobPasses(w http.ResponseWriter) bool {
	if a.b.JobPasses == nil || a.b.SpendPasses == nil {
		writeJSON(w, http.StatusNotImplemented, errorBody{Error: "Job Passes are not enabled on this server"})
		return false
	}
	return true
}

// bearerJob is the job a request's bearer token was minted for, when the
// token is a job token (ok). A job token is never an agent's: resolveAgent
// refuses it, and it reaches only the job endpoints and execution.
func (a *API) bearerJob(r *http.Request) (job *jobpass.Job, ok bool, err error) {
	token, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !found || !jobpass.IsToken(token) {
		return nil, false, nil
	}
	if a.b.JobPasses == nil {
		return nil, true, unauthenticatedError{"Job Passes are not enabled on this server"}
	}
	j, err := a.b.JobPasses.ByToken(r.Context(), token)
	if err != nil {
		return nil, true, unauthenticatedError{"invalid job token"}
	}
	return j, true, nil
}

// spendCaller is who a payment runs as, and under which job.
type spendCaller struct {
	// ag is the authenticated agent; for a job token, the agent of the job's pass.
	ag       *agent.Identity
	executor string
	passID   string
	jobID    string
	// scope is the job a job token was minted for; nil for an agent.
	scope *jobpass.Job
}

// spendCallerFor resolves the caller of a spending request. A job token pays
// under its own job or a job carved from it, and as its pass's agent. An agent
// pays under its own pass (the console's agent: under the person's pass it
// names) and, when it names a job, only under a job of that pass. needPass
// asks for the agent's pass to be looked up even when no job is named.
func (a *API) spendCallerFor(r *http.Request, passID, jobID string, needPass bool) (*spendCaller, error) {
	ctx := r.Context()
	jobID = strings.TrimSpace(jobID)
	job, isJob, err := a.bearerJob(r)
	if err != nil {
		return nil, err
	}
	if isJob {
		target := job.ID
		if jobID != "" && jobID != job.ID {
			in, err := a.b.JobPasses.Within(ctx, job, jobID)
			if err != nil {
				return nil, err
			}
			if !in {
				return nil, fmt.Errorf("%w: job %s", shared.ErrNotFound, jobID)
			}
			target = jobID
		}
		p, err := a.b.SpendPasses.OwnedActive(ctx, job.UserID, job.PassID)
		if err != nil {
			return nil, err
		}
		ag, err := a.b.Agents.Get(ctx, p.AgentID)
		if err != nil || ag.IsRevoked() {
			return nil, unauthenticatedError{"the job's Spend Pass agent is no longer valid"}
		}
		return &spendCaller{ag: ag, executor: p.AgentID, passID: p.ID, jobID: target, scope: job}, nil
	}
	ag, err := a.resolveAgent(r)
	if err != nil {
		return nil, err
	}
	executor, err := a.executorFor(r, ag, passID)
	if err != nil {
		return nil, err
	}
	c := &spendCaller{ag: ag, executor: executor}
	if jobID == "" && (!needPass || a.b.SpendPasses == nil) {
		return c, nil
	}
	pv, err := a.b.SpendPasses.ForAgent(ctx, executor)
	if err != nil {
		if jobID == "" && errors.Is(err, shared.ErrNotFound) {
			return c, nil
		}
		return nil, err
	}
	c.passID = pv.ID
	if jobID != "" {
		if a.b.JobPasses == nil {
			return nil, fmt.Errorf("%w: Job Passes are not enabled on this server", shared.ErrNotImplemented)
		}
		j, err := a.b.JobPasses.Job(ctx, jobID)
		if err != nil || j.PassID != pv.ID {
			return nil, fmt.Errorf("%w: job %s", shared.ErrNotFound, jobID)
		}
		c.jobID = j.ID
	}
	return c, nil
}

// canSee reports whether the caller may read or end job id.
func (a *API) canSee(ctx context.Context, c *spendCaller, j *jobpass.Job) (bool, error) {
	if c.scope != nil {
		return a.b.JobPasses.Within(ctx, c.scope, j.ID)
	}
	return c.passID != "" && j.PassID == c.passID, nil
}

type openJobRequest struct {
	Label string `json:"label"`
	// BudgetMinor is in the pass's minor units (micro-USDC).
	BudgetMinor int64 `json:"budget_minor"`
	// ExpiresInSeconds: how long the task may take; an hour when omitted.
	ExpiresInSeconds int64 `json:"expires_in_seconds"`
	// ParentID carves the job from another job of the same pass.
	ParentID string `json:"parent_id"`
	// PassID: the console's agent, or the console, naming one of the person's passes.
	PassID string `json:"spend_pass_id"`
	// ValueMinor is what the task is worth, if the opener says: the other side
	// of its P&L.
	ValueMinor *int64 `json:"value_minor"`
}

func (req openJobRequest) spec() (jobpass.Spec, error) {
	if req.ExpiresInSeconds < 0 {
		return jobpass.Spec{}, badRequest("expires_in_seconds can't be negative")
	}
	return jobpass.Spec{Label: req.Label, BudgetMinor: req.BudgetMinor, Lifetime: time.Duration(req.ExpiresInSeconds) * time.Second, ValueMinor: req.ValueMinor}, nil
}

// writeJobError adds the reason codes a refusal carries, and what is left
// when the parent couldn't cover the job.
func writeJobError(w http.ResponseWriter, err error) {
	var short *app.JobShort
	if errors.As(err, &short) {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": err.Error(), "reason_codes": short.ReasonCodes(), "left_minor": short.LeftMinor, "wanted_minor": short.WantedMinor,
		})
		return
	}
	writeEconError(w, err)
}

// openJob opens a job for an agent (or for a sub-agent, under its own job).
// The response carries the job's token, once.
func (a *API) openJob(w http.ResponseWriter, r *http.Request) {
	if !a.jobPasses(w) {
		return
	}
	var req openJobRequest
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	spec, err := req.spec()
	if err != nil {
		writeError(w, err)
		return
	}
	c, err := a.spendCallerFor(r, req.PassID, "", true)
	if err != nil {
		writeError(w, err)
		return
	}
	if c.passID == "" {
		writeError(w, fmt.Errorf("%w: this agent has no Spend Pass to set a job's budget aside from", shared.ErrUnauthorized))
		return
	}
	parent := strings.TrimSpace(req.ParentID)
	if c.scope != nil {
		// A sub-agent's job comes out of the job it was handed, or one below it.
		if parent == "" {
			parent = c.scope.ID
		}
		if in, err := a.b.JobPasses.Within(r.Context(), c.scope, parent); err != nil || !in {
			writeError(w, fmt.Errorf("%w: job %s", shared.ErrNotFound, parent))
			return
		}
	}
	v, err := a.b.JobPasses.Open(r.Context(), app.OpenJob{
		UserID: c.ag.UserID, PassID: c.passID, ParentID: parent, CreatedBy: c.ag.ID, Spec: spec, Delegable: true,
	})
	if err != nil {
		writeJobError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (a *API) getJob(w http.ResponseWriter, r *http.Request) {
	if !a.jobPasses(w) {
		return
	}
	a.agentJob(w, r, func(j *jobpass.Job) (*app.JobView, error) { return a.b.JobPasses.View(r.Context(), j.ID) })
}

// closeJob ends a job the caller can reach, and every job beneath it: what
// they didn't use goes back.
func (a *API) closeJob(w http.ResponseWriter, r *http.Request) {
	if !a.jobPasses(w) {
		return
	}
	a.agentJob(w, r, func(j *jobpass.Job) (*app.JobView, error) {
		return a.b.JobPasses.End(r.Context(), j.ID, jobpass.Closed, "closed by agent")
	})
}

func (a *API) agentJob(w http.ResponseWriter, r *http.Request, do func(j *jobpass.Job) (*app.JobView, error)) {
	c, err := a.spendCallerFor(r, r.URL.Query().Get("spend_pass_id"), "", true)
	if err != nil {
		writeError(w, err)
		return
	}
	j, err := a.b.JobPasses.Job(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if ok, err := a.canSee(r.Context(), c, j); err != nil || !ok {
		writeError(w, fmt.Errorf("%w: job %s", shared.ErrNotFound, j.ID))
		return
	}
	v, err := do(j)
	if err != nil {
		writeJobError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// --- The console: the signed-in person's jobs ---

func (a *API) listMyJobs(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok || !a.jobPasses(w) {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	jobs, err := a.b.JobPasses.List(r.Context(), sess.UserID, strings.TrimSpace(r.URL.Query().Get("spend_pass_id")), limit)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

// openMyJob opens a job for one of the person's passes. The console pays
// under it itself, so no token is minted.
func (a *API) openMyJob(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok || !a.jobPasses(w) {
		return
	}
	var req openJobRequest
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	spec, err := req.spec()
	if err != nil {
		writeError(w, err)
		return
	}
	v, err := a.b.JobPasses.Open(r.Context(), app.OpenJob{UserID: sess.UserID, PassID: strings.TrimSpace(req.PassID), ParentID: strings.TrimSpace(req.ParentID), Spec: spec})
	if err != nil {
		writeJobError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (a *API) getMyJob(w http.ResponseWriter, r *http.Request) {
	a.myJob(w, r, func(id string) (*app.JobView, error) { return a.b.JobPasses.View(r.Context(), id) })
}

func (a *API) closeMyJob(w http.ResponseWriter, r *http.Request) {
	a.myJob(w, r, func(id string) (*app.JobView, error) {
		return a.b.JobPasses.End(r.Context(), id, jobpass.Closed, "finished")
	})
}

func (a *API) revokeMyJob(w http.ResponseWriter, r *http.Request) {
	a.myJob(w, r, func(id string) (*app.JobView, error) {
		return a.b.JobPasses.End(r.Context(), id, jobpass.Revoked, "stopped by owner")
	})
}

// setJobValue records what a task is worth: {value_minor} (null clears it).
// The agent that can reach a job may set it, and so may its person.
func (a *API) setJobValue(w http.ResponseWriter, r *http.Request) {
	if !a.jobPasses(w) {
		return
	}
	var req struct {
		ValueMinor *int64 `json:"value_minor"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	a.agentJob(w, r, func(j *jobpass.Job) (*app.JobView, error) {
		return a.b.JobPasses.SetValue(r.Context(), j.ID, req.ValueMinor)
	})
}

func (a *API) setMyJobValue(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ValueMinor *int64 `json:"value_minor"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	a.myJob(w, r, func(id string) (*app.JobView, error) { return a.b.JobPasses.SetValue(r.Context(), id, req.ValueMinor) })
}

// myPassPnL: GET /api/v1/me/passes/{id}/pnl?days=30. An agent's money over a
// period: paid, wasted, accepted answers, saved, and its tasks' P&L.
func (a *API) myPassPnL(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok || !a.jobPasses(w) {
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	out, err := a.b.JobPasses.AgentPnL(r.Context(), sess.UserID, r.PathValue("id"), days)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// replayMyJob: POST /api/v1/me/jobs/{id}/replay. The task's purchases run
// again, on paper, against sellers that misbehave, under the task's own pass.
// Nothing is contacted or paid.
func (a *API) replayMyJob(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok || !a.jobPasses(w) || !a.executionEnabled(w) {
		return
	}
	j, err := a.b.JobPasses.Job(r.Context(), r.PathValue("id"))
	if err != nil || j.UserID != sess.UserID {
		writeError(w, fmt.Errorf("%w: job %s", shared.ErrNotFound, r.PathValue("id")))
		return
	}
	p, err := a.b.SpendPasses.OwnedActive(r.Context(), sess.UserID, j.PassID)
	if err != nil {
		writeError(w, err)
		return
	}
	v, err := a.b.JobPasses.View(r.Context(), j.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	rep, err := a.b.Execution.Replay(ctx, v, p.AgentID, func(ctx context.Context, capability string) []routing.Candidate {
		cands, _ := a.b.Candidates.Resolve(ctx, capability, nil, nil)
		return cands
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (a *API) myJob(w http.ResponseWriter, r *http.Request, do func(id string) (*app.JobView, error)) {
	sess, ok := a.requireSession(w, r)
	if !ok || !a.jobPasses(w) {
		return
	}
	j, err := a.b.JobPasses.Job(r.Context(), r.PathValue("id"))
	if err != nil || j.UserID != sess.UserID {
		writeError(w, fmt.Errorf("%w: job %s", shared.ErrNotFound, r.PathValue("id")))
		return
	}
	v, err := do(j.ID)
	if err != nil {
		writeJobError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
