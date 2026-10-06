package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/receipt"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
)

// sandboxRail settles every payment it authorized: enough to drive the
// real-database lifecycle without pretending to be a real network.
type sandboxRail struct{ settled sync.Map }

func (s *sandboxRail) Name() string { return "sandbox" }

func (s *sandboxRail) Authorize(_ context.Context, r *econ.Reservation, _ app.PaymentRequest) (*app.PaymentAuthority, error) {
	s.settled.Store("pay_"+r.ID, r.HoldMinor)
	return &app.PaymentAuthority{Header: "X-PAYMENT", Value: "sbx", AmountMinor: r.HoldMinor, Evidence: econ.Evidence{
		Rail: "sandbox", Protocol: "x402", Scheme: "exact", Network: "sandbox", Asset: "USDC", PaymentID: "pay_" + r.ID, AmountMinor: r.HoldMinor, Test: true,
	}}, nil
}

func (s *sandboxRail) Settlement(_ context.Context, ev econ.Evidence) (app.Settlement, error) {
	if v, ok := s.settled.Load(ev.PaymentID); ok {
		return app.Settlement{Status: app.SettlementSettled, AmountMinor: v.(int64), Transaction: "sbx_tx_" + ev.PaymentID, Test: true}, nil
	}
	return app.Settlement{Status: app.SettlementPending}, nil
}

type econFixture struct {
	db     *DB
	repo   *EconRepo
	svc    *app.EconomicService
	userID string
	agents []string
	signer *receipt.Signer
}

