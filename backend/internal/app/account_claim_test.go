package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/account"
	"github.com/project-algebra/algebra/internal/domain/privacy"
)

// An address nobody has proved can be signed up with a password by someone who
// does not own it, who then waits for the owner to arrive. Proving the address
// (a code, or a provider that has verified it) gives the owner that account
// without the password or the sessions the first person made.

const squatterPassword = "squatter-password-1"

func TestClaim_ACodeTakesTheAccountFromWhoeverSignedUpFirst(t *testing.T) {
	h := newLoginCodeHarness(t)
	ctx := context.Background()

	squatter, err := h.svc.SignUp(ctx, "", "owner@example.com", squatterPassword, meta)
	if err != nil {
		t.Fatal(err)
	}
	if squatter.User.EmailVerifiedAt != nil {
		t.Fatal("a password sign-up proves nothing about the address")
	}
	// The squatter is in, with a session and a console agent.
	if _, err := h.svc.Authenticate(ctx, squatter.Token); err != nil {
		t.Fatal(err)
	}

	if err := h.svc.RequestLoginCode(ctx, "owner@example.com", ""); err != nil {
		t.Fatal(err)
	}
	owner, err := h.svc.SignInWithLoginCode(ctx, "owner@example.com", h.lastCode(t), meta)
	if err != nil {
		t.Fatal(err)
	}
	if owner.Created || owner.User.ID != squatter.User.ID {
		t.Fatalf("the owner signed in as %s (created %v), want the account that was there: %s", owner.User.ID, owner.Created, squatter.User.ID)
	}
	if owner.User.EmailVerifiedAt == nil || h.store.users[owner.User.ID].EmailVerifiedAt == nil {
		t.Fatal("a code proves the address")
	}

	if _, err := h.svc.Authenticate(ctx, squatter.Token); !errors.Is(err, account.ErrSessionInvalid) {
		t.Errorf("the squatter's session survived: %v", err)
	}
	if ag, err := h.agents.Get(ctx, squatter.Session.AgentID); err != nil || !ag.IsRevoked() {
		t.Errorf("the squatter's console agent survived: %v %+v", err, ag)
	}
	if _, err := h.svc.SignIn(ctx, "owner@example.com", squatterPassword, meta); !errors.Is(err, account.ErrInvalidCredentials) {
		t.Errorf("the squatter's password still signs in: %v", err)
	}
	if h.store.users[owner.User.ID].PasswordHash != "" {
		t.Error("the password is still stored")
	}
	if sess, err := h.svc.Authenticate(ctx, owner.Token); err != nil || sess.UserID != owner.User.ID {
		t.Errorf("the owner's own session: %v", err)
	}

	// The owner can still give the account a password of their own, by the
	// emailed link, which proves the address too.
	if err := h.svc.RequestPasswordReset(ctx, "owner@example.com", "http://web"); err != nil {
		t.Fatal(err)
	}
	body := h.mailer.sent[len(h.mailer.sent)-1]
	token := strings.Fields(body[strings.Index(body, "token=")+len("token="):])[0]
	if err := h.svc.ResetPassword(ctx, token, "owners-new-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.SignIn(ctx, "owner@example.com", "owners-new-password", meta); err != nil {
		t.Errorf("the owner's new password: %v", err)
	}
}

