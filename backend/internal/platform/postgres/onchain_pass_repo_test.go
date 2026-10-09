package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

func TestOnchainPassRepoBindingsAndPulls(t *testing.T) {
	f := newEconFixture(t, 1, 1_000_000)
	repo := NewOnchainPassRepo(f.db)
	ctx := context.Background()
	passID := ""
	if err := f.db.Pool.QueryRow(ctx, `SELECT id FROM spend_passes WHERE user_id = $1 LIMIT 1`, f.userID).Scan(&passID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := repo.OnchainBinding(ctx, passID); !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("no binding yet: %v", err)
	}
	b := &app.OnchainBinding{PassID: passID, UserID: f.userID, Network: "solana-devnet", ProgramID: "46gQDUuJCt6VdDMaguGPoD8SKEtFqvpG7ECtpC2F9tdS",
		Address: "pass-" + uuid.NewString(), OwnerWallet: "9kEVg2noE8uubD6e1jMtEwumULEyNLgr7uYCXDVL4CLW", PassNumber: ^uint64(0) - 7, LinkedAt: now}
	if err := repo.SaveOnchainBinding(ctx, b); err != nil {
		t.Fatal(err)
	}
	got, err := repo.OnchainBinding(ctx, passID)
	if err != nil || got.PassNumber != b.PassNumber || got.Address != b.Address || got.UserID != f.userID {
		t.Fatalf("binding %+v, %v", got, err)
	}
	if err := repo.SaveOnchainBinding(ctx, b); !errors.Is(err, shared.ErrConflict) {
		t.Fatalf("second binding: %v", err)
	}

	p := &app.OnchainPull{ReservationID: "rsv_" + uuid.NewString(), IntentID: "eint_x", PassID: passID, Network: "solana-devnet",
		PassAddress: b.Address, OwnerWallet: b.OwnerWallet, AmountMinor: 50_000, PullSignature: "sig-" + uuid.NewString(),
		PullValidUntil: 123, State: app.PullSending, CreatedAt: now, UpdatedAt: now}
	if err := repo.InsertOnchainPull(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := repo.InsertOnchainPull(ctx, p); !errors.Is(err, shared.ErrConflict) {
		t.Fatalf("a second pull for one attempt: %v", err)
	}
	open, err := repo.OpenOnchainPulls(ctx, 1000)
	if err != nil || !containsPull(open, p.ReservationID) {
		t.Fatalf("open pulls: %v", err)
	}
	p.State = app.PullPulled
	if err := repo.UpdateOnchainPull(ctx, p, app.PullSending); err != nil {
		t.Fatal(err)
	}
	used, refund := int64(4_200), int64(45_800)
	p.State, p.UsedMinor, p.RefundMinor, p.RefundSignature, p.RefundValidUntil = app.PullRefunding, &used, &refund, "refund-sig", 456
	if err := repo.UpdateOnchainPull(ctx, p, app.PullPulled); err != nil {
		t.Fatal(err)
	}
	// Two reconcilers both read PULLED: only one may claim the refund.
	if err := repo.UpdateOnchainPull(ctx, p, app.PullPulled); !errors.Is(err, shared.ErrConflict) {
		t.Fatalf("second claim: %v", err)
	}
	back, err := repo.OnchainPull(ctx, p.ReservationID)
	if err != nil || back.State != app.PullRefunding || *back.UsedMinor != used || *back.RefundMinor != refund || back.RefundValidUntil != 456 || back.PullValidUntil != 123 {
		t.Fatalf("pull %+v, %v", back, err)
	}
	p.State = app.PullRefunded
	if err := repo.UpdateOnchainPull(ctx, p, app.PullRefunding); err != nil {
		t.Fatal(err)
	}
	if open, _ := repo.OpenOnchainPulls(ctx, 1000); containsPull(open, p.ReservationID) {
		t.Fatal("a refunded pull is still open")
	}
	if list, err := repo.OnchainPullsForPass(ctx, passID, 10); err != nil || len(list) != 1 {
		t.Fatalf("pulls for pass: %d, %v", len(list), err)
	}
	if err := repo.DeleteOnchainBinding(ctx, passID); err != nil {
		t.Fatal(err)
	}
	_, _ = f.db.Pool.Exec(ctx, `DELETE FROM onchain_pulls WHERE pass_id = $1`, passID)
}

func containsPull(ps []app.OnchainPull, id string) bool {
	for _, p := range ps {
		if p.ReservationID == id {
			return true
		}
	}
	return false
}
