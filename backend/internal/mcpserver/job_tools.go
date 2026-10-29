package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/project-algebra/algebra/internal/app"
	agentpkg "github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/jobpass"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/domain/tenancy"
)

// Job Passes over MCP (internal/domain/jobpass): an agent sets a task's budget
// aside from its Spend Pass before it starts, pays under it, hands the job's
// token to the sub-agents that work on the task, and closes it when it is done.
//
// A job token is a bearer token like a Spend Pass token, and it reaches only
// what REST gives it (internal/api/v1/jobs.go): paying under its job, opening
// smaller jobs inside it, reading and closing the jobs of its tree. Every other
// tool refuses it, and says so (resolveAgent).

// spendCaller is who a spending call runs as, and under which job.
type spendCaller struct {
	// ag is the authenticated agent; for a job token, the agent of the job's pass.
	ag       *agentpkg.Identity
	executor string
	passID   string
	jobID    string
	// scope is the job a job token was minted for; nil for an agent.
	scope *jobpass.Job
}

// spendCallerFor resolves the caller of a spending call. A job token pays under
// its own job, or a job carved from it, and as its pass's agent. An agent pays
// under its own pass and, when it names a job, only under a job of that pass.
// needPass asks for the agent's pass to be looked up even when no job is named.
func (srv *Server) spendCallerFor(ctx context.Context, agentToken, passID, jobID string, needPass bool, scope tenancy.Scope) (*spendCaller, error) {
	jobID = strings.TrimSpace(jobID)
	token := agentToken
	if token == "" {
		token = bearerFromContext(ctx)
	}
	if jobpass.IsToken(token) {
		return srv.jobTokenCaller(ctx, token, jobID)
	}
	ag, err := srv.resolveAgent(ctx, agentToken)
	if err != nil {
		return nil, err
	}
	executor, err := srv.executor(ctx, ag, passID, scope)
	if err != nil {
		return nil, err
	}
	c := &spendCaller{ag: ag, executor: executor}
	if jobID == "" && (!needPass || srv.SpendPasses == nil) {
		return c, nil
	}
	if srv.SpendPasses == nil {
		return nil, errors.New("spend passes are not enabled on this server")
	}
	pv, err := srv.SpendPasses.ForAgent(ctx, executor)
	if err != nil {
		if jobID == "" && errors.Is(err, shared.ErrNotFound) {
			return c, nil
		}
		return nil, err
	}
	c.passID = pv.ID
	if jobID != "" {
		if srv.JobPasses == nil {
			return nil, errors.New("job passes are not enabled on this server")
		}
		j, err := srv.JobPasses.Job(ctx, jobID)
		if err != nil || j.PassID != pv.ID {
			return nil, fmt.Errorf("%w: job %s", shared.ErrNotFound, jobID)
		}
		c.jobID = j.ID
	}
	return c, nil
}

// jobTokenCaller is the caller a job token stands for.
func (srv *Server) jobTokenCaller(ctx context.Context, token, jobID string) (*spendCaller, error) {
	if srv.JobPasses == nil || srv.SpendPasses == nil {
		return nil, errors.New("job passes are not enabled on this server")
	}
	job, err := srv.JobPasses.ByToken(ctx, token)
	if err != nil {
		return nil, errors.New("invalid job token")
	}
	target := job.ID
	if jobID != "" && jobID != job.ID {
		in, err := srv.JobPasses.Within(ctx, job, jobID)
		if err != nil {
			return nil, err
		}
		if !in {
			return nil, fmt.Errorf("%w: job %s", shared.ErrNotFound, jobID)
		}
		target = jobID
	}
	p, err := srv.SpendPasses.OwnedActive(ctx, job.UserID, job.PassID)
	if err != nil {
		return nil, err
	}
	ag, err := srv.Agents.Get(ctx, p.AgentID)
	if err != nil || ag.IsRevoked() {
		return nil, errors.New("the job's Spend Pass agent is no longer valid")
	}
	return &spendCaller{ag: ag, executor: p.AgentID, passID: p.ID, jobID: target, scope: job}, nil
}

// reachableJob is job id as far as the caller may read or end it: one of its
// pass's jobs, or, for a job token, one of the jobs of its tree. A job token
// that names none means its own.
func (srv *Server) reachableJob(ctx context.Context, c *spendCaller, id string) (*jobpass.Job, error) {
	id = strings.TrimSpace(id)
	if id == "" && c.scope != nil {
		id = c.scope.ID
	}
	if id == "" {
		return nil, errors.New("job_id is required: the job_id algebra_job_open returned")
	}
	j, err := srv.JobPasses.Job(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("%w: job %s", shared.ErrNotFound, id)
	}
	ok := c.passID != "" && j.PassID == c.passID
	if c.scope != nil {
		if ok, err = srv.JobPasses.Within(ctx, c.scope, j.ID); err != nil {
			return nil, err
		}
	}
	if !ok {
		return nil, fmt.Errorf("%w: job %s", shared.ErrNotFound, id)
	}
	return j, nil
}

