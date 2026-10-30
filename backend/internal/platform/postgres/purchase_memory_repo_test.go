package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/econ"
)

// The purchase memory query on real Postgres: only delivered purchases of the
// same capability and input, newest first, since the time asked.
func TestEconRepo_RecentPurchases(t *testing.T) {
	f := newEconFixture(t, 1, 1_000_000)
	ctx := context.Background()
	a := f.agents[0]
	in := f.intent(t, `{"mint":"JUP"}`, 50_000)
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
	if _, err := f.svc.Complete(ctx, a, in.ID, r.ID, app.CompletionReport{Outcome: econ.OutcomeFulfilled}); err != nil {
		t.Fatal(err)
	}
	other := f.intent(t, `{"mint":"BONK"}`, 50_000) // never paid

	got, err := f.repo.RecentPurchases(ctx, f.userID, in.Capability, in.InputHash, time.Now().Add(-time.Minute), 5)
	if err != nil || len(got) != 1 {
		t.Fatalf("one delivered purchase: %+v %v", got, err)
	}
	p := got[0]
	if p.IntentID != in.ID || p.ReservationID != r.ID || p.ProviderID != "x402:risk.example" || p.CommittedMinor != 3_000 || p.At.IsZero() {
		t.Errorf("purchase: %+v", p)
	}
	if got, _ := f.repo.RecentPurchases(ctx, f.userID, in.Capability, in.InputHash, time.Now().Add(time.Minute), 5); len(got) != 0 {
		t.Errorf("nothing since a minute from now: %+v", got)
	}
	if got, _ := f.repo.RecentPurchases(ctx, f.userID, other.Capability, other.InputHash, time.Now().Add(-time.Minute), 5); len(got) != 0 {
		t.Errorf("an intent never paid is no purchase: %+v", got)
	}
}