func newEconFixture(t *testing.T, executors int, budget int64) *econFixture {
	t.Helper()
	db := requireDB(t)
	ctx := context.Background()
	f := &econFixture{db: db, repo: NewEconRepo(db), userID: mustCreateUser(t, db)}
	passes := NewSpendPassRepo(db)
	agents := NewAgentRepo(db)
	now := time.Now()
	for i := 0; i < executors; i++ {
		_, hash, err := agent.GenerateToken()
		if err != nil {
			t.Fatal(err)
		}
		id := "agent_" + uuid.NewString()
		if err := agents.Create(ctx, &agent.Identity{ID: id, UserID: f.userID, ClientID: "spend-pass:custom", Name: fmt.Sprintf("executor-%d", i+1),
			Permissions: app.SpendPassPermissions, TokenHash: hash, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		if err := passes.Create(ctx, &spendpass.Pass{ID: "pass_" + uuid.NewString(), UserID: f.userID, AgentID: id, Label: "executor",
			AgentKind: spendpass.AgentCustom, Currency: "USDC", BudgetMinorUnits: budget, BudgetPeriod: spendpass.PeriodTotal,
			AllowedCategories: []string{}, AllowedMerchants: []string{},
			CreatedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		f.agents = append(f.agents, id)
	}
	f.svc = app.NewEconomicService(f.repo, agents, passes, nil)
	f.svc.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	f.svc.RegisterRail(&sandboxRail{})
	signer, err := receipt.NewSigner([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	f.signer = signer
	f.svc.SetReceipts(app.NewIntentReceiptService(signer, "https://algebra.test", agents, f.repo))
	t.Cleanup(func() {
		c := context.Background()
		_, _ = db.Pool.Exec(c, `DELETE FROM economic_intents WHERE principal_id = $1`, f.userID)
		_, _ = db.Pool.Exec(c, `DELETE FROM spend_passes WHERE user_id = $1`, f.userID)
	})
	return f
}

func (f *econFixture) intent(t *testing.T, input string, max int64) *app.IntentView {
	t.Helper()
	v, _, err := f.svc.CreateIntent(context.Background(), f.agents[0], econ.Spec{
		Capability: "solana.token-risk", Input: json.RawMessage(input), Window: "2026-09-29T10", BudgetMaxMinor: max,
	})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// The invariant, on real Postgres: many executors, one intent, exactly one
// reservation row, and every other attempt counted as blocked.
func TestEconRepo_ConcurrentExecutorsOneReservation(t *testing.T) {
	f := newEconFixture(t, 3, 10_000_000)
	ctx := context.Background()
	in := f.intent(t, `{"mint":"SOL"}`, 50_000)

	const racers = 24
	var granted, blocked atomic.Int64
	winners := sync.Map{}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			agentID := f.agents[i%len(f.agents)]
			r, err := f.svc.Reserve(ctx, agentID, in.ID, app.ReserveRequest{ProviderID: "x402:risk.example", Rail: "sandbox", QuoteMinor: 3_000})
			var rej *app.ReservationRejected
			switch {
			case err == nil:
				granted.Add(1)
				winners.Store(r.ID, agentID)
			case errors.As(err, &rej) && rej.Duplicate:
				blocked.Add(1)
			default:
				t.Errorf("unexpected: %v", err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	var rows, live int
	if err := f.db.Pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE state IN ('RESERVED','EXECUTING','UNKNOWN','RECONCILING'))
		FROM economic_reservations WHERE intent_id = $1`, in.ID).Scan(&rows, &live); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || live != 1 {
		t.Fatalf("exactly one reservation row may exist, got %d (live %d)", rows, live)
	}
	distinct := 0
	winners.Range(func(any, any) bool { distinct++; return true })
	if distinct != 1 {
		t.Errorf("every granted call must be the same reservation, got %d", distinct)
	}
	cur, err := f.repo.GetIntent(ctx, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if int64(cur.BlockedAttempts) != blocked.Load() || granted.Load()+blocked.Load() != racers {
		t.Errorf("blocked recorded=%d observed=%d granted=%d", cur.BlockedAttempts, blocked.Load(), granted.Load())
	}
	if cur.Attempts != 1 || cur.State != econ.StateReserved {
		t.Errorf("one attempt, RESERVED; got %d %s", cur.Attempts, cur.State)
	}
}

func TestEconRepo_ConcurrentCreatesOneIntent(t *testing.T) {
	f := newEconFixture(t, 3, 10_000_000)
	ctx := context.Background()
	ids := make(chan string, 15)
	var created atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 15; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, isNew, err := f.svc.CreateIntent(ctx, f.agents[i%3], econ.Spec{Capability: "solana.token-risk",
				Input: json.RawMessage(`{"mint":"SOL", "depth": 2}`), Window: "2026-09-29T11", BudgetMaxMinor: 50_000})
			if err != nil {
				t.Error(err)
				return
			}
			if isNew {
				created.Add(1)
			}
			ids <- v.ID
		}(i)
	}
	wg.Wait()
	close(ids)
	seen := map[string]bool{}
	for id := range ids {
		seen[id] = true
	}
	if len(seen) != 1 || created.Load() != 1 {
		t.Errorf("one outcome is one intent: %d ids, %d created", len(seen), created.Load())
	}
}

// Row locks on the pass stop two intents both fitting into the same
// remaining budget.
func TestEconRepo_PassBudgetSerializedAcrossIntents(t *testing.T) {
	f := newEconFixture(t, 1, 100_000)
	ctx := context.Background()
	var ids []string
	for i := 0; i < 6; i++ {
		ids = append(ids, f.intent(t, fmt.Sprintf(`{"n":%d}`, i), 30_000).ID)
	}
	var granted atomic.Int64
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if _, err := f.svc.Reserve(ctx, f.agents[0], id, app.ReserveRequest{ProviderID: "x402:reports", Rail: "sandbox"}); err == nil {
				granted.Add(1)
			}
		}(id)
	}
	wg.Wait()
	if granted.Load() != 3 {
		t.Errorf("0.10 of authority covers three 0.03 holds, got %d", granted.Load())
	}
}

func TestEconRepo_LifecycleCommitsWithReceipt(t *testing.T) {
	f := newEconFixture(t, 2, 10_000_000)
	ctx := context.Background()
	in := f.intent(t, `{"mint":"JUP"}`, 50_000)
	a := f.agents[0]
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
	// A fallback agent racing mid-execution is refused by the database.
	if _, err := f.svc.Reserve(ctx, f.agents[1], in.ID, app.ReserveRequest{ProviderID: "x402:other", Rail: "sandbox"}); err == nil {
		t.Fatal("fallback must be refused while an attempt executes")
	}
	v, err := f.svc.Complete(ctx, a, in.ID, r.ID, app.CompletionReport{Outcome: econ.OutcomeFulfilled, Evidence: econ.Evidence{ResultHash: "sha256:r"}})
	if err != nil {
		t.Fatal(err)
	}
	if v.State != econ.StateCommitted || v.Receipt == "" {
		t.Fatalf("want COMMITTED with receipt, got %s", v.State)
	}
	c, err := receipt.VerifyIntent(v.Receipt, f.signer.JWKS())
	if err != nil || c.Intent.Hash != v.IntentHash || c.Coordination.DuplicateCommitAttemptsBlocked != 1 {
		t.Fatalf("receipt: %v %+v", err, c)
	}
	events, err := f.repo.Events(ctx, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range events {
		names = append(names, e.Event)
	}
	want := []string{"intent.created", "authority.evaluated", "authority.evaluated", "reservation.acquired", "authority.rechecked",
		"execution.started", "payment.authorized", "reservation.rejected", "payment.confirmed", "intent.committed", "receipt.signed"}
	if fmt.Sprint(names) != fmt.Sprint(want) {
		t.Errorf("event order:\n got %v\nwant %v", names, want)
	}
	st, err := f.repo.Stats(ctx, f.userID, time.Now().Add(-time.Hour))
	if err != nil || st.Committed != 1 || st.SpentMinor != 3_000 || st.DuplicateAttemptsBlock != 1 || st.AuthorizedMinor != 3_000 {
		t.Errorf("stats: %v %+v", err, st)
	}
}

// A pass has one budget. What its agent committed through economic intents,
// or still holds, is what the pass's view and the shopping check count.
func TestEconRepo_PassSpendCountsCommittedAndHeld(t *testing.T) {
	f := newEconFixture(t, 1, 1_000_000)
	ctx := context.Background()
	a := f.agents[0]
	pass, err := NewSpendPassRepo(f.db).GetByAgent(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	spend := func(since time.Time) int64 {
		t.Helper()
		n, err := f.repo.PassSpend(ctx, pass.ID, since)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	hourAgo := time.Now().Add(-time.Hour)
	if n := spend(hourAgo); n != 0 {
		t.Fatalf("a fresh pass has used nothing, got %d", n)
	}

	first := f.intent(t, `{"mint":"JUP"}`, 50_000)
	r1, err := f.svc.Reserve(ctx, a, first.ID, app.ReserveRequest{ProviderID: "x402:risk.example", Rail: "sandbox", QuoteMinor: 3_000})
	if err != nil {
		t.Fatal(err)
	}
	if n := spend(hourAgo); n != 3_000 {
		t.Errorf("a held reservation counts: got %d, want 3000", n)
	}
	if r1, err = f.svc.Begin(ctx, a, first.ID, r1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.AuthorizePayment(ctx, a, first.ID, r1.ID, app.PaymentRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Complete(ctx, a, first.ID, r1.ID, app.CompletionReport{Outcome: econ.OutcomeFulfilled, Evidence: econ.Evidence{ResultHash: "sha256:r"}}); err != nil {
		t.Fatal(err)
	}
	if n := spend(hourAgo); n != 3_000 {
		t.Errorf("a commitment counts: got %d, want 3000", n)
	}
	if n := spend(time.Now().Add(time.Hour)); n != 0 {
		t.Errorf("a commitment from before the window does not count: got %d", n)
	}

	second := f.intent(t, `{"mint":"BONK"}`, 50_000)
	r2, err := f.svc.Reserve(ctx, a, second.ID, app.ReserveRequest{ProviderID: "x402:risk.example", Rail: "sandbox", QuoteMinor: 2_000})
	if err != nil {
		t.Fatal(err)
	}
	if n := spend(hourAgo); n != 5_000 {
		t.Errorf("commitment plus a new hold: got %d, want 5000", n)
	}
	if err := f.svc.Release(ctx, a, second.ID, r2.ID); err != nil {
		t.Fatal(err)
	}
	if n := spend(hourAgo); n != 3_000 {
		t.Errorf("a released hold stops counting: got %d, want 3000", n)
	}
}
