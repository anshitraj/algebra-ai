package postgres

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/privacy"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

func TestResultRepo_SealedAtRestBoundToItsOwnerAndExpiring(t *testing.T) {
	f := newEconFixture(t, 1, 10_000_000)
	repo := NewResultRepo(f.db)
	ctx := context.Background()
	in := f.intent(t, `{"mint":"RESULTS"}`, 50_000)
	rsv, err := f.svc.Reserve(ctx, f.agents[0], in.ID, app.ReserveRequest{ProviderID: "alpha", Rail: "sandbox", QuoteMinor: 3_000})
	if err != nil {
		t.Fatal(err)
	}
	enc, err := privacy.NewAESGCMEncryptor(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	vault := app.NewResultVault(repo, enc, time.Hour, 0)

	body := []byte(`{"mint":"RESULTS","risk_score":12,"secret_marker":"PLAINTEXT-MUST-NOT-BE-AT-REST"}`)
	k, err := vault.Keep(ctx, f.userID, in.ID, rsv.ID, "application/json", 200, body)
	if err != nil || !k.Stored || k.Size != len(body) || time.Until(k.ExpiresAt) < 59*time.Minute {
		t.Fatalf("kept: %+v %v", k, err)
	}

	// At rest it is sealed: the bytes in the table are not the body.
	var sealed []byte
	var size int
	var hash string
	if err := f.db.Pool.QueryRow(ctx, `SELECT sealed, size_bytes, body_sha256 FROM intent_results WHERE intent_id = $1`, in.ID).Scan(&sealed, &size, &hash); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("PLAINTEXT-MUST-NOT-BE-AT-REST")) || bytes.Contains(sealed, []byte("risk_score")) || size != len(body) || hash == "" {
		t.Errorf("the stored bytes must be sealed: %d bytes, hash %q", len(sealed), hash)
	}

	// It opens for its owner, and matches what was sealed.
	got, err := vault.Get(ctx, f.userID, in.ID)
	if err != nil || !bytes.Equal(got.Body, body) || got.ReservationID != rsv.ID || got.ContentType != "application/json" || got.HTTPStatus != 200 || got.SHA256 != hash {
		t.Fatalf("get: %+v %v", got, err)
	}
	// Not for anyone else.
	if _, err := vault.Get(ctx, "someone-else", in.ID); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("another person's result is not found: %v", err)
	}
	// A row moved to another intent or person doesn't open: the additional data differs.
	other := f.intent(t, `{"mint":"OTHER"}`, 50_000)
	if _, err := f.db.Pool.Exec(ctx, `UPDATE intent_results SET intent_id = $1 WHERE intent_id = $2`, other.ID, in.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Get(ctx, f.userID, other.ID); err == nil {
		t.Error("a sealed result copied to another intent must not open")
	}
	if _, err := f.db.Pool.Exec(ctx, `UPDATE intent_results SET intent_id = $1 WHERE intent_id = $2`, in.ID, other.ID); err != nil {
		t.Fatal(err)
	}

	// Tampering is detected.
	flipped := append([]byte(nil), sealed...)
	flipped[len(flipped)-1] ^= 0xff
	if _, err := f.db.Pool.Exec(ctx, `UPDATE intent_results SET sealed = $1 WHERE intent_id = $2`, flipped, in.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Get(ctx, f.userID, in.ID); err == nil {
		t.Error("a tampered result must not open")
	}
	if _, err := f.db.Pool.Exec(ctx, `UPDATE intent_results SET sealed = $1 WHERE intent_id = $2`, sealed, in.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Get(ctx, f.userID, in.ID); err != nil {
		t.Fatalf("restored: %v", err)
	}

	// Keeping again replaces it: one result per intent.
	if _, err := vault.Keep(ctx, f.userID, in.ID, rsv.ID, "text/plain", 200, []byte("second")); err != nil {
		t.Fatal(err)
	}
	var rows int
	_ = f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM intent_results WHERE intent_id = $1`, in.ID).Scan(&rows)
	if again, _ := vault.Get(ctx, f.userID, in.ID); rows != 1 || again == nil || string(again.Body) != "second" {
		t.Errorf("one result per intent, the newest: rows=%d %+v", rows, again)
	}

	// Expiry: past expires_at it is gone to readers, and purging removes the row.
	future := time.Now().Add(2 * time.Hour)
	if _, err := repo.GetSealed(ctx, in.ID, future); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("an expired result is not found: %v", err)
	}
	n, err := repo.DeleteExpired(ctx, future)
	if err != nil || n < 1 {
		t.Fatalf("purge: %d %v", n, err)
	}
	if _, err := vault.Get(ctx, f.userID, in.ID); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("after the purge: %v", err)
	}

	// Deleting the intent (or the account behind it) takes its result along.
	if _, err := vault.Keep(ctx, f.userID, in.ID, rsv.ID, "text/plain", 200, []byte("again")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Pool.Exec(ctx, `DELETE FROM economic_intents WHERE id = $1`, in.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetSealed(ctx, in.ID, time.Now()); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("the result goes with its intent: %v", err)
	}
}
