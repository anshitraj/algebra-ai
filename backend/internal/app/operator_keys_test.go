package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/account"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

type fakeOperators map[string]bool

func (f fakeOperators) IsOperator(_ context.Context, id string) bool { return f[id] }

const operatorSecret = "sk-operator-0123456789abcdef"

func TestOperatorKeys_OnlyTheOperatorsAccountsGetTheKey(t *testing.T) {
	ctx := context.Background()
	keys, _ := testToolKeys(t, nil)
	if err := keys.SetOperatorKeys(map[string]string{"acme": operatorSecret}, fakeOperators{"op": true}); err != nil {
		t.Fatal(err)
	}

	got := keys.CandidatesFor(ctx, "op", "web.search")
	if len(got) != 1 || got[0].Provider != "keyed:acme" {
		t.Fatalf("the operator's account is offered acme's tools: %+v", got)
	}
	if k, err := keys.Key(ctx, "op", "acme"); err != nil || k != operatorSecret {
		t.Fatalf("the operator's account uses the operator's key: %q %v", k, err)
	}
	// zed has no operator key, so nothing comes of it.
	if _, err := keys.Key(ctx, "op", "zed"); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("no key for zed: %v", err)
	}

	// Nobody else is offered it, and nobody else can open it.
	if got := keys.CandidatesFor(ctx, "stranger", "web.search"); len(got) != 0 {
		t.Errorf("a stranger is offered the operator's tools: %+v", got)
	}
	if k, err := keys.Key(ctx, "stranger", "acme"); !errors.Is(err, shared.ErrNotFound) || k != "" {
		t.Errorf("a stranger opened the operator's key: %q %v", k, err)
	}
	if got := keys.CandidatesFor(ctx, "", "web.search"); len(got) != 0 {
		t.Errorf("no account, no tools: %+v", got)
	}

	// The listing says it is theirs to use, never what it is.
	list, err := keys.List(ctx, "op")
	if err != nil || len(list) != 2 || !list[0].ServerKey || list[0].Saved || list[1].ServerKey {
		t.Fatalf("acme is the server's key for the operator, zed is nothing: %+v %v", list, err)
	}
	raw, _ := json.Marshal(list)
	if strings.Contains(string(raw), operatorSecret) || strings.Contains(string(raw), "0123456789") || strings.Contains(string(raw), "cdef") {
		t.Fatalf("the operator's key is in a listing: %s", raw)
	}
	other, err := keys.List(ctx, "stranger")
	if err != nil || other[0].ServerKey || other[1].ServerKey {
		t.Fatalf("a stranger sees no server key: %+v %v", other, err)
	}
}

func TestOperatorKeys_APersonsOwnKeyWinsAndRemovingItHandsBack(t *testing.T) {
	ctx := context.Background()
	keys, _ := testToolKeys(t, nil)
	if err := keys.SetOperatorKeys(map[string]string{"acme": operatorSecret}, fakeOperators{"op": true}); err != nil {
		t.Fatal(err)
	}
	const own = "sk-mine-9876543210fedcba"
	if err := keys.Save(ctx, "op", "acme", own); err != nil {
		t.Fatal(err)
	}
	if k, err := keys.Key(ctx, "op", "acme"); err != nil || k != own {
		t.Fatalf("a key the person saved wins: %q %v", k, err)
	}
	if got := keys.CandidatesFor(ctx, "op", "web.search"); len(got) != 1 {
		t.Fatalf("one vendor, one offer, however many keys: %+v", got)
	}
	list, _ := keys.List(ctx, "op")
	if !list[0].Saved || list[0].Hint != "dcba" || !list[0].ServerKey {
		t.Fatalf("the listing shows their key, and that the server's is behind it: %+v", list[0])
	}

	if err := keys.Remove(ctx, "op", "acme"); err != nil {
		t.Fatal(err)
	}
	if k, err := keys.Key(ctx, "op", "acme"); err != nil || k != operatorSecret {
		t.Fatalf("with their own key gone the operator's is used again: %q %v", k, err)
	}
	// Removing is for the person's own key only: the server's cannot be removed from here.
	if err := keys.Remove(ctx, "op", "acme"); err != nil {
		t.Fatal(err)
	}
	if k, _ := keys.Key(ctx, "op", "acme"); k != operatorSecret {
		t.Error("removing a key that isn't saved must not touch the operator's")
	}
}

func TestOperatorKeys_ABadKeyIsRefusedAtStartWithoutRepeatingIt(t *testing.T) {
	keys, _ := testToolKeys(t, nil)
	for name, m := range map[string]map[string]string{
		"unknown vendor": {"nope": operatorSecret},
		"too short":      {"acme": "short"},
		"has a space":    {"acme": "sk operator 0123456789"},
		"too long":       {"acme": strings.Repeat("k", 300)},
	} {
		err := keys.SetOperatorKeys(m, fakeOperators{"op": true})
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		for _, v := range m {
			if strings.Contains(err.Error(), v) {
				t.Errorf("%s: the error repeats the key: %v", name, err)
			}
		}
	}
	if keys.isOperator(context.Background(), "op") {
		t.Error("a refused set leaves nothing behind")
	}
}

