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

// A person's tool keys are kept sealed, listed without their bytes, replaced
// when saved again, and gone when the person is erased.
func TestToolKeyRepo_SealedKeysPerOwnerAndVendor(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	owner, other := mustCreateUser(t, db), mustCreateUser(t, db)
	enc, err := privacy.NewAESGCMEncryptor(bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatal(err)
	}
	keys := app.NewToolKeys(NewToolKeyRepo(db), enc, []app.ToolVendor{{ID: "tinyfish", Name: "TinyFish"}, {ID: "exa", Name: "Exa"}}, nil, nil)

	if err := keys.Save(ctx, owner, "tinyfish", "tf-live-0123456789abcd"); err != nil {
		t.Fatal(err)
	}
	if err := keys.Save(ctx, owner, "exa", "exa-live-0123456789wxyz"); err != nil {
		t.Fatal(err)
	}
	if got, err := keys.Key(ctx, owner, "tinyfish"); err != nil || got != "tf-live-0123456789abcd" {
		t.Fatalf("the owner reads their key: %q %v", got, err)
	}
	if _, err := keys.Key(ctx, other, "tinyfish"); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("another account has none: %v", err)
	}
	var stored []byte
	if err := db.Pool.QueryRow(ctx, `SELECT sealed FROM tool_keys WHERE user_id = $1 AND vendor = 'tinyfish'`, owner).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte("0123456789")) {
		t.Fatal("the database holds the key sealed")
	}

	list, err := keys.List(ctx, owner)
	if err != nil || len(list) != 2 || !list[0].Saved || !list[1].Saved {
		t.Fatalf("list: %+v %v", list, err)
	}

	// Saving again replaces.
	if err := keys.Save(ctx, owner, "tinyfish", "tf-new-9876543210efgh"); err != nil {
		t.Fatal(err)
	}
	if got, _ := keys.Key(ctx, owner, "tinyfish"); got != "tf-new-9876543210efgh" {
		t.Errorf("replaced: %q", got)
	}
	if err := keys.Remove(ctx, owner, "exa"); err != nil {
		t.Fatal(err)
	}
	if list, _ = keys.List(ctx, owner); list[1].Saved {
		t.Errorf("exa was removed: %+v", list)
	}

	if err := NewAccountRepo(db).EraseUser(ctx, owner, time.Now()); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM tool_keys WHERE user_id = $1`, owner).Scan(&n); err != nil || n != 0 {
		t.Errorf("erasing a person takes their keys: %d %v", n, err)
	}
}