// describeJobError says a refusal of a job in words an agent can act on.
func describeJobError(err error) error {
	var short *app.JobShort
	var denied *app.AuthorityDenied
	switch {
	case errors.As(err, &short):
		return fmt.Errorf("%s: only %s USDC is left to set aside and the job asked for %s USDC. Ask for less, or close a job you no longer need",
			jobpass.ReasonParentShort, chain.FormatUnits(short.LeftMinor, chain.USDCDecimals), chain.FormatUnits(short.WantedMinor, chain.USDCDecimals))
	case errors.As(err, &denied):
		return fmt.Errorf("the person's limits don't allow this (%s)", strings.Join(denied.ReasonCodes, ", "))
	case errors.Is(err, app.ErrInvalidRequest):
		return errors.New(strings.TrimPrefix(err.Error(), app.ErrInvalidRequest.Error()+": "))
	}
	return err
}

// usdcOf reads an optional amount of USDC: "" is none.
func usdcOf(field, s string) (*int64, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	v, err := chain.ParseUnits(s, chain.USDCDecimals)
	if err != nil || v < 0 {
		return nil, fmt.Errorf("%s must be an amount of USDC such as \"0.50\"", field)
	}
	if err := jobpass.CheckValue(v); err != nil {
		return nil, fmt.Errorf("%s: %w", field, err)
	}
	return &v, nil
}

type jobOpenInput struct {
	AgentToken  string `json:"agent_token,omitempty" jsonschema:"bearer token identifying the calling agent, or a job token when you are a sub-agent opening a smaller job inside the job you were handed; omit when the connection sends Authorization: Bearer"`
	Label       string `json:"label" jsonschema:"what the task is, in a few words, as the person will read it, such as \"Research three competitors\""`
	BudgetUSDC  string `json:"budget_usdc" jsonschema:"the most the task may spend, in USDC, as a decimal string such as \"0.50\". It is set aside from your Spend Pass (or from the parent job) now, all or nothing; what the task doesn't use goes back when it ends"`
	ExpiresIn   int64  `json:"expires_in_seconds,omitempty" jsonschema:"how long the task may take, in seconds; an hour when omitted. A job never outlives its Spend Pass or its parent"`
	ParentID    string `json:"parent_id,omitempty" jsonschema:"carve this job out of another job of the same Spend Pass instead of out of the pass itself. A sub-agent's jobs come out of the job it was handed"`
	ValueUSDC   string `json:"value_usdc,omitempty" jsonschema:"what the task is worth, in USDC, if you know: the person then sees the task's profit and loss (what it is worth minus what it paid). Never guess it; leave it out when you don't know"`
	SpendPassID string `json:"spend_pass_id,omitempty" jsonschema:"with an Algebra API key: the Spend Pass to set the budget aside from. Omit to use the newest active one"`
}

type jobRefInput struct {
	AgentToken  string `json:"agent_token,omitempty" jsonschema:"bearer token identifying the calling agent, or a job token; omit when the connection sends Authorization: Bearer"`
	JobID       string `json:"job_id,omitempty" jsonschema:"the job_id algebra_job_open returned. A sub-agent holding a job token may leave it out to mean its own job"`
	ValueUSDC   string `json:"value_usdc,omitempty" jsonschema:"only for algebra_job_close: what the task turned out to be worth, in USDC, if you know. Never guess it"`
	SpendPassID string `json:"spend_pass_id,omitempty" jsonschema:"with an Algebra API key: the Spend Pass the job belongs to. Omit to use the newest active one"`
}

func (srv *Server) registerJobTools(s *gomcp.Server) {
	gomcp.AddTool(s, &gomcp.Tool{
		Name: "algebra_job_open",
		Description: "Set a budget aside for one task, out of the Spend Pass you spend under, before you start it. Pay for the task by passing the job's `job_id` to algebra_execute: no payment under it can take more than the job has left, " +
			"and the task's cost, answers and profit and loss are kept together for the person to read. What the task doesn't use goes back when you close the job or its time runs out. " +
			"A job narrows the Spend Pass and never widens it: the person's own limits still apply to every payment under it. It moves no money: it is a slice of the pass's budget, not a wallet. " +
			"The answer carries the job's `token`, once. Give it to a sub-agent as its Authorization: Bearer token and the sub-agent can pay under the job, open smaller jobs inside it and close those, and nothing else; it never holds more than the job. " +
			"If the budget can't be covered the job isn't opened and the refusal says what is left. Amounts in the answer are in micro-USDC (1000000 is one USDC).",
	}, func(ctx context.Context, _ *gomcp.CallToolRequest, in jobOpenInput) (*gomcp.CallToolResult, map[string]any, error) {
		m, err := srv.openJob(ctx, in)
		return nil, m, err
	})
	gomcp.AddTool(s, &gomcp.Tool{
		Name: "algebra_job_status",
		Description: "Where a task's budget stands: what it was set at, what is paid, what is held by payments still settling, what is set aside for smaller jobs and what is left; the task's profit and loss; each payment made under it and its sub-jobs. " +
			"Use it to decide whether the task can afford another call. It moves no money. Amounts are in micro-USDC (1000000 is one USDC).",
	}, func(ctx context.Context, _ *gomcp.CallToolRequest, in jobRefInput) (*gomcp.CallToolResult, map[string]any, error) {
		m, err := srv.jobStatus(ctx, in)
		return nil, m, err
	})
	gomcp.AddTool(s, &gomcp.Tool{
		Name: "algebra_job_close",
		Description: "Finish a task. Whatever its budget and its sub-jobs' budgets didn't use goes back at once to where it came from; payments already under way keep their holds until they settle. " +
			"Say what the task turned out to be worth in `value_usdc` if you know, so the person sees its profit and loss. Closing a job that has already ended changes nothing. " +
			"Amounts in the answer are in micro-USDC (1000000 is one USDC).",
	}, func(ctx context.Context, _ *gomcp.CallToolRequest, in jobRefInput) (*gomcp.CallToolResult, map[string]any, error) {
		m, err := srv.closeJob(ctx, in)
		return nil, m, err
	})
}

