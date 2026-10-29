package app

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/jobpass"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
)

// JobPassStore persists Job Passes (migrations/0031_job_passes.sql).
type JobPassStore interface {
	// WithPass runs fn with the Spend Pass row locked: the same lock every
	// reservation under the pass takes, so setting a job's budget aside and
	// spending from the pass happen in one order.
	WithPass(ctx context.Context, passID string, fn func(u JobUnit) error) error
	Job(ctx context.Context, id string) (*jobpass.Job, error)
	JobByTokenHash(ctx context.Context, hash string) (*jobpass.Job, error)
	// Jobs lists a person's top-level jobs, newest first: all of them, or one
	// pass's.
	Jobs(ctx context.Context, userID, passID string, limit int) ([]jobpass.Job, error)
	// Subtree is a job and every job carved from it, at any depth.
	Subtree(ctx context.Context, rootID string) ([]jobpass.Job, error)
	// JobAttempts are the reservations made under the given jobs, newest
	// first, with what their intents asked for.
	JobAttempts(ctx context.Context, jobIDs []string, limit int) ([]JobAttempt, error)
	// ExpireJobs writes EXPIRED on active jobs past their time. Nothing waits
	// on it: a job past its time already authorizes nothing.
	ExpireJobs(ctx context.Context, now time.Time, limit int) (int, error)
}

// JobUnit is the work done under a pass's lock.
type JobUnit interface {
	PassExposure(passID string, since, now time.Time) (int64, error)
	JobExposure(jobID string, now time.Time) (int64, error)
	Job(id string) (*jobpass.Job, error)
	// LiveJobs counts the pass's jobs live at now, at every depth.
	LiveJobs(passID string, now time.Time) (int, error)
	InsertJob(j *jobpass.Job) error
	// EndJobs moves a job and every job beneath it that is still ACTIVE to
	// state, and reports how many it moved.
	EndJobs(rootID string, state jobpass.State, reason string, now time.Time) (int, error)
}

// JobAttempt is one paid attempt under a job, for the task's details.
type JobAttempt struct {
	ReservationID string                `json:"reservation_id"`
	IntentID      string                `json:"intent_id"`
	JobID         string                `json:"job_pass_id"`
	Capability    string                `json:"capability"`
	ProviderID    string                `json:"provider_id"`
	Rail          string                `json:"rail,omitempty"`
	State         econ.ReservationState `json:"state"`
	HoldMinor     int64                 `json:"hold_minor"`
	Transaction   string                `json:"transaction,omitempty"`
	Network       string                `json:"network,omitempty"`
	Fulfillment   econ.Fulfillment      `json:"fulfillment,omitempty"`
	Outcome       econ.Outcome          `json:"outcome,omitempty"`
	CreatedAt     time.Time             `json:"created_at"`
	FinishedAt    *time.Time            `json:"finished_at,omitempty"`
	ExecutorAgent string                `json:"executor_agent_id,omitempty"`
}

// JobView is a job with where its budget stands.
type JobView struct {
	jobpass.Job
	Live bool `json:"live"`
	// PaidMinor: payments committed under the job and its ended sub-jobs.
	PaidMinor int64 `json:"paid_minor"`
	// HeldMinor: attempts under way that may still become payments.
	HeldMinor int64 `json:"held_minor"`
	// SetAsideMinor: the budgets of its live sub-jobs.
	SetAsideMinor int64 `json:"set_aside_minor"`
	// LeftMinor: what can still be spent or set aside under it (0 once it ended).
	LeftMinor int64 `json:"left_minor"`
	// ReturnedMinor: once it ended, what went back to its parent.
	ReturnedMinor int64        `json:"returned_minor"`
	Children      []JobView    `json:"children"`
	Attempts      []JobAttempt `json:"attempts"`
	// Token is the job's delegation token, present only in the answer that
	// opened it.
	Token string `json:"token,omitempty"`
}

// OpenJob asks for a job.
type OpenJob struct {
	UserID string
	PassID string
	// ParentID carves the job from another job instead of from the pass.
	ParentID string
	// CreatedBy is the agent opening it ("" for the person, in the console).
	CreatedBy string
	Spec      jobpass.Spec
	// Delegable mints a token the opener can hand to sub-agents.
	Delegable bool
}

// JobPassService opens, ends and reads Job Passes. What a job may spend is
// enforced in EconomicService.Reserve under the pass's lock; this sets the
// budgets aside and gives them back.
type JobPassService struct {
	store  JobPassStore
	passes SpendPassStore
	ledger PassSpendLedger
	log    *slog.Logger
	now    func() time.Time
}

func NewJobPassService(store JobPassStore, passes SpendPassStore, ledger PassSpendLedger) *JobPassService {
	return &JobPassService{store: store, passes: passes, ledger: ledger, log: slog.Default(), now: time.Now}
}

// SetLogger sets where job events are logged.
func (s *JobPassService) SetLogger(l *slog.Logger) { s.log = l }

