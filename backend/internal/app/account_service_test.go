package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/account"
	agentpkg "github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/privacy"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/policy"
)

type fakeAccountStore struct {
	mu       sync.Mutex
	users    map[string]*account.User
	oauth    map[string]string // provider|id -> userID
	sessions map[string]*account.Session
	resets   map[string]resetRow
}

type resetRow struct {
	userID  string
	expires time.Time
	used    bool
}

func newFakeAccountStore() *fakeAccountStore {
	return &fakeAccountStore{
		users: map[string]*account.User{}, oauth: map[string]string{},
		sessions: map[string]*account.Session{}, resets: map[string]resetRow{},
	}
}

func (f *fakeAccountStore) EraseUser(_ context.Context, userID string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[userID]
	if !ok {
		return shared.ErrNotFound
	}
	u.Email = "deleted-" + userID + "@deleted.invalid"
	u.Name, u.PasswordHash = "", ""
	for k, id := range f.oauth {
		if id == userID {
			delete(f.oauth, k)
		}
	}
	for k, s := range f.sessions {
		if s.UserID == userID {
			delete(f.sessions, k)
		}
	}
	return nil
}

func (f *fakeAccountStore) CreateUser(_ context.Context, u *account.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, existing := range f.users {
		if existing.Email == u.Email {
			return shared.ErrConflict
		}
	}
	cp := *u
	f.users[u.ID] = &cp
	return nil
}

func (f *fakeAccountStore) GetUser(_ context.Context, id string) (*account.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok {
		return nil, shared.ErrNotFound
	}
	cp := *u
	return &cp, nil
}

func (f *fakeAccountStore) GetUserByEmail(_ context.Context, email string) (*account.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.users {
		if u.Email == email {
			cp := *u
			return &cp, nil
		}
	}
	return nil, shared.ErrNotFound
}

func (f *fakeAccountStore) UpdateUserProfile(_ context.Context, id, name, avatar string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok {
		return shared.ErrNotFound
	}
	u.Name, u.AvatarURL = name, avatar
	return nil
}

func (f *fakeAccountStore) SetPassword(_ context.Context, id, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[id].PasswordHash = hash
	return nil
}

func (f *fakeAccountStore) MarkEmailVerified(_ context.Context, id string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[id].EmailVerifiedAt = &at
	return nil
}

func (f *fakeAccountStore) MarkOnboarded(_ context.Context, id string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[id].OnboardedAt = &at
	return nil
}

func (f *fakeAccountStore) GetOAuthIdentity(_ context.Context, provider, pid string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := f.oauth[provider+"|"+pid]; ok {
		return id, nil
	}
	return "", shared.ErrNotFound
}

func (f *fakeAccountStore) LinkOAuthIdentity(_ context.Context, provider, pid, userID, _ string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.oauth[provider+"|"+pid] = userID
	return nil
}

func (f *fakeAccountStore) ListOAuthProviders(_ context.Context, userID string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for k, v := range f.oauth {
		if v == userID {
			out = append(out, strings.SplitN(k, "|", 2)[0])
		}
	}
	return out, nil
}

func (f *fakeAccountStore) CreateSession(_ context.Context, s *account.Session) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *s
	f.sessions[s.ID] = &cp
	return nil
}

func (f *fakeAccountStore) GetSessionByTokenHash(_ context.Context, hash string) (*account.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.sessions {
		if s.TokenHash == hash {
			cp := *s
			return &cp, nil
		}
	}
	return nil, shared.ErrNotFound
}

func (f *fakeAccountStore) TouchSession(_ context.Context, id string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[id].LastSeenAt = at
	return nil
}

func (f *fakeAccountStore) RevokeSession(_ context.Context, id string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[id].RevokedAt = &at
	return nil
}

func (f *fakeAccountStore) ListActiveSessions(_ context.Context, userID string, now time.Time) ([]account.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []account.Session
	for _, s := range f.sessions {
		if s.UserID == userID && s.Active(now) {
			out = append(out, *s)
		}
	}
	return out, nil
}

func (f *fakeAccountStore) PurgeExpired(_ context.Context, _ time.Time) (int64, error) { return 0, nil }

func (f *fakeAccountStore) CreateResetToken(_ context.Context, hash, userID string, expires time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resets[hash] = resetRow{userID: userID, expires: expires}
	return nil
}

func (f *fakeAccountStore) ConsumeResetToken(_ context.Context, hash string, now time.Time) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.resets[hash]
	if !ok || r.used || !now.Before(r.expires) {
		return "", shared.ErrNotFound
	}
	r.used = true
	f.resets[hash] = r
	return r.userID, nil
}

type fakeGuardrailStore struct{ data map[string]account.Guardrails }