func TestOperatorKeys_WithoutOperatorsThereIsNothing(t *testing.T) {
	ctx := context.Background()
	keys, _ := testToolKeys(t, nil)
	// Keys but nobody named: nobody may use them.
	if err := keys.SetOperatorKeys(map[string]string{"acme": operatorSecret}, NewOperatorEmails(nil, &fakeOperatorUsers{})); err != nil {
		t.Fatal(err)
	}
	if got := keys.CandidatesFor(ctx, "op", "web.search"); len(got) != 0 {
		t.Errorf("no operator named, yet offered: %+v", got)
	}
	// And a service that was never given keys behaves as it always did.
	plain, _ := testToolKeys(t, nil)
	if got := plain.CandidatesFor(ctx, "op", "web.search"); len(got) != 0 {
		t.Errorf("plain: %+v", got)
	}
}

type fakeOperatorUsers struct {
	mu      sync.Mutex
	byEmail map[string]*account.User
	err     error
	calls   int
}

func (f *fakeOperatorUsers) UserByEmail(_ context.Context, email string) (*account.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	u, ok := f.byEmail[email]
	if !ok {
		return nil, shared.ErrNotFound
	}
	c := *u
	return &c, nil
}

func TestOperatorEmails_OnlyAVerifiedPasswordlessLiveAccountCounts(t *testing.T) {
	ctx := context.Background()
	at := time.Now()
	users := &fakeOperatorUsers{byEmail: map[string]*account.User{
		"me@example.com":         {ID: "u_me", Email: "me@example.com", EmailVerifiedAt: &at},
		"unverified@example.com": {ID: "u_unverified", Email: "unverified@example.com"},
		"password@example.com":   {ID: "u_password", Email: "password@example.com", EmailVerifiedAt: &at, PasswordHash: "x"},
		"demo@example.com":       {ID: "u_demo", Email: "demo@example.com", EmailVerifiedAt: &at, Mode: account.ModeDemo},
	}}
	op := NewOperatorEmails([]string{"me@example.com", "unverified@example.com", "password@example.com", "demo@example.com", "nobody@example.com"}, users)
	for id, want := range map[string]bool{
		"u_me":         true,
		"u_unverified": false, // nobody has proved the address
		"u_password":   false, // a password may have been set before the address was verified
		"u_demo":       false,
		"u_other":      false, // not at a listed address
		"":             false,
	} {
		if got := op.IsOperator(ctx, id); got != want {
			t.Errorf("%q: IsOperator = %v, want %v", id, got, want)
		}
	}
	if (*OperatorEmails)(nil).IsOperator(ctx, "u_me") {
		t.Error("no check, no operator")
	}
	if NewOperatorEmails(nil, users).IsOperator(ctx, "u_me") {
		t.Error("no address listed, no operator")
	}
}

func TestOperatorEmails_LookedUpOnceAWhileAndAFailureIsClosed(t *testing.T) {
	ctx := context.Background()
	at := time.Now()
	users := &fakeOperatorUsers{byEmail: map[string]*account.User{}}
	op := NewOperatorEmails([]string{"me@example.com"}, users)
	now := at
	op.now = func() time.Time { return now }

	// Not signed in yet: no.
	if op.IsOperator(ctx, "u_me") {
		t.Fatal("no account at the address yet")
	}
	// They sign in; within the lifetime of what was looked up, still no, after it, yes.
	users.byEmail["me@example.com"] = &account.User{ID: "u_me", Email: "me@example.com", EmailVerifiedAt: &at}
	calls := users.calls
	if op.IsOperator(ctx, "u_me") || users.calls != calls {
		t.Fatal("the set is not looked up again within its lifetime")
	}
	now = now.Add(operatorAccountsTTL)
	if !op.IsOperator(ctx, "u_me") {
		t.Fatal("after its lifetime the account counts")
	}
	if users.calls != calls+1 {
		t.Errorf("one lookup for the one address: %d", users.calls-calls)
	}

	// The account gets a password: it stops counting once the set is looked up again.
	users.byEmail["me@example.com"].PasswordHash = "x"
	now = now.Add(operatorAccountsTTL)
	if op.IsOperator(ctx, "u_me") {
		t.Fatal("an account with a password does not count")
	}

	// A failed lookup says no, and the next call tries again.
	users.byEmail["me@example.com"].PasswordHash = ""
	users.err = errors.New("db down")
	now = now.Add(operatorAccountsTTL)
	if op.IsOperator(ctx, "u_me") {
		t.Fatal("a failed lookup must not let anyone in")
	}
	users.err = nil
	if !op.IsOperator(ctx, "u_me") {
		t.Fatal("the next call looks again")
	}
}
