package app

import (
	"context"
	"sort"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/jobpass"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// The memory store's Job Passes: the same rules as passExposureSQL and
// jobExposureSQL (postgres/job_pass_repo.go), written out in Go.

// covers is a set of jobs and everything carved from them.
func covers(jobs map[string]*jobpass.Job, roots []string) map[string]bool {
	out := map[string]bool{}
	queue := append([]string(nil), roots...)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if out[id] {
			continue
		}
		out[id] = true
		for _, j := range jobs {
			if j.ParentID == id {
				queue = append(queue, j.ID)
			}
		}
	}
	return out
}

func memPassExposure(rsv map[string]*econ.Reservation, jobs map[string]*jobpass.Job, passID string, since, now time.Time) int64 {
	var live []string
	for _, j := range jobs {
		if j.PassID == passID && j.Live(now) {
			live = append(live, j.ID)
		}
	}
	covered := covers(jobs, live)
	var total int64
	for _, x := range rsv {
		if x.ExecutorPassID != passID || (x.JobID != "" && covered[x.JobID]) {
			continue
		}
		if x.State.Live() || (x.State == econ.ReservationCommitted && x.FinishedAt != nil && !x.FinishedAt.Before(since)) {
			total += x.HoldMinor
		}
	}
	for _, id := range live {
		if j := jobs[id]; j.ParentID == "" || !covered[j.ParentID] {
			total += j.BudgetMinor
		}
	}
	return total
}

func memJobExposure(rsv map[string]*econ.Reservation, jobs map[string]*jobpass.Job, jobID string, now time.Time) int64 {
	sub := covers(jobs, []string{jobID})
	var live []string
	for id := range sub {
		if id != jobID && jobs[id].Live(now) {
			live = append(live, id)
		}
	}
	covered := covers(jobs, live)
	var total int64
	for _, x := range rsv {
		if sub[x.JobID] && !covered[x.JobID] && (x.State.Live() || x.State == econ.ReservationCommitted) {
			total += x.HoldMinor
		}
	}
	for _, id := range live {
		if !covered[jobs[id].ParentID] {
			total += jobs[id].BudgetMinor
		}
	}
	return total
}

type memJobUnit struct {
	m    *memEconStore
	jobs map[string]*jobpass.Job
}

func (m *memEconStore) WithPass(_ context.Context, _ string, fn func(u JobUnit) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := &memJobUnit{m: m, jobs: map[string]*jobpass.Job{}}
	if err := fn(u); err != nil {
		return err // nothing staged is applied
	}
	for id, j := range u.jobs {
		m.jobs[id] = j
	}
	return nil
}

// view is the store's jobs with this unit's staged writes over them.
func (u *memJobUnit) view() map[string]*jobpass.Job {
	all := map[string]*jobpass.Job{}
	for id, j := range u.m.jobs {
		all[id] = j
	}
	for id, j := range u.jobs {
		all[id] = j
	}
	return all
}

func (u *memJobUnit) PassExposure(passID string, since, now time.Time) (int64, error) {
	return memPassExposure(u.m.rsv, u.view(), passID, since, now), nil
}

func (u *memJobUnit) JobExposure(jobID string, now time.Time) (int64, error) {
	return memJobExposure(u.m.rsv, u.view(), jobID, now), nil
}

func (u *memJobUnit) Job(id string) (*jobpass.Job, error) {
	j, ok := u.view()[id]
	if !ok {
		return nil, shared.ErrNotFound
	}
	cp := *j
	return &cp, nil
}

func (u *memJobUnit) LiveJobs(passID string, now time.Time) (int, error) {
	n := 0
	for _, j := range u.view() {
		if j.PassID == passID && j.Live(now) {
			n++
		}
	}
	return n, nil
}

func (u *memJobUnit) InsertJob(j *jobpass.Job) error {
	cp := *j
	u.jobs[j.ID] = &cp
	return nil
}

func (u *memJobUnit) EndJobs(rootID string, state jobpass.State, reason string, now time.Time) (int, error) {
	all := u.view()
	n := 0
	for id := range covers(all, []string{rootID}) {
		j := *all[id]
		if j.State != jobpass.Active || !now.Before(j.ExpiresAt) {
			continue
		}
		t := now.UTC()
		j.State, j.CloseReason, j.ClosedAt = state, reason, &t
		u.jobs[id] = &j
		n++
	}
	return n, nil
}

func (m *memEconStore) Job(_ context.Context, id string) (*jobpass.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return nil, shared.ErrNotFound
	}
	cp := *j
	return &cp, nil
}

