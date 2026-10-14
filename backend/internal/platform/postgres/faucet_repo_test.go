package postgres

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/project-algebra/algebra/internal/app"
)

// faucetRepoOnAScratchSchema runs the real migration in a schema of its own, so
// the test can never touch (or leave junk in) a real airdrop ledger.
func faucetRepoOnAScratchSchema(t *testing.T) (*FaucetRepo, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping Postgres integration test (see docs/LOCAL_DEVELOPMENT.md)")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	schema := "t_faucet_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	// Only what the foreign key needs; the real table has much more.
	if _, err := pool.Exec(ctx, "CREATE TABLE users (id TEXT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"u1", "u2", "u3"} {
		if _, err := pool.Exec(ctx, "INSERT INTO users (id) VALUES ($1)", id); err != nil {
			t.Fatal(err)
		}
	}
	migration, err := os.ReadFile("../../../migrations/0027_devnet_faucet_drops.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("the migration: %v", err)
	}
	return NewFaucetRepo(&DB{Pool: pool}), pool
}

func TestFaucetRepo_OneAirdropPerAccountRecordedAndRemembered(t *testing.T) {
	repo, _ := faucetRepoOnAScratchSchema(t)
	ctx := context.Background()
	now := time.Now()

	if got, err := repo.Received(ctx, "u1"); err != nil || got != nil {
		t.Fatalf("nothing given yet: %+v %v", got, err)
	}
	if err := repo.Claim(ctx, "u1", "WalletOne", now); err != nil {
		t.Fatal(err)
	}
	// Being sent: the account has a claim, with no signature yet.
	if got, err := repo.Received(ctx, "u1"); err != nil || got == nil || got.Signature != "" || got.Address != "WalletOne" {
		t.Fatalf("a claim being sent: %+v %v", got, err)
	}
	if err := repo.Record(ctx, "u1", app.FaucetDrop{Address: "WalletOne", SOLLamports: 200_000_000, USDCMinor: 2_000_000, Signature: "SigOne"}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Received(ctx, "u1")
	if err != nil || got == nil || got.Signature != "SigOne" || got.SOLLamports != 200_000_000 || got.USDCMinor != 2_000_000 || got.Address != "WalletOne" {
		t.Fatalf("a recorded drop: %+v %v", got, err)
	}

	// The same account again, even for another wallet: refused, with what it got.
	var used *app.FaucetUsedError
	err = repo.Claim(ctx, "u1", "WalletTwo", now)
	if !errors.As(err, &used) || used.Scope != "account" || used.Received == nil || used.Received.Signature != "SigOne" {
		t.Fatalf("the same account twice: %v", err)
	}
	// The same wallet on another account: refused, and what the first account got is not shown.
	err = repo.Claim(ctx, "u2", "WalletOne", now)
	if !errors.As(err, &used) || used.Scope != "wallet" || used.Received != nil {
		t.Fatalf("the same wallet on another account: %v (%+v)", err, used)
	}
	// Another account with its own wallet is fine.
	if err := repo.Claim(ctx, "u2", "WalletTwo", now); err != nil {
		t.Fatalf("another account, another wallet: %v", err)
	}
}

func TestFaucetRepo_ReleaseGivesTheAirdropBack(t *testing.T) {
	repo, _ := faucetRepoOnAScratchSchema(t)
	ctx := context.Background()
	now := time.Now()
	if err := repo.Claim(ctx, "u1", "WalletOne", now); err != nil {
		t.Fatal(err)
	}
	if err := repo.Release(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.Received(ctx, "u1"); got != nil {
		t.Fatalf("a released claim is still held: %+v", got)
	}
	// Both the account and the wallet are free again.
	if err := repo.Claim(ctx, "u2", "WalletOne", now); err != nil {
		t.Fatalf("the wallet after a release: %v", err)
	}
	if err := repo.Claim(ctx, "u1", "WalletTwo", now); err != nil {
		t.Fatalf("the account after a release: %v", err)
	}
	// Releasing what is not held is nothing.
	if err := repo.Release(ctx, "u3"); err != nil {
		t.Fatalf("releasing nothing: %v", err)
	}
}

func TestFaucetRepo_ANeverSentClaimIsGivenUpAfterTenMinutes(t *testing.T) {
	repo, pool := faucetRepoOnAScratchSchema(t)
	ctx := context.Background()
	// A server that stopped between claiming and sending, half an hour ago.
	if _, err := pool.Exec(ctx, `INSERT INTO devnet_faucet_drops (user_id, address, created_at) VALUES ('u1', 'WalletOne', now() - interval '30 minutes')`); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.Received(ctx, "u1"); err != nil || got != nil {
		t.Fatalf("a stale claim must not count as an airdrop: %+v %v", got, err)
	}
	if err := repo.Claim(ctx, "u1", "WalletOne", time.Now()); err != nil {
		t.Fatalf("the account may claim again: %v", err)
	}
	// The same, but for the wallet: another account may take the wallet over.
	if _, err := pool.Exec(ctx, `INSERT INTO devnet_faucet_drops (user_id, address, created_at) VALUES ('u2', 'WalletTwo', now() - interval '30 minutes')`); err != nil {
		t.Fatal(err)
	}
	if err := repo.Claim(ctx, "u3", "WalletTwo", time.Now()); err != nil {
		t.Fatalf("the wallet of a stale claim: %v", err)
	}
	// A claim that was sent never goes stale.
	if _, err := pool.Exec(ctx, `UPDATE devnet_faucet_drops SET signature = 'Sig', created_at = now() - interval '90 days' WHERE user_id = 'u1'`); err != nil {
		t.Fatal(err)
	}
	var used *app.FaucetUsedError
	if err := repo.Claim(ctx, "u1", "WalletThree", time.Now()); !errors.As(err, &used) {
		t.Fatalf("a drop that was sent is permanent: %v", err)
	}
}

func TestFaucetRepo_OnlyOneOfManyAtOnceWins(t *testing.T) {
	repo, _ := faucetRepoOnAScratchSchema(t)
	ctx := context.Background()
	// The same account from many places at once.
	var wg sync.WaitGroup
	results := make([]error, 12)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = repo.Claim(ctx, "u1", "Wallet"+string(rune('A'+i)), time.Now())
		}()
	}
	wg.Wait()
	wins := 0
	for _, err := range results {
		var used *app.FaucetUsedError
		switch {
		case err == nil:
			wins++
		case !errors.As(err, &used):
			t.Fatalf("a claim failed for another reason: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d claims won for one account, want 1", wins)
	}
	// And the same wallet from many accounts.
	results = make([]error, 3)
	for i, user := range []string{"u1", "u2", "u3"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = repo.Claim(ctx, user, "SharedWallet", time.Now())
		}()
	}
	wg.Wait()
	wins = 0
	for _, err := range results {
		if err == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("%d accounts claimed one wallet, want 1", wins)
	}
}

func TestFaucetRepo_RecordNeedsAClaim(t *testing.T) {
	repo, _ := faucetRepoOnAScratchSchema(t)
	if err := repo.Record(context.Background(), "u1", app.FaucetDrop{Signature: "Sig"}, time.Now()); err == nil {
		t.Fatal("recording a drop nobody claimed must fail")
	}
}
