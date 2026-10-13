package postgres

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/platform/payersigner"
)

// signerRepoOnAScratchSchema runs the real migration in a schema of its own,
// so the test can never touch (or leave junk in) a real devnet signer row.
func signerRepoOnAScratchSchema(t *testing.T) (*SignerRepo, *pgxpool.Pool) {
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
	schema := "t_signer_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
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
	migration, err := os.ReadFile("../../../migrations/0026_platform_signers.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("the migration: %v", err)
	}
	// Only what HasPasses reads; the real table has foreign keys to users.
	if _, err := pool.Exec(ctx, "CREATE TABLE onchain_passes (network TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	return NewSignerRepo(&DB{Pool: pool}), pool
}

func TestSignerRepo_OneKeyPerClusterAndNeverReplaced(t *testing.T) {
	repo, _ := signerRepoOnAScratchSchema(t)
	ctx := context.Background()
	if _, err := repo.Signer(ctx, "devnet"); !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("nothing stored yet: %v", err)
	}
	first := &payersigner.Stored{Cluster: "devnet", PublicKey: "FirstAddress", Sealed: []byte("sealed-one"), Nonce: []byte("nonce-one"), CreatedAt: time.Now()}
	if ok, err := repo.SaveSigner(ctx, first); err != nil || !ok {
		t.Fatalf("the first key is stored: %v %v", ok, err)
	}
	// A second server starting at the same moment must not replace it.
	second := &payersigner.Stored{Cluster: "devnet", PublicKey: "SecondAddress", Sealed: []byte("sealed-two"), Nonce: []byte("nonce-two"), CreatedAt: time.Now()}
	if ok, err := repo.SaveSigner(ctx, second); err != nil || ok {
		t.Fatalf("a second key must not be stored, and that is not an error: %v %v", ok, err)
	}
	got, err := repo.Signer(ctx, "devnet")
	if err != nil || got.PublicKey != "FirstAddress" || string(got.Sealed) != "sealed-one" || string(got.Nonce) != "nonce-one" {
		t.Fatalf("the first key stays: %+v %v", got, err)
	}
}

// The schema itself refuses a mainnet key: real money is never held this way.
func TestSignerRepo_TheDatabaseRefusesAMainnetKey(t *testing.T) {
	repo, _ := signerRepoOnAScratchSchema(t)
	_, err := repo.SaveSigner(context.Background(), &payersigner.Stored{Cluster: "mainnet", PublicKey: "x", Sealed: []byte("s"), Nonce: []byte("n"), CreatedAt: time.Now()})
	if err == nil {
		t.Fatal("a mainnet signer row must be refused by the table's CHECK")
	}
}

func TestSignerRepo_HasPassesSeesVaultsOnANetwork(t *testing.T) {
	repo, pool := signerRepoOnAScratchSchema(t)
	ctx := context.Background()
	if has, err := repo.HasPasses(ctx, "solana-devnet"); err != nil || has {
		t.Fatalf("no vaults yet: %v %v", has, err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO onchain_passes (network) VALUES ('solana-devnet')"); err != nil {
		t.Fatal(err)
	}
	if has, err := repo.HasPasses(ctx, "solana-devnet"); err != nil || !has {
		t.Fatalf("a vault on devnet: %v %v", has, err)
	}
	if has, _ := repo.HasPasses(ctx, "solana"); has {
		t.Fatal("devnet vaults aren't mainnet vaults")
	}
}