func (m *memEconStore) JobByTokenHash(_ context.Context, hash string) (*jobpass.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.TokenHash != "" && j.TokenHash == hash {
			cp := *j
			return &cp, nil
		}
	}
	return nil, shared.ErrNotFound
}

func (m *memEconStore) Jobs(_ context.Context, userID, passID string, limit int) ([]jobpass.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []jobpass.Job{}
	for _, j := range m.jobs {
		if j.UserID == userID && j.ParentID == "" && (passID == "" || j.PassID == passID) {
			out = append(out, *j)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].CreatedAt.After(out[b].CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *memEconStore) Subtree(_ context.Context, rootID string) ([]jobpass.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.jobs[rootID]; !ok {
		return []jobpass.Job{}, nil
	}
	out := []jobpass.Job{}
	for id := range covers(m.jobs, []string{rootID}) {
		out = append(out, *m.jobs[id])
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Depth < out[b].Depth })
	return out, nil
}

func (m *memEconStore) JobAttempts(_ context.Context, jobIDs []string, limit int) ([]JobAttempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	want := map[string]bool{}
	for _, id := range jobIDs {
		want[id] = true
	}
	out := []JobAttempt{}
	for _, r := range m.rsv {
		if !want[r.JobID] {
			continue
		}
		a := JobAttempt{ReservationID: r.ID, IntentID: r.IntentID, JobID: r.JobID, ProviderID: r.ProviderID, Rail: r.Rail,
			State: r.State, HoldMinor: r.HoldMinor, Transaction: r.Evidence.Transaction, Outcome: r.Outcome,
			CreatedAt: r.CreatedAt, FinishedAt: r.FinishedAt, ExecutorAgent: r.ExecutorAgentID}
		if in, ok := m.intents[r.IntentID]; ok {
			a.Capability, a.Fulfillment = in.Capability, in.Fulfillment
		}
		out = append(out, a)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].CreatedAt.After(out[b].CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *memEconStore) ExpireJobs(_ context.Context, now time.Time, _ int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, j := range m.jobs {
		if j.State == jobpass.Active && !now.Before(j.ExpiresAt) {
			t := j.ExpiresAt
			j.State, j.ClosedAt, j.CloseReason = jobpass.Expired, &t, "expired"
			n++
		}
	}
	return n, nil
}

func (m *memEconStore) SetJobValue(_ context.Context, id string, value *int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return shared.ErrNotFound
	}
	j.ValueMinor = value
	return nil
}

func (m *memEconStore) JobSavings(_ context.Context, jobIDs []string) (map[string]Saving, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	want := map[string]bool{}
	for _, id := range jobIDs {
		want[id] = true
	}
	out := map[string]Saving{}
	for _, e := range m.events {
		id, _ := e.Data["job_pass_id"].(string)
		if e.Event != "result.reused" || !want[id] {
			continue
		}
		s := out[id]
		s.Minor += savedOf(e)
		s.Count++
		out[id] = s
	}
	return out, nil
}

func savedOf(e EconEvent) int64 {
	switch v := e.Data["saved_minor"].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	}
	return 0
}

func (m *memEconStore) PassPnL(_ context.Context, passID string, since time.Time) (*PassMoney, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out PassMoney
	for _, r := range m.rsv {
		if r.ExecutorPassID != passID || r.CreatedAt.Before(since) {
			continue
		}
		in := m.intents[r.IntentID]
		var j JobAttempt
		j.State, j.HoldMinor = r.State, r.HoldMinor
		if in != nil {
			j.Fulfillment = in.Fulfillment
		}
		var p TaskPnL
		p.add(j)
		out.PaidMinor += p.PaidMinor
		out.WastedMinor += p.WastedMinor
		out.Accepted += p.Accepted
		out.FailedFree += p.FailedFree
	}
	for _, e := range m.events {
		if e.Event == "result.reused" && e.Data["spend_pass_id"] == passID && !e.CreatedAt.Before(since) {
			out.SavedMinor += savedOf(e)
			out.Reused++
		}
	}
	for _, in := range m.intents {
		if in.PassID == passID && !in.CreatedAt.Before(since) {
			out.DuplicatesBlocked += in.BlockedAttempts
			out.DuplicatePreventedMinor += int64(in.BlockedAttempts) * in.BudgetMaxMinor
		}
	}
	return &out, nil
}

var _ PnLStore = (*memEconStore)(nil)

var _ JobPassStore = (*memEconStore)(nil)
