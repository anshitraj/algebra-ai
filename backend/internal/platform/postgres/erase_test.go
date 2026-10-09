package postgres

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/privacy"
)

// Erasing a person takes what their agents left behind too: the answers kept for
// repeat requests, the inputs of their requests, and the passes that could still
// spend. The records the law requires, intents, receipts and their hashes, stay
// and are tied to nobody.
func TestEraseUser_TakesWhatTheirAgentsLeftBehind(t *testing.T) {
	f := newEconFixture(t, 1, 10_000_000)
	ctx := context.Background()
	in := f.intent(t, `{"question":"PRIVATE-MARKER what is my diagnosis"}`, 50_000)
	rsv, err := f.svc.Reserve(ctx, f.agents[0], in.ID, app.ReserveRequest{ProviderID: "alpha", Rail: "sandbox", QuoteMinor: 3_000})
	if err != nil {
		t.Fatal(err)
	}
	enc, _ := privacy.NewAESGCMEncryptor(bytes.Repeat([]byte{7}, 32))
	if _, err := app.NewResultVault(NewResultRepo(f.db), enc, time.Hour, 0).Keep(ctx, f.userID, in.ID, rsv.ID, "application/json", 200, []byte(`{"answer":"kept"}`)); err != nil {
		t.Fatal(err)
	}
	count := func(query string, args ...any) int {
		var n int
		if err := f.db.Pool.QueryRow(ctx, query, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count(`SELECT count(*) FROM intent_results WHERE principal_id = $1`, f.userID) != 1 ||
		count(`SELECT count(*) FROM spend_passes WHERE user_id = $1 AND revoked_at IS NULL`, f.userID) != 1 {
		t.Fatal("before erasure there is a kept answer and a live pass")
	}

	if err := NewAccountRepo(f.db).EraseUser(ctx, f.userID, time.Now()); err != nil {
		t.Fatal(err)
	}

	if n := count(`SELECT count(*) FROM intent_results WHERE principal_id = $1`, f.userID); n != 0 {
		t.Errorf("the kept answers are gone: %d", n)
	}
	if n := count(`SELECT count(*) FROM spend_passes WHERE user_id = $1 AND revoked_at IS NULL`, f.userID); n != 0 {
		t.Errorf("no pass can spend any more: %d", n)
	}
	var input, hash string
	if err := f.db.Pool.QueryRow(ctx, `SELECT input::text, input_hash FROM economic_intents WHERE id = $1`, in.ID).Scan(&input, &hash); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains([]byte(input), []byte("PRIVATE-MARKER")) || input != "{}" {
		t.Errorf("what the person asked is scrubbed: %s", input)
	}
	if hash == "" {
		t.Error("but the record of what was asked, as a hash, stays")
	}
	if n := count(`SELECT count(*) FROM economic_intents WHERE id = $1`, in.ID); n != 1 {
		t.Error("the intent itself is a record that stays")
	}
}