// Errors a caller can tell apart.
var (
	ErrJobDepth = fmt.Errorf("%w: a job can be carved %d levels deep at most", shared.ErrConflict, jobpass.MaxDepth)
	ErrJobCount = fmt.Errorf("%w: this Spend Pass already has %d jobs open; close one first", shared.ErrConflict, jobpass.MaxActivePerPass)
)

// Open sets a job's budget aside from its parent, all of it or none: when the
// parent hasn't that much left the job isn't opened, and the refusal says
// what is left.
func (s *JobPassService) Open(ctx context.Context, req OpenJob) (*JobView, error) {
	spec, err := req.Spec.Normalize()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidRequest, err.Error())
	}
	now := s.now()
	p, err := s.passes.Get(ctx, req.PassID)
	if err != nil || p.UserID != req.UserID {
		return nil, fmt.Errorf("%w: pass %s", shared.ErrNotFound, req.PassID)
	}
	if !p.Active(now) {
		return nil, &AuthorityDenied{ReasonCodes: []string{spendpass.ReasonExpired}}
	}
	if p.FrozenAt != nil {
		return nil, &AuthorityDenied{ReasonCodes: []string{spendpass.ReasonFrozen}}
	}
	expires := now.Add(spec.Lifetime)
	if p.ExpiresAt.Before(expires) {
		expires = p.ExpiresAt
	}
	depth := 1
	if req.ParentID != "" {
		parent, err := s.store.Job(ctx, req.ParentID)
		if err != nil || parent.UserID != req.UserID || parent.PassID != p.ID {
			return nil, fmt.Errorf("%w: job %s", shared.ErrNotFound, req.ParentID)
		}
		if parent.Depth >= jobpass.MaxDepth {
			return nil, ErrJobDepth
		}
		depth = parent.Depth + 1
		if parent.ExpiresAt.Before(expires) {
			expires = parent.ExpiresAt
		}
	}
	ordersSpent := int64(0)
	if req.ParentID == "" && s.ledger != nil {
		if ordersSpent, err = s.ledger.SpentByAgentSince(ctx, p.AgentID, p.Currency, p.WindowStart(now)); err != nil {
			return nil, err
		}
	}
	j := &jobpass.Job{
		ID: newID("job"), UserID: req.UserID, PassID: p.ID, ParentID: req.ParentID, Depth: depth,
		Label: spec.Label, Currency: p.Currency, BudgetMinor: spec.BudgetMinor, State: jobpass.Active,
		CreatedBy: req.CreatedBy, CreatedAt: now.UTC(), ExpiresAt: expires.UTC(),
	}
	var token string
	if req.Delegable {
		if token, j.TokenHash, err = jobpass.NewToken(); err != nil {
			return nil, err
		}
	}
	var short *JobShort
	err = s.store.WithPass(ctx, p.ID, func(u JobUnit) error {
		n, err := u.LiveJobs(p.ID, now)
		if err != nil {
			return err
		}
		if n >= jobpass.MaxActivePerPass {
			return ErrJobCount
		}
		budget, used := p.BudgetMinorUnits, int64(0)
		if req.ParentID != "" {
			parent, err := u.Job(req.ParentID)
			if err != nil {
				return err
			}
			if code := parent.Refusal(now); code != "" {
				return &AuthorityDenied{ReasonCodes: []string{code}}
			}
			if used, err = u.JobExposure(parent.ID, now); err != nil {
				return err
			}
			budget = parent.BudgetMinor
		} else {
			exposure, err := u.PassExposure(p.ID, p.WindowStart(now), now)
			if err != nil {
				return err
			}
			used = ordersSpent + exposure
		}
		if used+j.BudgetMinor > budget {
			short = &JobShort{LeftMinor: max(budget-used, 0), WantedMinor: j.BudgetMinor}
			return nil
		}
		return u.InsertJob(j)
	})
	if err != nil {
		return nil, err
	}
	if short != nil {
		return nil, short
	}
	s.log.InfoContext(ctx, "job pass opened", "job", j.ID, "pass", p.ID, "parent", j.ParentID, "budget_minor", j.BudgetMinor, "expires_at", j.ExpiresAt)
	v, err := s.view(ctx, j.ID, now)
	if err != nil {
		return nil, err
	}
	v.Token = token
	return v, nil
}

// JobShort is the refusal of a job its parent can't cover.
type JobShort struct {
	LeftMinor   int64 `json:"left_minor"`
	WantedMinor int64 `json:"wanted_minor"`
}

func (e *JobShort) Error() string {
	return fmt.Sprintf("authority denied: %s (%d left, %d asked for)", jobpass.ReasonParentShort, e.LeftMinor, e.WantedMinor)
}

// ReasonCodes lets a JobShort be read like any other authority refusal.
func (e *JobShort) ReasonCodes() []string { return []string{jobpass.ReasonParentShort} }