func (srv *Server) jobsEnabled() error {
	if srv.JobPasses == nil || srv.SpendPasses == nil {
		return errors.New("job passes are not enabled on this server")
	}
	return nil
}

func (srv *Server) openJob(ctx context.Context, in jobOpenInput) (map[string]any, error) {
	if err := srv.jobsEnabled(); err != nil {
		return nil, err
	}
	budget, err := chain.ParseUnits(in.BudgetUSDC, chain.USDCDecimals)
	if err != nil || budget <= 0 {
		return nil, errors.New("budget_usdc must be a positive amount of USDC such as \"0.50\"")
	}
	if in.ExpiresIn < 0 {
		return nil, errors.New("expires_in_seconds can't be negative")
	}
	value, err := usdcOf("value_usdc", in.ValueUSDC)
	if err != nil {
		return nil, err
	}
	c, err := srv.spendCallerFor(ctx, in.AgentToken, in.SpendPassID, "", true, tenancy.ScopeExecute)
	if err != nil {
		return nil, err
	}
	if c.passID == "" {
		return nil, errors.New("this agent has no Spend Pass to set a job's budget aside from")
	}
	parent := strings.TrimSpace(in.ParentID)
	if c.scope != nil {
		// A sub-agent's job comes out of the job it was handed, or one below it.
		if parent == "" {
			parent = c.scope.ID
		}
		if within, err := srv.JobPasses.Within(ctx, c.scope, parent); err != nil || !within {
			return nil, fmt.Errorf("%w: job %s", shared.ErrNotFound, parent)
		}
	}
	v, err := srv.JobPasses.Open(ctx, app.OpenJob{
		UserID: c.ag.UserID, PassID: c.passID, ParentID: parent, CreatedBy: c.ag.ID, Delegable: true,
		Spec: jobpass.Spec{Label: in.Label, BudgetMinor: budget, Lifetime: time.Duration(in.ExpiresIn) * time.Second, ValueMinor: value},
	})
	if err != nil {
		return nil, describeJobError(err)
	}
	return toMap(v)
}

func (srv *Server) jobStatus(ctx context.Context, in jobRefInput) (map[string]any, error) {
	if err := srv.jobsEnabled(); err != nil {
		return nil, err
	}
	c, err := srv.spendCallerFor(ctx, in.AgentToken, in.SpendPassID, "", true, tenancy.ScopeRead)
	if err != nil {
		return nil, err
	}
	j, err := srv.reachableJob(ctx, c, in.JobID)
	if err != nil {
		return nil, err
	}
	v, err := srv.JobPasses.View(ctx, j.ID)
	if err != nil {
		return nil, err
	}
	return toMap(v)
}

func (srv *Server) closeJob(ctx context.Context, in jobRefInput) (map[string]any, error) {
	if err := srv.jobsEnabled(); err != nil {
		return nil, err
	}
	value, err := usdcOf("value_usdc", in.ValueUSDC)
	if err != nil {
		return nil, err
	}
	c, err := srv.spendCallerFor(ctx, in.AgentToken, in.SpendPassID, "", true, tenancy.ScopeExecute)
	if err != nil {
		return nil, err
	}
	j, err := srv.reachableJob(ctx, c, in.JobID)
	if err != nil {
		return nil, err
	}
	v, err := srv.JobPasses.End(ctx, j.ID, jobpass.Closed, "closed by agent")
	if err != nil {
		return nil, describeJobError(err)
	}
	if value != nil {
		if v, err = srv.JobPasses.SetValue(ctx, j.ID, value); err != nil {
			return nil, describeJobError(err)
		}
	}
	return toMap(v)
}
