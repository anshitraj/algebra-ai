package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/jobpass"
)

// jobRig is the coordinator with Job Passes over one memory store, and a
// clock the test moves.
type jobRig struct {
	*econRig
	jobs  *JobPassService
	clock time.Time
	n     int
}

func newJobRig(t *testing.T, budget int64) *jobRig {
	t.Helper()
	rig := &jobRig{econRig: newEconRig(t, 1, budget), clock: time.Now()}
	now := func() time.Time { return rig.clock }
	rig.svc.now = now
	rig.jobs = NewJobPassService(rig.store, rig.passes, nil)
	rig.jobs.now = now
	rig.jobs.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	return rig
}

func (rig *jobRig) open(t *testing.T, parent string, budget int64) *JobView {
	t.Helper()
	v, err := rig.jobs.Open(context.Background(), OpenJob{UserID: "user_1", PassID: "pass_agent_1", ParentID: parent, CreatedBy: "agent_1",
		Spec: jobpass.Spec{Label: "Research a token", BudgetMinor: budget}, Delegable: true})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// newIntent is a fresh outcome (a new input each time) with a 0.05 ceiling.
func (rig *jobRig) newIntent(t *testing.T) string {
	t.Helper()
	rig.n++
	v, _, err := rig.svc.CreateIntent(context.Background(), "agent_1", econ.Spec{
		Capability: "company-report", Input: json.RawMessage(fmt.Sprintf(`{"n":%d}`, rig.n)), Currency: "USDC", BudgetMaxMinor: 50_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	return v.ID
}

func (rig *jobRig) reserve(job string, intentID string, quote int64) (*econ.Reservation, error) {
	return rig.svc.Reserve(context.Background(), "agent_1", intentID, ReserveRequest{ProviderID: "x402:reports", Rail: "sandbox", QuoteMinor: quote, JobID: job})
}

// pay runs one attempt under a job to a fulfilled commitment of quote.
func (rig *jobRig) pay(t *testing.T, job string, quote int64) {
	t.Helper()
	ctx := context.Background()
	id := rig.newIntent(t)
	r, err := rig.reserve(job, id, quote)
	if err != nil {
		t.Fatal(err)
	}
	if r, err = rig.svc.Begin(ctx, "agent_1", id, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.svc.AuthorizePayment(ctx, "agent_1", id, r.ID, PaymentRequest{}); err != nil {
		t.Fatal(err)
	}
	rig.rail.land(r, quote)
	if _, err := rig.svc.Complete(ctx, "agent_1", id, r.ID, CompletionReport{Outcome: econ.OutcomeFulfilled}); err != nil {
		t.Fatal(err)
	}
}

func (rig *jobRig) passExposure(t *testing.T) int64 {
	t.Helper()
	n, err := rig.store.PassSpend(context.Background(), "pass_agent_1", rig.clock.Add(-48*time.Hour), rig.clock)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (rig *jobRig) view(t *testing.T, id string) *JobView {
	t.Helper()
	v, err := rig.jobs.View(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func deniedFor(err error, code string) bool {
	var rej *ReservationRejected
	if errors.As(err, &rej) && slices.Contains(rej.ReasonCodes, code) {
		return true
	}
	var den *AuthorityDenied
	return errors.As(err, &den) && slices.Contains(den.ReasonCodes, code)
}

func TestJobPass_SetsItsBudgetAsideAllOrNothing(t *testing.T) {
	rig := newJobRig(t, 100_000)
	a := rig.open(t, "", 60_000)
	if got := rig.passExposure(t); got != 60_000 {
		t.Fatalf("an open job sets its whole budget aside in the pass: want 60000, got %d", got)
	}
	_, err := rig.jobs.Open(context.Background(), OpenJob{UserID: "user_1", PassID: "pass_agent_1", Spec: jobpass.Spec{Label: "Too big", BudgetMinor: 50_000}})
	var short *JobShort
	if !errors.As(err, &short) || short.LeftMinor != 40_000 || short.WantedMinor != 50_000 {
		t.Fatalf("a job the pass can't cover isn't opened, and says what is left: %v", err)
	}
	rig.open(t, "", 40_000)
	// Nothing is left outside the jobs: a payment straight from the pass is refused.
	if _, err := rig.reserve("", rig.newIntent(t), 3_000); !deniedFor(err, "PASS_BUDGET_EXCEEDED") {
		t.Fatalf("the pass's budget is all set aside, so a payment outside the jobs is refused: %v", err)
	}
	// Inside a job it goes ahead.
	rig.pay(t, a.ID, 3_000)
	if got := rig.passExposure(t); got != 100_000 {
		t.Errorf("a job's own spend is inside its budget, never counted twice: want 100000, got %d", got)
	}
}

func TestJobPass_SpendsInsideItsBudgetAndReturnsTheRest(t *testing.T) {
	rig := newJobRig(t, 100_000)
	j := rig.open(t, "", 10_000)
	rig.pay(t, j.ID, 3_000)
	v := rig.view(t, j.ID)
	if v.PaidMinor != 3_000 || v.LeftMinor != 7_000 || len(v.Attempts) != 1 || v.Attempts[0].Capability != "company-report" {
		t.Fatalf("want 3000 paid, 7000 left, one attempt: %+v", v)
	}
	if _, err := rig.reserve(j.ID, rig.newIntent(t), 8_000); !deniedFor(err, jobpass.ReasonBudgetExceeded) {
		t.Fatalf("a hold past what the job has left is refused: %v", err)
	}
	ended, err := rig.jobs.End(context.Background(), j.ID, jobpass.Closed, "finished")
	if err != nil {
		t.Fatal(err)
	}
	if ended.State != jobpass.Closed || ended.ReturnedMinor != 7_000 || ended.LeftMinor != 0 || ended.Live {
		t.Fatalf("closing returns what the job didn't use: %+v", ended)
	}
	if got := rig.passExposure(t); got != 3_000 {
		t.Errorf("after the job, the pass counts only what it paid: want 3000, got %d", got)
	}
	if _, err := rig.reserve(j.ID, rig.newIntent(t), 1_000); !deniedFor(err, jobpass.ReasonClosed) {
		t.Errorf("a closed job authorizes nothing: %v", err)
	}
	// Ending it again changes nothing.
	if again, err := rig.jobs.End(context.Background(), j.ID, jobpass.Revoked, "late"); err != nil || again.State != jobpass.Closed {
		t.Errorf("an ended job stays as it ended: %v %+v", err, again)
	}
}

func TestJobPass_SubJobsNestInsideTheirParent(t *testing.T) {
	rig := newJobRig(t, 100_000)
	a := rig.open(t, "", 10_000)
	b := rig.open(t, a.ID, 6_000)
	if b.Depth != 2 || b.ParentID != a.ID {
		t.Fatalf("a sub-job sits one level under its parent: %+v", b.Job)
	}
	_, err := rig.jobs.Open(context.Background(), OpenJob{UserID: "user_1", PassID: "pass_agent_1", ParentID: a.ID, Spec: jobpass.Spec{Label: "c", BudgetMinor: 5_000}})
	var short *JobShort
	if !errors.As(err, &short) || short.LeftMinor != 4_000 {
		t.Fatalf("a sub-job comes out of what its parent has left (4000): %v", err)
	}
	rig.pay(t, b.ID, 2_000)
	if v := rig.view(t, a.ID); v.SetAsideMinor != 6_000 || v.LeftMinor != 4_000 || len(v.Children) != 1 || v.Children[0].PaidMinor != 2_000 {
		t.Fatalf("a live sub-job counts in its parent by its whole budget: %+v", v)
	}
	if _, err := rig.jobs.End(context.Background(), b.ID, jobpass.Closed, "finished"); err != nil {
		t.Fatal(err)
	}
	if v := rig.view(t, a.ID); v.SetAsideMinor != 0 || v.PaidMinor != 2_000 || v.LeftMinor != 8_000 || v.Children[0].ReturnedMinor != 4_000 {
		t.Fatalf("an ended sub-job gives its parent back what it didn't use: %+v", v)
	}
	if got := rig.passExposure(t); got != 10_000 {
		t.Errorf("while the top job is open the pass counts its budget, not its parts: want 10000, got %d", got)
	}
	c := rig.open(t, a.ID, 1_000)
	d := rig.open(t, c.ID, 500)
	if _, err := rig.jobs.Open(context.Background(), OpenJob{UserID: "user_1", PassID: "pass_agent_1", ParentID: d.ID, Spec: jobpass.Spec{Label: "e", BudgetMinor: 100}}); !errors.Is(err, ErrJobDepth) {
		t.Errorf("delegation stops at %d levels: %v", jobpass.MaxDepth, err)
	}
	// Closing the top closes everything under it; the pass keeps only what was paid.
	if _, err := rig.jobs.End(context.Background(), a.ID, jobpass.Revoked, "stopped by owner"); err != nil {
		t.Fatal(err)
	}
	if v := rig.view(t, a.ID); v.Children[1].State != jobpass.Revoked || v.Children[1].Children[0].State != jobpass.Revoked {
		t.Errorf("revoking a job revokes the jobs carved from it: %+v", v.Children)
	}
	if got := rig.passExposure(t); got != 2_000 {
		t.Errorf("want only the 2000 paid left on the pass, got %d", got)
	}
}

func TestJobPass_SubAgentsRacingShareOneBudget(t *testing.T) {
	rig := newJobRig(t, 1_000_000)
	j := rig.open(t, "", 10_000)
	var ids []string
	for i := 0; i < 8; i++ {
		ids = append(ids, rig.newIntent(t))
	}
	var granted atomic.Int64
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if _, err := rig.reserve(j.ID, id, 3_000); err == nil {
				granted.Add(1)
			}
		}(id)
	}
	wg.Wait()
	if granted.Load() != 3 {
		t.Errorf("0.01 of job budget covers three 0.003 holds, got %d", granted.Load())
	}
}

func TestJobPass_ExpiryAndRevocationStopPayments(t *testing.T) {
	ctx := context.Background()
	rig := newJobRig(t, 100_000)
	v, err := rig.jobs.Open(ctx, OpenJob{UserID: "user_1", PassID: "pass_agent_1", Spec: jobpass.Spec{Label: "Quick", BudgetMinor: 20_000, Lifetime: time.Minute}})
	if err != nil {
		t.Fatal(err)
	}
	if !v.ExpiresAt.Equal(rig.clock.Add(time.Minute).UTC()) {
		t.Fatalf("expires a minute from now, got %s", v.ExpiresAt)
	}
	rig.clock = rig.clock.Add(2 * time.Minute)
	if _, err := rig.reserve(v.ID, rig.newIntent(t), 1_000); !deniedFor(err, jobpass.ReasonExpired) {
		t.Fatalf("an expired job authorizes nothing: %v", err)
	}
	if got := rig.passExposure(t); got != 0 {
		t.Errorf("an expired job's budget is back in the pass at once, before any sweep: %d", got)
	}
	if n, err := rig.jobs.Sweep(ctx); err != nil || n != 1 || rig.view(t, v.ID).State != jobpass.Expired {
		t.Errorf("the sweep writes EXPIRED: %d %v", n, err)
	}

	// Revoked between reserving and beginning: nothing starts.
	j := rig.open(t, "", 20_000)
	id := rig.newIntent(t)
	r, err := rig.reserve(j.ID, id, 2_000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rig.jobs.End(ctx, j.ID, jobpass.Revoked, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.svc.Begin(ctx, "agent_1", id, r.ID); !deniedFor(err, jobpass.ReasonRevoked) {
		t.Fatalf("a revoked job's attempt doesn't begin: %v", err)
	}

	// Revoked after beginning, before the payment is signed: no payment authority.
	k := rig.open(t, "", 20_000)
	id = rig.newIntent(t)
	if r, err = rig.reserve(k.ID, id, 2_000); err != nil {
		t.Fatal(err)
	}
	if r, err = rig.svc.Begin(ctx, "agent_1", id, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.jobs.End(ctx, k.ID, jobpass.Revoked, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.svc.AuthorizePayment(ctx, "agent_1", id, r.ID, PaymentRequest{}); !deniedFor(err, jobpass.ReasonRevoked) {
		t.Fatalf("no payment is signed under a revoked job: %v", err)
	}
	if rig.rail.authorized.Load() != 0 {
		t.Error("the rail was never asked to sign")
	}
}

func TestJobPass_LimitsComeFromTheParent(t *testing.T) {
	ctx := context.Background()
	rig := newJobRig(t, 100_000)
	p := rig.passes.passes["pass_agent_1"]
	p.ExpiresAt = rig.clock.Add(10 * time.Minute)
	parent := rig.open(t, "", 10_000)
	if !parent.ExpiresAt.Equal(p.ExpiresAt.UTC()) {
		t.Errorf("a job never outlives its pass: %s vs %s", parent.ExpiresAt, p.ExpiresAt)
	}
	if _, err := rig.jobs.Open(ctx, OpenJob{UserID: "user_2", PassID: "pass_agent_1", Spec: jobpass.Spec{Label: "x", BudgetMinor: 1}}); err == nil {
		t.Error("only the pass's owner opens a job under it")
	}
	if _, err := rig.jobs.Open(ctx, OpenJob{UserID: "user_1", PassID: "pass_agent_1", Spec: jobpass.Spec{Label: "x", BudgetMinor: 1, Lifetime: 48 * time.Hour}}); err == nil {
		t.Error("a job is for a task: at most a day")
	}
	now := rig.clock
	_ = rig.passes.SetFrozen(ctx, "pass_agent_1", &now)
	if _, err := rig.jobs.Open(ctx, OpenJob{UserID: "user_1", PassID: "pass_agent_1", Spec: jobpass.Spec{Label: "x", BudgetMinor: 1}}); !deniedFor(err, "PASS_FROZEN") {
		t.Errorf("a frozen pass opens no jobs: %v", err)
	}
	_ = rig.passes.SetFrozen(ctx, "pass_agent_1", nil)
	if _, err := rig.reserve(parent.ID, rig.newIntent(t), 1_000); err != nil {
		t.Fatalf("unfrozen, the job pays again: %v", err)
	}
}

func TestJobPass_TokenReachesItsJobAndBelowOnly(t *testing.T) {
	ctx := context.Background()
	rig := newJobRig(t, 100_000)
	a := rig.open(t, "", 10_000)
	if !strings.HasPrefix(a.Token, jobpass.TokenPrefix) || a.TokenHash == "" || a.TokenHash == a.Token {
		t.Fatalf("a delegable job gets a token shown once and kept hashed: %+v", a.Job)
	}
	if got := rig.view(t, a.ID); got.Token != "" {
		t.Error("the token is never shown again")
	}
	found, err := rig.jobs.ByToken(ctx, a.Token)
	if err != nil || found.ID != a.ID {
		t.Fatalf("the token finds its job: %v", err)
	}
	if _, err := rig.jobs.ByToken(ctx, "ajob_not-a-real-one"); err == nil {
		t.Error("an unknown token finds nothing")
	}
	child := rig.open(t, a.ID, 1_000)
	sibling := rig.open(t, "", 1_000)
	if ok, _ := rig.jobs.Within(ctx, found, child.ID); !ok {
		t.Error("a job's token reaches the jobs carved from it")
	}
	if ok, _ := rig.jobs.Within(ctx, found, sibling.ID); ok {
		t.Error("a job's token never reaches another job of the pass")
	}
}