func TestClaim_AnAccountVerifiedBeforeKeepsItsPasswordAndSessions(t *testing.T) {
	h := newLoginCodeHarness(t)
	ctx := context.Background()
	made, err := h.svc.SignUp(ctx, "", "owner@example.com", "owners-own-password", meta)
	if err != nil {
		t.Fatal(err)
	}
	// Its address was proved earlier (by a provider, say): the password on it is the owner's.
	at := *h.clock
	h.store.users[made.User.ID].EmailVerifiedAt = &at

	if err := h.svc.RequestLoginCode(ctx, "owner@example.com", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.SignInWithLoginCode(ctx, "owner@example.com", h.lastCode(t), meta); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Authenticate(ctx, made.Token); err != nil {
		t.Errorf("signing in with a code signed out a verified account's other session: %v", err)
	}
	if _, err := h.svc.SignIn(ctx, "owner@example.com", "owners-own-password", meta); err != nil {
		t.Errorf("signing in with a code took a verified account's password: %v", err)
	}
}

func TestClaim_AProviderThatVerifiesTheAddressDoesTheSame(t *testing.T) {
	h := newAccountHarness(t)
	ctx := context.Background()
	squatter, err := h.svc.SignUp(ctx, "", "lin@example.com", squatterPassword, meta)
	if err != nil {
		t.Fatal(err)
	}

	res, err := h.svc.SignInWithOAuth(ctx, account.OAuthProfile{
		Provider: "github", ProviderUserID: "42", Email: "LIN@example.com", EmailVerified: true, Name: "Lin",
	}, meta)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created || res.User.ID != squatter.User.ID || res.User.EmailVerifiedAt == nil {
		t.Fatalf("want the account that was there, now verified: %+v (created %v)", res.User, res.Created)
	}
	if res.User.Name != "Lin" {
		t.Errorf("the name is still filled in from the provider: %q", res.User.Name)
	}
	if h.store.oauth["github|42"] != squatter.User.ID {
		t.Error("the provider is linked to the account")
	}
	if _, err := h.svc.Authenticate(ctx, squatter.Token); !errors.Is(err, account.ErrSessionInvalid) {
		t.Errorf("the squatter's session survived: %v", err)
	}
	if _, err := h.svc.SignIn(ctx, "lin@example.com", squatterPassword, meta); !errors.Is(err, account.ErrInvalidCredentials) {
		t.Errorf("the squatter's password still signs in: %v", err)
	}
	// Coming back by the provider is still the same account.
	again, err := h.svc.SignInWithOAuth(ctx, account.OAuthProfile{Provider: "github", ProviderUserID: "42", Email: "lin@example.com", EmailVerified: true}, meta)
	if err != nil || again.User.ID != squatter.User.ID {
		t.Errorf("signing in again: %v", err)
	}
}

// flakyAccountStore fails to clear a password on demand.
type flakyAccountStore struct {
	*fakeAccountStore
	failSetPassword bool
}

func (f *flakyAccountStore) SetPassword(ctx context.Context, id, hash string) error {
	if f.failSetPassword {
		return errors.New("the database is down")
	}
	return f.fakeAccountStore.SetPassword(ctx, id, hash)
}

func TestClaim_AFailurePartWayLeavesTheAddressUnverifiedAndNothingLinked(t *testing.T) {
	enc, err := privacy.NewAESGCMEncryptor(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := &flakyAccountStore{fakeAccountStore: newFakeAccountStore()}
	agents := newFakeAgentStore()
	mailer := &fakeMailer{}
	svc := NewAccountService(store, NewAgentService(agents), agents, enc, mailer,
		&fakeGuardrailStore{data: map[string]account.Guardrails{}}, time.Hour)
	svc.SetLoginCodes(newMemLoginCodes(), []byte("a-server-secret-of-its-own-32by"))
	now := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	h := loginCodeHarness{accountHarness: accountHarness{svc: svc, store: store.fakeAccountStore, agents: agents, mailer: mailer}, clock: &now}
	ctx := context.Background()

	squatter, err := svc.SignUp(ctx, "", "owner@example.com", squatterPassword, meta)
	if err != nil {
		t.Fatal(err)
	}

	// The password can't be cleared: neither way of proving the address gets in,
	// the address stays unverified, and the provider is not linked.
	store.failSetPassword = true
	if err := svc.RequestLoginCode(ctx, "owner@example.com", ""); err != nil {
		t.Fatal(err)
	}
	code := h.lastCode(t)
	if _, err := svc.SignInWithLoginCode(ctx, "owner@example.com", code, meta); err == nil {
		t.Fatal("signed in with the squatter's password still set")
	}
	if _, err := svc.SignInWithOAuth(ctx, account.OAuthProfile{Provider: "github", ProviderUserID: "7", Email: "owner@example.com", EmailVerified: true}, meta); err == nil {
		t.Fatal("a provider signed in with the squatter's password still set")
	}
	if u := store.users[squatter.User.ID]; u.EmailVerifiedAt != nil || u.PasswordHash == "" {
		t.Errorf("a failed claim must change nothing: %+v", u)
	}
	if len(store.oauth) != 0 {
		t.Errorf("a failed claim linked a provider: %v", store.oauth)
	}
	if _, err := svc.Authenticate(ctx, squatter.Token); err != nil {
		t.Errorf("a failed claim must not half-sign-out: %v", err)
	}

	// Once the database is back, the next proof does the whole claim.
	store.failSetPassword = false
	now = now.Add(account.LoginCodeCooldown)
	if err := svc.RequestLoginCode(ctx, "owner@example.com", ""); err != nil {
		t.Fatal(err)
	}
	owner, err := svc.SignInWithLoginCode(ctx, "owner@example.com", h.lastCode(t), meta)
	if err != nil {
		t.Fatal(err)
	}
	if owner.User.EmailVerifiedAt == nil || store.users[squatter.User.ID].PasswordHash != "" {
		t.Errorf("the claim did not complete: %+v", store.users[squatter.User.ID])
	}
	if _, err := svc.Authenticate(ctx, squatter.Token); !errors.Is(err, account.ErrSessionInvalid) {
		t.Errorf("the squatter's session survived: %v", err)
	}
}
