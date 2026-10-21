package postgres

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/project-algebra/algebra/internal/app"
)

// loginCodesOnAScratchSchema runs the real migration in a schema of its own, so
// the test can never touch a real login code.
func loginCodesOnAScratchSchema(t *testing.T) (*AccountRepo, *pgxpool.Pool) {
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
	schema := "t_logincode_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
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
	migration, err := os.ReadFile("../../../migrations/0028_login_codes.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("the migration: %v", err)
	}
	// What PurgeExpired also reads.
	for _, ddl := range []string{
		"CREATE TABLE user_sessions (revoked_at TIMESTAMPTZ, expires_at TIMESTAMPTZ)",
		"CREATE TABLE password_reset_tokens (used_at TIMESTAMPTZ, expires_at TIMESTAMPTZ)",
	} {
		if _, err := pool.Exec(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	return NewAccountRepo(&DB{Pool: pool}), pool
}

func TestLoginCodes_PutTryAndUseUp(t *testing.T) {
	repo, _ := loginCodesOnAScratchSchema(t)
	ctx := context.Background()
	now := time.Now()

	if at, err := repo.LoginCodeSentAt(ctx, "addr"); err != nil || !at.IsZero() {
		t.Fatalf("nothing sent yet: %v %v", at, err)
	}
	if got, err := repo.TryLoginCode(ctx, "addr", "h1", now, 5); err != nil || got != app.LoginCodeGone {
		t.Fatalf("a code that was never sent: %v %v", got, err)
	}
	if err := repo.PutLoginCode(ctx, "addr", "h1", now, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if at, err := repo.LoginCodeSentAt(ctx, "addr"); err != nil || at.IsZero() {
		t.Fatalf("sent at: %v %v", at, err)
	}
	if got, err := repo.TryLoginCode(ctx, "addr", "nope", now, 5); err != nil || got != app.LoginCodeWrong {
		t.Fatalf("a wrong code: %v %v", got, err)
	}
	if got, err := repo.TryLoginCode(ctx, "addr", "h1", now, 5); err != nil || got != app.LoginCodeAccepted {
		t.Fatalf("the right code: %v %v", got, err)
	}
	if got, err := repo.TryLoginCode(ctx, "addr", "h1", now, 5); err != nil || got != app.LoginCodeGone {
		t.Fatalf("the right code a second time: %v %v", got, err)
	}
}

func TestLoginCodes_ANewCodeReplacesTheOldAndResetsTheTries(t *testing.T) {
	repo, _ := loginCodesOnAScratchSchema(t)
	ctx := context.Background()
	now := time.Now()
	if err := repo.PutLoginCode(ctx, "addr", "old", now, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if got, _ := repo.TryLoginCode(ctx, "addr", "nope", now, 5); got != app.LoginCodeWrong {
			t.Fatalf("wrong try %d: %v", i, got)
		}
	}
	if err := repo.PutLoginCode(ctx, "addr", "new", now.Add(time.Minute), now.Add(11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.TryLoginCode(ctx, "addr", "old", now, 5); got != app.LoginCodeWrong {
		t.Fatalf("the replaced code: %v, want wrong", got)
	}
	// Three wrong tries before the replacement don't count against the new code.
	for i := 0; i < 2; i++ {
		if got, _ := repo.TryLoginCode(ctx, "addr", "nope", now, 5); got != app.LoginCodeWrong {
			t.Fatalf("the new code's wrong try %d: %v", i, got)
		}
	}
	if got, _ := repo.TryLoginCode(ctx, "addr", "new", now, 5); got != app.LoginCodeAccepted {
		t.Fatalf("the new code: %v, want accepted", got)
	}
}

func TestLoginCodes_TheLastWrongTryBurnsTheCode(t *testing.T) {
	repo, _ := loginCodesOnAScratchSchema(t)
	ctx := context.Background()
	now := time.Now()
	if err := repo.PutLoginCode(ctx, "addr", "right", now, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < 5; i++ {
		if got, _ := repo.TryLoginCode(ctx, "addr", "nope", now, 5); got != app.LoginCodeWrong {
			t.Fatalf("wrong try %d: %v", i, got)
		}
	}
	if got, _ := repo.TryLoginCode(ctx, "addr", "nope", now, 5); got != app.LoginCodeGone {
		t.Fatalf("the fifth wrong try: %v, want the code gone", got)
	}
	if got, _ := repo.TryLoginCode(ctx, "addr", "right", now, 5); got != app.LoginCodeGone {
		t.Fatalf("the right code after the burn: %v, want gone", got)
	}
}

func TestLoginCodes_AnExpiredCodeIsGoneAndNotSent(t *testing.T) {
	repo, pool := loginCodesOnAScratchSchema(t)
	ctx := context.Background()
	now := time.Now()
	if err := repo.PutLoginCode(ctx, "addr", "h", now.Add(-20*time.Minute), now.Add(-10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if at, err := repo.LoginCodeSentAt(ctx, "addr"); err != nil || !at.IsZero() {
		t.Fatalf("an expired code counts as none: %v %v", at, err)
	}
	if got, _ := repo.TryLoginCode(ctx, "addr", "h", now, 5); got != app.LoginCodeGone {
		t.Fatalf("an expired code: %v, want gone", got)
	}
	// And the purge takes the dead ones away.
	if err := repo.PutLoginCode(ctx, "dead", "h", now.Add(-2*time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.PurgeExpired(ctx, now); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM login_codes").Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d codes left after the purge (%v), want 0", n, err)
	}
}

func TestLoginCodes_GuessesAtOnceAreCountedOneAfterTheOther(t *testing.T) {
	repo, _ := loginCodesOnAScratchSchema(t)
	ctx := context.Background()
	now := time.Now()
	if err := repo.PutLoginCode(ctx, "addr", "right", now, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Forty guesses at the same moment: only four may be told "wrong"; the rest find the code burned.
	var wg sync.WaitGroup
	var mu sync.Mutex
	counts := map[app.LoginCodeOutcome]int{}
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := repo.TryLoginCode(ctx, "addr", "nope", now, 5)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			counts[got]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if counts[app.LoginCodeWrong] != 4 || counts[app.LoginCodeAccepted] != 0 {
		t.Fatalf("outcomes %v: want exactly 4 wrong (the 5th burns it) and none accepted", counts)
	}
	if got, _ := repo.TryLoginCode(ctx, "addr", "right", now, 5); got != app.LoginCodeGone {
		t.Fatalf("the right code after the burn: %v, want gone", got)
	}
}