func (f *fakeGuardrailStore) Get(_ context.Context, userID string) (*account.Guardrails, error) {
	g, ok := f.data[userID]
	if !ok {
		return nil, shared.ErrNotFound
	}
	return &g, nil
}

func (f *fakeGuardrailStore) Upsert(_ context.Context, userID string, g account.Guardrails, _ time.Time) error {
	f.data[userID] = g
	return nil
}

type fakeMailer struct{ sent []string }

func (m *fakeMailer) Send(_ context.Context, _, _, text string) error {
	m.sent = append(m.sent, text)
	return nil
}

type accountHarness struct {
	svc    *AccountService
	store  *fakeAccountStore
	agents *fakeAgentStore
	mailer *fakeMailer
}

func newAccountHarness(t *testing.T) accountHarness {
	t.Helper()
	enc, err := privacy.NewAESGCMEncryptor(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := newFakeAccountStore()
	agents := newFakeAgentStore()
	mailer := &fakeMailer{}
	svc := NewAccountService(store, NewAgentService(agents), agents, enc, mailer,
		&fakeGuardrailStore{data: map[string]account.Guardrails{}}, time.Hour)
	return accountHarness{svc: svc, store: store, agents: agents, mailer: mailer}
}

var meta = account.ClientMeta{UserAgent: "test", IP: "127.0.0.1"}

func TestAccount_SignUpThenSignIn(t *testing.T) {
	h := newAccountHarness(t)
	ctx := context.Background()

	res, err := h.svc.SignUp(ctx, "Ada", "Ada@Example.com", "hunter2hunter2", meta)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.User.Email != "ada@example.com" {
		t.Fatalf("unexpected sign-up result: %+v", res.User)
	}
	if _, err := h.svc.SignUp(ctx, "", "ada@example.com", "anotherpass1", meta); !errors.Is(err, account.ErrEmailTaken) {
		t.Errorf("duplicate sign-up: got %v, want ErrEmailTaken", err)
	}

	if _, err := h.svc.SignIn(ctx, "ada@example.com", "wrong-password", meta); !errors.Is(err, account.ErrInvalidCredentials) {
		t.Errorf("wrong password: got %v", err)
	}
	if _, err := h.svc.SignIn(ctx, "nobody@example.com", "whatever12", meta); !errors.Is(err, account.ErrInvalidCredentials) {
		t.Errorf("unknown email must look identical to a wrong password: got %v", err)
	}
	in, err := h.svc.SignIn(ctx, " ADA@example.com ", "hunter2hunter2", meta)
	if err != nil {
		t.Fatalf("sign-in: %v", err)
	}
	sess, err := h.svc.Authenticate(ctx, in.Token)
	if err != nil || sess.UserID != res.User.ID {
		t.Fatalf("authenticate: %v %+v", err, sess)
	}
}

func TestAccount_SessionAgentCanShopButIsRevokedOnSignOut(t *testing.T) {
	h := newAccountHarness(t)
	ctx := context.Background()
	res, _ := h.svc.SignUp(ctx, "", "sam@example.com", "longpassword", meta)

	token, err := h.svc.AgentToken(res.Session)
	if err != nil {
		t.Fatal(err)
	}
	ag, err := h.agents.GetByTokenHash(ctx, agentpkg.HashToken(token))
	if err != nil {
		t.Fatalf("unsealed token doesn't resolve to the session's agent: %v", err)
	}
	if ag.ID != res.Session.AgentID || !ag.HasPermission(agentpkg.PermShoppingExecute) || !ag.HasPermission(agentpkg.PermProfilesWrite) {
		t.Errorf("console agent missing expected permissions: %+v", ag.Permissions)
	}

	if err := h.svc.SignOut(ctx, res.Session); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Authenticate(ctx, res.Token); !errors.Is(err, account.ErrSessionInvalid) {
		t.Errorf("signed-out session still authenticates: %v", err)
	}
	ag, _ = h.agents.Get(ctx, res.Session.AgentID)
	if !ag.IsRevoked() {
		t.Error("sign-out must revoke the session's console agent too")
	}
}

func TestAccount_OAuthLinksOnlyVerifiedEmails(t *testing.T) {
	h := newAccountHarness(t)
	ctx := context.Background()
	pw, _ := h.svc.SignUp(ctx, "", "lin@example.com", "longpassword", meta)

	if _, err := h.svc.SignInWithOAuth(ctx, account.OAuthProfile{
		Provider: "github", ProviderUserID: "42", Email: "lin@example.com", EmailVerified: false,
	}, meta); err == nil {
		t.Fatal("unverified provider email must not sign in (account takeover vector)")
	}

	res, err := h.svc.SignInWithOAuth(ctx, account.OAuthProfile{
		Provider: "github", ProviderUserID: "42", Email: "LIN@example.com", EmailVerified: true, Name: "Lin",
	}, meta)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created || res.User.ID != pw.User.ID {
		t.Errorf("verified email should link to the existing account, got created=%v id=%s", res.Created, res.User.ID)
	}
	if res.User.Name != "Lin" {
		t.Errorf("missing name should be backfilled from the provider, got %q", res.User.Name)
	}

	again, err := h.svc.SignInWithOAuth(ctx, account.OAuthProfile{
		Provider: "github", ProviderUserID: "42", Email: "changed@example.com", EmailVerified: true,
	}, meta)
	if err != nil || again.User.ID != pw.User.ID {
		t.Errorf("returning OAuth user must resolve by provider id, not email: %v", err)
	}

	fresh, err := h.svc.SignInWithOAuth(ctx, account.OAuthProfile{
		Provider: "google", ProviderUserID: "g-1", Email: "new@example.com", EmailVerified: true,
	}, meta)
	if err != nil || !fresh.Created || fresh.User.EmailVerifiedAt == nil {
		t.Errorf("new OAuth user should be created verified: %v %+v", err, fresh)
	}
}

func TestAccount_PasswordResetIsSingleUseAndSignsOutEverywhere(t *testing.T) {
	h := newAccountHarness(t)
	ctx := context.Background()
	res, _ := h.svc.SignUp(ctx, "", "kay@example.com", "oldpassword", meta)

	if err := h.svc.RequestPasswordReset(ctx, "nobody@example.com", "http://web"); err != nil || len(h.mailer.sent) != 0 {
		t.Fatalf("unknown email must succeed silently without sending: err=%v sent=%d", err, len(h.mailer.sent))
	}
	if err := h.svc.RequestPasswordReset(ctx, "kay@example.com", "http://web/"); err != nil {
		t.Fatal(err)
	}
	if len(h.mailer.sent) != 1 {
		t.Fatalf("expected one email, got %d", len(h.mailer.sent))
	}
	body := h.mailer.sent[0]
	i := strings.Index(body, "token=")
	token := strings.Fields(body[i+len("token="):])[0]
	if !strings.Contains(body, "http://web/reset-password?token=") {
		t.Errorf("reset link malformed: %s", body)
	}

	if err := h.svc.ResetPassword(ctx, token, "newpassword1"); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.ResetPassword(ctx, token, "newpassword2"); err == nil {
		t.Error("reset token reused")
	}
	if _, err := h.svc.Authenticate(ctx, res.Token); err == nil {
		t.Error("existing session survived a password reset")
	}
	if _, err := h.svc.SignIn(ctx, "kay@example.com", "newpassword1", meta); err != nil {
		t.Errorf("new password rejected: %v", err)
	}
}

func TestAccount_GuardrailsDriveThePolicyEngine(t *testing.T) {
	h := newAccountHarness(t)
	ctx := context.Background()
	res, _ := h.svc.SignUp(ctx, "", "raj@example.com", "longpassword", meta)

	if rules, err := h.svc.RulesFor(ctx, res.User.ID); err != nil || rules != nil {
		t.Fatalf("no guardrails set → platform default (nil rules), got %v %v", rules, err)
	}
	if _, err := h.svc.SetGuardrails(ctx, res.User.ID, account.Guardrails{
		MaxPerDayMinorUnits: 1_000_000, ApprovalThresholdMinorUnits: 250_000, BlockedCategories: []string{"alcohol"},
	}); err != nil {
		t.Fatal(err)
	}
	provider, err := providerFor(ctx, h.svc, policy.NewLocalProvider(policy.DefaultRules()), res.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	base := policy.Input{Merchant: "mock", Currency: "INR", PaymentProfile: "payment:personal", ShippingProfile: "shipping:home"}

	// ₹1,500: above the platform default's ₹1,000 approval threshold, but
	// under this user's ₹2,500 — auto-approved for them.
	in := base
	in.AmountMinorUnits = 150_000
	if d, _ := provider.EvaluatePurchaseIntent(ctx, in); d.Decision != policy.Allow {
		t.Errorf("₹1,500 under a ₹2,500 threshold: got %s %v", d.Decision, d.ReasonCodes)
	}
	in.AmountMinorUnits = 300_000
	if d, _ := provider.EvaluatePurchaseIntent(ctx, in); d.Decision != policy.RequireApproval {
		t.Errorf("₹3,000 over threshold: got %s", d.Decision)
	}
	in.AmountMinorUnits = 10_000
	in.Category = "alcohol"
	if d, _ := provider.EvaluatePurchaseIntent(ctx, in); d.Decision != policy.Deny {
		t.Errorf("user-blocked category: got %s", d.Decision)
	}
}
