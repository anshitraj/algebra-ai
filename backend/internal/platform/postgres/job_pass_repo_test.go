package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/jobpass"
)

// The exposure SQL with Job Passes, on real Postgres: a live job counts by
// its budget, its own spend never twice, an ended job by what it used.
func TestJobPassRepo_ExposureCountsJobsOnce(t *testing.T) {
	f := newEconFixture(t, 1, 100_000)
	ctx := context.Background()
	a := f.agents[0]
	pass, err := NewSpendPassRepo(f.db).GetByAgent(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewJobPassRepo(f.db)
	jobs := app.NewJobPassService(repo, NewSpendPassRepo(f.db), nil)
	t.Cleanup(func() {
		_, _ = f.db.Pool.Exec(context.Background(), `DELETE FROM job_passes WHERE user_id = $1`, f.userID)
	})

	open := func(parent string, budget int64) *app.JobView {
		t.Helper()
		v, err := jobs.Open(ctx, app.OpenJob{UserID: f.userID, PassID: pass.ID, ParentID: parent, Spec: jobpass.Spec{Label: "task", BudgetMinor: budget}, Delegable: true})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	exposure := func() int64 {
		t.Helper()
		n, err := f.repo.PassSpend(ctx, pass.ID, time.Now().Add(-time.Hour), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	n := 0
	pay := func(job string, quote int64) {
		t.Helper()
		n++
		in := f.intent(t, fmt.Sprintf(`{"mint":"M%d"}`, n), 50_000)
		r, err := f.svc.Reserve(ctx, a, in.ID, app.ReserveRequest{ProviderID: "x402:risk.example", Rail: "sandbox", QuoteMinor: quote, JobID: job})
		if err != nil {
			t.Fatal(err)
		}
		if r, err = f.svc.Begin(ctx, a, in.ID, r.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.AuthorizePayment(ctx, a, in.ID, r.ID, app.PaymentRequest{}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.Complete(ctx, a, in.ID, r.ID, app.CompletionReport{Outcome: econ.OutcomeFulfilled}); err != nil {
			t.Fatal(err)
		}
	}

	top := open("", 60_000)
	if got := exposure(); got != 60_000 {
		t.Fatalf("a live job counts by its budget: want 60000, got %d", got)
	}
	if _, err := jobs.Open(ctx, app.OpenJob{UserID: f.userID, PassID: pass.ID, Spec: jobpass.Spec{Label: "big", BudgetMinor: 50_000}}); !isShort(err, 40_000) {
		t.Fatalf("want a refusal with 40000 left, got %v", err)
	}
	sub := open(top.ID, 20_000)
	pay(sub.ID, 3_000)
	pay(top.ID, 1_000)
	if got := exposure(); got != 60_000 {
		t.Errorf("spend inside the job tree is inside its budget: want 60000, got %d", got)
	}
	if aside, err := f.repo.PassSetAside(ctx, pass.ID, time.Now()); err != nil || aside != 56_000 {
		t.Fatalf("what the live job holds unused: 60000 less the 4000 paid in its tree, got %d %v", aside, err)
	}
	var used int64
	err = repo.WithPass(ctx, pass.ID, func(u app.JobUnit) error { used, err = u.JobExposure(top.ID, time.Now()); return err })
	if err != nil || used != 21_000 {
		t.Fatalf("the top job: 1000 its own + 20000 for the live sub-job, got %d %v", used, err)
	}
	if _, err := jobs.End(ctx, sub.ID, jobpass.Closed, "done"); err != nil {
		t.Fatal(err)
	}
	err = repo.WithPass(ctx, pass.ID, func(u app.JobUnit) error { used, err = u.JobExposure(top.ID, time.Now()); return err })
	if err != nil || used != 4_000 {
		t.Fatalf("an ended sub-job counts by what it used (3000) plus the top's 1000, got %d %v", used, err)
	}
	if v, err := jobs.View(ctx, top.ID); err != nil || v.LeftMinor != 56_000 || v.Children[0].ReturnedMinor != 17_000 || len(v.Children[0].Attempts) != 1 {
		t.Fatalf("view: %v %+v", err, v)
	}
	if _, err := jobs.End(ctx, top.ID, jobpass.Closed, "done"); err != nil {
		t.Fatal(err)
	}
	if got := exposure(); got != 4_000 {
		t.Errorf("after the jobs the pass counts what was paid: want 4000, got %d", got)
	}
	if j, err := jobs.ByToken(ctx, top.Token); err != nil || j.ID != top.ID {
		t.Errorf("the token finds the job: %v", err)
	}
}

// Many sub-agents paying under one job at once, on real Postgres: the pass's
// row lock lets exactly as many through as the job's budget covers.
func TestJobPassRepo_ConcurrentSpendFitsTheJob(t *testing.T) {
	f := newEconFixture(t, 1, 1_000_000)
	ctx := context.Background()
	a := f.agents[0]
	pass, err := NewSpendPassRepo(f.db).GetByAgent(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	jobs := app.NewJobPassService(NewJobPassRepo(f.db), NewSpendPassRepo(f.db), nil)
	t.Cleanup(func() {
		_, _ = f.db.Pool.Exec(context.Background(), `DELETE FROM job_passes WHERE user_id = $1`, f.userID)
	})
	j, err := jobs.Open(ctx, app.OpenJob{UserID: f.userID, PassID: pass.ID, Spec: jobpass.Spec{Label: "race", BudgetMinor: 10_000}})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := 0; i < 8; i++ {
		v, _, err := f.svc.CreateIntent(ctx, a, econ.Spec{Capability: "company-report", Input: json.RawMessage(fmt.Sprintf(`{"n":%d}`, i)), BudgetMaxMinor: 30_000})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, v.ID)
	}
	var granted atomic.Int64
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if _, err := f.svc.Reserve(ctx, a, id, app.ReserveRequest{ProviderID: "x402:reports", Rail: "sandbox", QuoteMinor: 3_000, JobID: j.ID}); err == nil {
				granted.Add(1)
			}
		}(id)
	}
	wg.Wait()
	if granted.Load() != 3 {
		t.Errorf("0.01 covers three 0.003 holds, got %d", granted.Load())
	}
}

func isShort(err error, left int64) bool {
	var short *app.JobShort
	return errors.As(err, &short) && short.LeftMinor == left
}
