package postgres

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/intel"
	"github.com/project-algebra/algebra/internal/domain/routing"
)

// fixtureCatalog is a catalog whose answer the test sets before each refresh.
type fixtureCatalog struct {
	name   string
	listed []intel.Listed
	err    error
}

func (f *fixtureCatalog) Name() string { return f.name }
func (f *fixtureCatalog) Snapshot(context.Context) ([]intel.Listed, bool, error) {
	return f.listed, false, f.err
}

// Demo E: the registry refresh against a stand-in catalog, on real Postgres.
// It sees a first listing, then a new endpoint, a price that triples and a
// payee that changes, then the catalog failing, answering with nothing, and
// dropping endpoints; and it checks what the payment-time rule says at each
// step.
func TestIntelRefresh_NewChangedAndRemovedEndpointsAndTheirTrust(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	run := uuid.NewString()[:8]
	provider := "fixture:" + run
	cat := &fixtureCatalog{name: "fixture-" + run}
	svc := app.NewIntelService(NewIntelRepo(db), cat)
	svc.Retries, svc.Backoff = 1, time.Millisecond
	now := time.Now().UTC().Truncate(time.Second)
	svc.SetClock(func() time.Time { return now })

	listing := func(path string, price int64, payTo string) intel.Listed {
		return intel.Listed{ProviderID: provider, ProviderName: "Fixture", Capability: "token.price", Method: "POST",
			URL: "https://" + run + ".example.test/" + path, Network: "solana-devnet", Protocol: "x402", Asset: "USDC",
			PriceMinor: price, PayTo: payTo, Callable: true}
	}
	quoteOf := func(l intel.Listed, payTo string) routing.Quote {
		return routing.Quote{Provider: l.ProviderID, Endpoint: l.URL, Network: l.Network, PayTo: payTo}
	}
	endpoint := func(l intel.Listed) intel.Endpoint {
		t.Helper()
		e, err := svc.Endpoint(ctx, l.ID())
		if err != nil {
			t.Fatalf("endpoint %s: %v", l.URL, err)
		}
		return e.Endpoint
	}
	changesOf := func(l intel.Listed, kind intel.ChangeKind) []intel.Change {
		t.Helper()
		ch, err := svc.Changes(ctx, app.ChangeFilter{EndpointID: l.ID(), Kind: string(kind), Limit: 20})
		if err != nil {
			t.Fatal(err)
		}
		return ch
	}
	refresh := func() app.SourceRun {
		t.Helper()
		r := svc.Sync(ctx, "test", map[string]app.SourceCheckpoint{})
		if len(r.Sources) != 1 {
			t.Fatalf("one catalog, one result: %+v", r)
		}
		return r.Sources[0]
	}

	// 1. The first listing: everything is new, unverified, with its payee pinned.
	a, b := listing("a", 1000, "payee-a-"+run), listing("b", 2000, "payee-b-"+run)
	cat.listed = []intel.Listed{a, b}
	if r := refresh(); !r.OK || r.Added != 2 || r.Seen != 2 {
		t.Fatalf("first refresh: %+v", r)
	}
	for _, l := range cat.listed {
		e := endpoint(l)
		if e.Trust != intel.TrustUnverified || e.PinnedPayTo != l.PayTo || e.Status != intel.StatusActive || e.VerifiedAt != nil {
			t.Fatalf("a new listing is unverified with its payee pinned: %+v", e)
		}
	}
	if refused := svc.CheckQuote(ctx, quoteOf(a, a.PayTo)); refused != nil {
		t.Fatalf("an unverified endpoint asking for its pinned payee is not refused by the registry: %v", refused)
	}

	// The same listing again records nothing.
	if r := refresh(); !r.OK || r.Added != 0 || r.Changed != 0 || r.Removed != 0 {
		t.Fatalf("an unchanged catalog changes nothing: %+v", r)
	}

	// 2. A new endpoint, a price that triples, a payee that changes.
	now = now.Add(6 * time.Hour)
	a2, b2, c := listing("a", 3000, a.PayTo), listing("b", 2000, "attacker-"+run), listing("c", 500, "payee-c-"+run)
	cat.listed = []intel.Listed{a2, b2, c}
	if r := refresh(); !r.OK || r.Added != 1 {
		t.Fatalf("second refresh: %+v", r)
	}
	if e := endpoint(c); e.Trust != intel.TrustUnverified {
		t.Fatalf("a listing never verifies itself: %+v", e)
	}
	if e := endpoint(a); e.ListedPriceMinor != 3000 || e.Trust != intel.TrustUnverified {
		t.Fatalf("the new price is recorded and trust is unchanged: %+v", e)
	}
	if ch := changesOf(a, intel.ChangePriceChanged); len(ch) != 1 || !ch[0].Suspicious {
		t.Fatalf("a price that triples is one suspicious change: %+v", ch)
	}
	eb := endpoint(b)
	if eb.Trust != intel.TrustBlocked || eb.TrustReason != intel.ReasonPayeeChanged || eb.PinnedPayTo != b.PayTo {
		t.Fatalf("a changed payee blocks the endpoint and keeps the pinned payee: %+v", eb)
	}
	if ch := changesOf(b, intel.ChangePayeeChanged); len(ch) != 1 || !ch[0].Suspicious {
		t.Fatalf("a changed payee is one suspicious change: %+v", ch)
	}
	// Nothing is paid to it: not at the new payee, and not at the old one either.
	for _, payTo := range []string{b2.PayTo, b.PayTo} {
		if refused := svc.CheckQuote(ctx, quoteOf(b, payTo)); refused == nil || refused.Code != intel.ReasonBlocked {
			t.Fatalf("a blocked endpoint is refused whatever it asks (%s): %v", payTo, refused)
		}
	}
	// An endpoint whose listing is unchanged but whose live quote names
	// another payee is refused at payment time, and that blocks it.
	if refused := svc.CheckQuote(ctx, quoteOf(c, "attacker-"+run)); refused == nil || refused.Code != intel.ReasonPayeeChanged {
		t.Fatalf("a quote naming another payee is refused: %v", refused)
	}
	if e := endpoint(c); e.Trust != intel.TrustBlocked || e.TrustReason != intel.ReasonPayeeChanged || e.PinnedPayTo != c.PayTo {
		t.Fatalf("the refused quote blocks the endpoint and keeps its pinned payee: %+v", e)
	}
	if refused := svc.CheckQuote(ctx, quoteOf(c, c.PayTo)); refused == nil || refused.Code != intel.ReasonBlocked {
		t.Fatalf("once blocked, even its pinned payee is not paid until a person unblocks it: %v", refused)
	}

	// 3. The catalog is down: its last good copy stays.
	cat.err = errors.New("fixture catalog is down")
	if r := refresh(); r.OK || r.Error == "" {
		t.Fatalf("a failed catalog is reported: %+v", r)
	}
	cat.err = nil
	// It answers with nothing: not believed.
	cat.listed = nil
	if r := refresh(); r.OK || !r.KeptLastGood {
		t.Fatalf("an empty answer keeps the last good copy: %+v", r)
	}
	for _, l := range []intel.Listed{a, b, c} {
		if e := endpoint(l); e.Status != intel.StatusActive {
			t.Fatalf("an unreachable or empty catalog removes nothing: %+v", e)
		}
	}

	// 4. A paid delivery to the pinned payee is what verifies.
	svc.ObserveDelivery(ctx, quoteOf(a, a.PayTo), 120)
	if e := endpoint(a); e.Trust != intel.TrustVerified || e.TrustReason != intel.ReasonPaidDelivery || e.TestedAt == nil {
		t.Fatalf("a delivery at the pinned payee verifies: %+v", e)
	}

	// 5. The catalog drops two endpoints: they are marked removed, never deleted.
	now = now.Add(6 * time.Hour)
	cat.listed = []intel.Listed{a2}
	if r := refresh(); !r.OK || r.Removed != 2 {
		t.Fatalf("third refresh: %+v", r)
	}
	for _, l := range []intel.Listed{b, c} {
		if e := endpoint(l); e.Status != intel.StatusRemoved {
			t.Fatalf("a dropped endpoint is marked removed: %+v", e)
		}
	}
	if e := endpoint(b); e.Trust != intel.TrustBlocked {
		t.Fatalf("leaving the catalog does not lift a block: %+v", e)
	}
	if ch := changesOf(b, intel.ChangePayeeChanged); len(ch) != 1 {
		t.Fatalf("history outlives the listing: %+v", ch)
	}
	if e := endpoint(a); e.Trust != intel.TrustVerified || e.Status != intel.StatusActive {
		t.Fatalf("what is still listed keeps its trust: %+v", e)
	}
}

