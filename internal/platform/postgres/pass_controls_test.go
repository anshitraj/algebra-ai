package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
)

func passOf(t *testing.T, f *econFixture, agentID string) *spendpass.Pass {
	t.Helper()
	p, err := NewSpendPassRepo(f.db).GetByAgent(context.Background(), agentID)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPassControls_KillSwitchAndControlsRoundTrip(t *testing.T) {
	f := newEconFixture(t, 2, 10_000_000)
	ctx := context.Background()
	repo := NewSpendPassRepo(f.db)

	p := passOf(t, f, f.agents[0])
	if p.FrozenAt != nil || p.Controls.NewProviders != spendpass.NewProvidersCap || p.Controls.MaxCallsPerMinute != spendpass.DefaultMaxCallsPerMinute {
		t.Fatalf("a pass made before controls reads with the defaults: %+v", p.Controls)
	}
	in := f.intent(t, `{"mint":"FRZ"}`, 50_000)
	now := time.Now().UTC()
	if n, err := repo.SetFrozenAll(ctx, f.userID, &now); err != nil || n != 2 {
		t.Fatalf("freeze all: %d %v", n, err)
	}
	if n, _ := repo.SetFrozenAll(ctx, f.userID, &now); n != 0 {
		t.Fatalf("freezing twice changes nothing, changed %d", n)
	}
	if p = passOf(t, f, f.agents[0]); p.FrozenAt == nil {
		t.Fatal("not frozen")
	}
	// A frozen pass can't reserve.
	_, err := f.svc.Reserve(ctx, f.agents[0], in.ID, app.ReserveRequest{ProviderID: "x402:risk.example", Rail: "sandbox", QuoteMinor: 3_000})
	if err == nil {
		t.Fatal("a frozen pass reserved")
	}
	if n, err := repo.SetFrozenAll(ctx, f.userID, nil); err != nil || n != 2 {
		t.Fatalf("thaw all: %d %v", n, err)
	}

	c := spendpass.Controls{MaxCallsPerMinute: 1, MaxCallsPerProviderPerMinute: 1, NewProviders: spendpass.NewProvidersAllow}
	if err := repo.SetControls(ctx, p.ID, c); err != nil {
		t.Fatal(err)
	}
	if got := passOf(t, f, f.agents[0]).Controls; got.MaxCallsPerMinute != 1 || got.NewProviders != spendpass.NewProvidersAllow {
		t.Fatalf("controls: %+v", got)
	}
}

func TestPassControls_VelocityAndProviderPaidOnPostgres(t *testing.T) {
	f := newEconFixture(t, 1, 10_000_000)
	ctx := context.Background()
	a := f.agents[0]
	p := passOf(t, f, a)
	if err := NewSpendPassRepo(f.db).SetControls(ctx, p.ID, spendpass.Controls{MaxCallsPerMinute: 1, NewProviders: spendpass.NewProvidersAllow}); err != nil {
		t.Fatal(err)
	}
	paid, err := f.repo.ProviderPaidBy(ctx, f.userID, "x402:risk.example")
	if err != nil || paid {
		t.Fatalf("nothing paid yet: %v %v", paid, err)
	}

	in := f.intent(t, `{"mint":"V1"}`, 50_000)
	r, err := f.svc.Reserve(ctx, a, in.ID, app.ReserveRequest{ProviderID: "x402:risk.example", Rail: "sandbox", QuoteMinor: 3_000})
	if err != nil {
		t.Fatal(err)
	}
	if r, err = f.svc.Begin(ctx, a, in.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.AuthorizePayment(ctx, a, in.ID, r.ID, app.PaymentRequest{}); err != nil {
		t.Fatal(err)
	}
	if v, err := f.svc.Complete(ctx, a, in.ID, r.ID, app.CompletionReport{Outcome: econ.OutcomeFulfilled, Evidence: econ.Evidence{ResultHash: "sha256:r"}}); err != nil || v.State != econ.StateCommitted {
		t.Fatalf("commit: %v", err)
	}
	if paid, _ = f.repo.ProviderPaidBy(ctx, f.userID, "x402:risk.example"); !paid {
		t.Fatal("the committed provider must read as paid")
	}
	if n, _ := f.repo.PassAttemptsRecent(ctx, p.ID, "", time.Now().Add(-time.Minute)); n != 1 {
		t.Fatalf("attempts in the last minute: %d", n)
	}

	// A second outcome in the same minute is over the limit of one.
	in2 := f.intent(t, `{"mint":"V2"}`, 50_000)
	_, err = f.svc.Reserve(ctx, a, in2.ID, app.ReserveRequest{ProviderID: "x402:risk.example", Rail: "sandbox", QuoteMinor: 3_000})
	var rej *app.ReservationRejected
	if !errors.As(err, &rej) || rej.Reason != app.RejectRateLimited {
		t.Fatalf("want rate_limited, got %v", err)
	}
}

func TestHealthRepo_SaveAndRead(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	repo := NewHealthRepo(db)
	id := "cand_test_" + time.Now().Format("150405.000000")
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM provider_health WHERE candidate_id = $1`, id)
	})
	h := app.EndpointHealth{CandidateID: id, Provider: "payai:test", Capability: "token.price", Endpoint: "https://x.example/p", Network: "solana",
		Status: app.HealthUp, HTTPStatus: 402, LatencyMS: 210, ListedPriceMinor: 1_000, LivePriceMinor: 2_000, Overcharges: true,
		Checks: 3, Ups: 2, Failures: 0, CheckedAt: time.Now().UTC().Truncate(time.Microsecond)}
	if err := repo.SaveHealth(ctx, h); err != nil {
		t.Fatal(err)
	}
	h.Checks, h.Ups = 4, 3
	if err := repo.SaveHealth(ctx, h); err != nil {
		t.Fatal(err)
	}
	got, err := repo.HealthFor(ctx, []string{id})
	if err != nil || got[id].Checks != 4 || !got[id].Overcharges || got[id].LatencyMS != 210 || !got[id].CheckedAt.Equal(h.CheckedAt) {
		t.Fatalf("read back: %v %+v", err, got[id])
	}
	list, err := repo.HealthForCapability(ctx, "token.price")
	if err != nil || len(list) == 0 {
		t.Fatalf("by capability: %v %d", err, len(list))
	}
}