// End closes or revokes a job and every job beneath it. What none of them
// used goes back to the parent at once; attempts already under way keep
// their holds until they settle. Ending a job that has already ended changes
// nothing.
func (s *JobPassService) End(ctx context.Context, jobID string, state jobpass.State, reason string) (*JobView, error) {
	if state != jobpass.Closed && state != jobpass.Revoked {
		return nil, fmt.Errorf("%w: a job is closed or revoked", ErrInvalidRequest)
	}
	j, err := s.store.Job(ctx, jobID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	var n int
	err = s.store.WithPass(ctx, j.PassID, func(u JobUnit) error {
		n, err = u.EndJobs(j.ID, state, reason, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	if n > 0 {
		s.log.InfoContext(ctx, "job pass ended", "job", j.ID, "state", state, "reason", reason, "jobs", n)
	}
	return s.view(ctx, j.ID, now)
}

// Job reads one job, for an owner check.
func (s *JobPassService) Job(ctx context.Context, id string) (*jobpass.Job, error) {
	return s.store.Job(ctx, id)
}

// ByToken is the job a delegation token was minted for.
func (s *JobPassService) ByToken(ctx context.Context, token string) (*jobpass.Job, error) {
	if !jobpass.IsToken(token) {
		return nil, shared.ErrNotFound
	}
	return s.store.JobByTokenHash(ctx, jobpass.HashToken(token))
}

// Within reports whether job id is root or one of the jobs carved from it:
// what a delegation token for root reaches.
func (s *JobPassService) Within(ctx context.Context, root *jobpass.Job, id string) (bool, error) {
	if id == root.ID {
		return true, nil
	}
	tree, err := s.store.Subtree(ctx, root.ID)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(tree, func(j jobpass.Job) bool { return j.ID == id }), nil
}

// View is a job with its sub-jobs and attempts.
func (s *JobPassService) View(ctx context.Context, id string) (*JobView, error) {
	return s.view(ctx, id, s.now())
}

// List is a person's recent top-level jobs with their trees: all of them, or
// one pass's.
func (s *JobPassService) List(ctx context.Context, userID, passID string, limit int) ([]JobView, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	jobs, err := s.store.Jobs(ctx, userID, passID, limit)
	if err != nil {
		return nil, err
	}
	now := s.now()
	out := make([]JobView, 0, len(jobs))
	for _, j := range jobs {
		v, err := s.view(ctx, j.ID, now)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, nil
}

// maxViewAttempts bounds the attempts read for one job tree.
const maxViewAttempts = 500

func (s *JobPassService) view(ctx context.Context, id string, now time.Time) (*JobView, error) {
	tree, err := s.store.Subtree(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(tree) == 0 {
		return nil, fmt.Errorf("%w: job %s", shared.ErrNotFound, id)
	}
	ids := make([]string, len(tree))
	for i, j := range tree {
		ids[i] = j.ID
	}
	attempts, err := s.store.JobAttempts(ctx, ids, maxViewAttempts)
	if err != nil {
		return nil, err
	}
	return buildJobView(id, tree, attempts, now), nil
}

// buildJobView assembles the tree under root and works out each job's
// numbers the way JobExposure counts them: a live sub-job by its whole
// budget, an ended one by what its attempts held or paid.
func buildJobView(root string, tree []jobpass.Job, attempts []JobAttempt, now time.Time) *JobView {
	byParent := map[string][]jobpass.Job{}
	var top *jobpass.Job
	for i := range tree {
		if tree[i].ID == root {
			top = &tree[i]
			continue
		}
		byParent[tree[i].ParentID] = append(byParent[tree[i].ParentID], tree[i])
	}
	byJob := map[string][]JobAttempt{}
	for _, a := range attempts {
		byJob[a.JobID] = append(byJob[a.JobID], a)
	}
	var build func(j jobpass.Job) JobView
	build = func(j jobpass.Job) JobView {
		v := JobView{Job: j, Live: j.Live(now), Children: []JobView{}, Attempts: byJob[j.ID]}
		v.State = j.StateAt(now)
		if v.Attempts == nil {
			v.Attempts = []JobAttempt{}
		}
		for _, a := range v.Attempts {
			switch {
			case a.State == econ.ReservationCommitted:
				v.PaidMinor += a.HoldMinor
			case a.State.Live():
				v.HeldMinor += a.HoldMinor
			}
		}
		for _, c := range byParent[j.ID] {
			cv := build(c)
			if cv.Live {
				v.SetAsideMinor += cv.BudgetMinor
			} else {
				v.PaidMinor += cv.PaidMinor
				v.HeldMinor += cv.HeldMinor
			}
			v.Children = append(v.Children, cv)
		}
		used := v.PaidMinor + v.HeldMinor + v.SetAsideMinor
		if v.Live {
			v.LeftMinor = max(v.BudgetMinor-used, 0)
		} else {
			v.ReturnedMinor = max(v.BudgetMinor-v.PaidMinor-v.HeldMinor, 0)
		}
		return v
	}
	v := build(*top)
	return &v
}

// Sweep writes EXPIRED on jobs whose time ran out.
func (s *JobPassService) Sweep(ctx context.Context) (int, error) {
	return s.store.ExpireJobs(ctx, s.now(), 500)
}