// Several API instances share one refresh job: exactly one takes it.
func TestScheduledJob_ExactlyOneInstanceTakesTheLease(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	repo := NewIntelRepo(db)
	job := "test-job-" + uuid.NewString()
	due := time.Now().UTC().Truncate(time.Second)
	if err := repo.EnsureJob(ctx, job, intel.Schedule, due); err != nil {
		t.Fatal(err)
	}
	lease := 30 * time.Minute

	// Not due yet: nobody takes it.
	if won, err := repo.ClaimJob(ctx, job, "early", due.Add(-time.Minute), lease, false); err != nil || won {
		t.Fatalf("a job that isn't due is not claimed: %v %v", won, err)
	}

	// Sixteen instances wake at the same moment.
	var winners atomic.Int32
	var winner atomic.Value
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 16; i++ {
		owner := "instance-" + uuid.NewString()
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			won, err := repo.ClaimJob(ctx, job, owner, due.Add(time.Second), lease, false)
			if err != nil {
				t.Errorf("claim: %v", err)
			}
			if won {
				winners.Add(1)
				winner.Store(owner)
			}
		}()
	}
	close(start)
	wg.Wait()
	if n := winners.Load(); n != 1 {
		t.Fatalf("exactly one instance takes the lease, not %d", n)
	}
	owner := winner.Load().(string)

	// While it is held nobody else gets it, not even a person pressing Refresh.
	for _, force := range []bool{false, true} {
		if won, _ := repo.ClaimJob(ctx, job, "latecomer", due.Add(time.Minute), lease, force); won {
			t.Fatalf("a held lease is not taken (force=%v)", force)
		}
	}
	// Someone who doesn't hold the lease can't release it.
	next := due.Add(6 * time.Hour)
	if err := repo.FinishJob(ctx, job, "latecomer", due.Add(2*time.Minute), next, "", nil); err != nil {
		t.Fatal(err)
	}
	if j, err := repo.Job(ctx, job); err != nil || j.LeaseOwner != owner {
		t.Fatalf("the lease is still its owner's: %+v %v", j, err)
	}

	// The owner finishes: the next run is scheduled and the lease is free.
	if err := repo.FinishJob(ctx, job, owner, due.Add(3*time.Minute), next, "", nil); err != nil {
		t.Fatal(err)
	}
	j, err := repo.Job(ctx, job)
	if err != nil || j.LeaseOwner != "" || !j.NextRunAt.Equal(next) || j.LastSuccessAt == nil {
		t.Fatalf("after a run: %+v %v", j, err)
	}
	if won, _ := repo.ClaimJob(ctx, job, "eager", next.Add(-time.Second), lease, false); won {
		t.Fatal("the job does not run again before its next slot")
	}
	if won, _ := repo.ClaimJob(ctx, job, "second-run", next, lease, false); !won {
		t.Fatal("at its next slot the job is claimed again")
	}

	// An instance that dies holding the lease does not hold it for ever.
	if won, _ := repo.ClaimJob(ctx, job, "rescuer", next.Add(lease-time.Second), lease, true); won {
		t.Fatal("the lease is honoured until it expires")
	}
	if won, _ := repo.ClaimJob(ctx, job, "rescuer", next.Add(lease+time.Second), lease, true); !won {
		t.Fatal("an expired lease can be taken over")
	}
}
