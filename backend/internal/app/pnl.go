package app

import (
	"context"
	"fmt"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/jobpass"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// Task and agent P&L: what each task and each agent's spending came to.
//
// Cost is known exactly: what was paid (committed), what of that was paid for
// nothing usable (wasted), how many answers were accepted, and what purchase
// memory saved by handing back an answer already paid for. Value is not
// something Algebra can know: the agent that opened a task, or its person,
// states it (jobpass.Job.ValueMinor), and only then is there a profit.

// TaskPnL is one task's money, its sub-tasks included.
type TaskPnL struct {
	ValueMinor *int64 `json:"value_minor,omitempty"`
	// PaidMinor: payments committed under the task.
	PaidMinor int64 `json:"paid_minor"`
	// WastedMinor: the part of PaidMinor that bought nothing usable.
	WastedMinor int64 `json:"wasted_minor"`
	Accepted    int   `json:"accepted"`
	// FailedFree: attempts that ended without anything paid (a provider down,
	// a refusal before payment, a payment proven never to have landed).
	FailedFree           int   `json:"failed_free"`
	CostPerAcceptedMinor int64 `json:"cost_per_accepted_minor"`
	// SavedMinor: what reused answers would have cost again.
	SavedMinor int64 `json:"saved_minor"`
	Reused     int   `json:"reused"`
	// ProfitMinor: value less what was paid, when a value was stated.
	ProfitMinor *int64 `json:"profit_minor,omitempty"`
}

// Saving is what purchase memory saved one job.
type Saving struct {
	Minor int64
	Count int
}

func (p *TaskPnL) finish() {
	if p.Accepted > 0 {
		p.CostPerAcceptedMinor = p.PaidMinor / int64(p.Accepted)
	}
	if p.ValueMinor != nil {
		v := *p.ValueMinor - p.PaidMinor
		p.ProfitMinor = &v
	}
}

// add counts one attempt.
func (p *TaskPnL) add(a JobAttempt) {
	switch {
	case a.State == econ.ReservationCommitted:
		p.PaidMinor += a.HoldMinor
		switch a.Fulfillment {
		case econ.FulfillmentFulfilled:
			p.Accepted++
		case econ.FulfillmentNotFulfilled:
			p.WastedMinor += a.HoldMinor
		}
	case a.State == econ.ReservationReleased || a.State == econ.ReservationExpired:
		p.FailedFree++
	}
}

// PassMoney is what a store sums for one agent's pass since a time.
type PassMoney struct {
	PaidMinor, WastedMinor int64
	Accepted, FailedFree   int
	SavedMinor             int64
	Reused                 int
	// DuplicatesBlocked: attempts refused because the outcome was held,
	// unresolved or already bought; DuplicatePreventedMinor is their intents'
	// ceilings, an upper bound of what a second payment could have cost.
	DuplicatesBlocked       int
	DuplicatePreventedMinor int64
}

// PnLStore sums an agent's money.
type PnLStore interface {
	PassPnL(ctx context.Context, passID string, since time.Time) (*PassMoney, error)
}

// AgentPnL is one agent's money over a period, and its tasks'.
type AgentPnL struct {
	PassID string    `json:"spend_pass_id"`
	Days   int       `json:"days"`
	Since  time.Time `json:"since"`

	PaidMinor               int64 `json:"paid_minor"`
	WastedMinor             int64 `json:"wasted_minor"`
	Accepted                int   `json:"accepted"`
	FailedFree              int   `json:"failed_free"`
	CostPerAcceptedMinor    int64 `json:"cost_per_accepted_minor"`
	SavedMinor              int64 `json:"saved_minor"`
	Reused                  int   `json:"reused"`
	DuplicatesBlocked       int   `json:"duplicates_blocked"`
	DuplicatePreventedMinor int64 `json:"duplicate_prevented_minor"`

	// Tasks are the Job Passes opened in the period (at most maxPnLTasks);
	// those with a stated value add up to ValueMinor, cost ValuedCostMinor,
	// and leave ProfitMinor.
	Tasks           int   `json:"tasks"`
	ValuedTasks     int   `json:"valued_tasks"`
	ValueMinor      int64 `json:"value_minor"`
	ValuedCostMinor int64 `json:"valued_cost_minor"`
	ProfitMinor     int64 `json:"profit_minor"`
}

// maxPnLTasks bounds the tasks read for an agent's P&L.
const maxPnLTasks = 50

// SetPnL attaches the store an agent's P&L is summed from.
func (s *JobPassService) SetPnL(p PnLStore) { s.pnl = p }

// SetValue records what a task is worth (nil clears it).
func (s *JobPassService) SetValue(ctx context.Context, jobID string, value *int64) (*JobView, error) {
	if value != nil {
		if err := jobpass.CheckValue(*value); err != nil {
			return nil, fmt.Errorf("%w: %s", ErrInvalidRequest, err.Error())
		}
	}
	if err := s.store.SetJobValue(ctx, jobID, value); err != nil {
		return nil, err
	}
	return s.View(ctx, jobID)
}

// AgentPnL is a pass's money over the last days.
func (s *JobPassService) AgentPnL(ctx context.Context, userID, passID string, days int) (*AgentPnL, error) {
	if days <= 0 || days > 366 {
		days = 30
	}
	p, err := s.passes.Get(ctx, passID)
	if err != nil || p.UserID != userID {
		return nil, fmt.Errorf("%w: pass %s", shared.ErrNotFound, passID)
	}
	since := s.now().Add(-time.Duration(days) * 24 * time.Hour)
	out := &AgentPnL{PassID: p.ID, Days: days, Since: since.UTC()}
	if s.pnl != nil {
		m, err := s.pnl.PassPnL(ctx, p.ID, since)
		if err != nil {
			return nil, err
		}
		out.PaidMinor, out.WastedMinor, out.Accepted, out.FailedFree = m.PaidMinor, m.WastedMinor, m.Accepted, m.FailedFree
		out.SavedMinor, out.Reused, out.DuplicatesBlocked, out.DuplicatePreventedMinor = m.SavedMinor, m.Reused, m.DuplicatesBlocked, m.DuplicatePreventedMinor
		if m.Accepted > 0 {
			out.CostPerAcceptedMinor = m.PaidMinor / int64(m.Accepted)
		}
	}
	jobs, err := s.List(ctx, userID, p.ID, maxPnLTasks)
	if err != nil {
		return nil, err
	}
	for _, j := range jobs {
		if j.CreatedAt.Before(since) {
			continue
		}
		out.Tasks++
		if j.PnL.ValueMinor != nil {
			out.ValuedTasks++
			out.ValueMinor += *j.PnL.ValueMinor
			out.ValuedCostMinor += j.PnL.PaidMinor
		}
	}
	out.ProfitMinor = out.ValueMinor - out.ValuedCostMinor
	return out, nil
}
