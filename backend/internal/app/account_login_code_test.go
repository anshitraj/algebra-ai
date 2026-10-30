package app

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/account"
)

// memLoginCodes keeps login codes the way the database does: one per address,
// tries counted, burned at maxTries, gone when expired.
type memLoginCodes struct {
	mu   sync.Mutex
	rows map[string]*memLoginCode
}

type memLoginCode struct {
	hash         string
	tries        int
	created, exp time.Time
}

func newMemLoginCodes() *memLoginCodes { return &memLoginCodes{rows: map[string]*memLoginCode{}} }

func (m *memLoginCodes) LoginCodeSentAt(_ context.Context, key string) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r := m.rows[key]; r != nil {
		return r.created, nil
	}
	return time.Time{}, nil
}

func (m *memLoginCodes) PutLoginCode(_ context.Context, key, hash string, at, exp time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[key] = &memLoginCode{hash: hash, created: at, exp: exp}
	return nil
}

func (m *memLoginCodes) TryLoginCode(_ context.Context, key, hash string, now time.Time, maxTries int) (LoginCodeOutcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.rows[key]
	if r == nil {
		return LoginCodeGone, nil
	}
	if !now.Before(r.exp) {
		delete(m.rows, key)
		return LoginCodeGone, nil
	}
	if r.hash == hash {
		delete(m.rows, key)
		return LoginCodeAccepted, nil
	}
	if r.tries+1 >= maxTries {
		delete(m.rows, key)
		return LoginCodeGone, nil
	}
	r.tries++
	return LoginCodeWrong, nil
}

type loginCodeHarness struct {
	accountHarness
	codes *memLoginCodes
	clock *time.Time
}

func newLoginCodeHarness(t *testing.T) loginCodeHarness {
	t.Helper()
	h := newAccountHarness(t)
	codes := newMemLoginCodes()
	h.svc.SetLoginCodes(codes, []byte("a-server-secret-of-its-own-32by"))
	now := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	h.svc.now = func() time.Time { return now }
	return loginCodeHarness{accountHarness: h, codes: codes, clock: &now}
}

var sixDigits = regexp.MustCompile(`\b(\d{6})\b`)

// lastCode is the code in the latest email sent.
func (h loginCodeHarness) lastCode(t *testing.T) string {
	t.Helper()
	if len(h.mailer.sent) == 0 {
		t.Fatal("no email was sent")
	}
	m := sixDigits.FindStringSubmatch(h.mailer.sent[len(h.mailer.sent)-1])
	if m == nil {
		t.Fatalf("no six-digit code in the email: %q", h.mailer.sent[len(h.mailer.sent)-1])
	}
	return m[1]
}

func TestLoginCode_ACodeSignsInAndMakesTheAccount(t *testing.T) {
	h := newLoginCodeHarness(t)
	ctx := context.Background()
	if err := h.svc.RequestLoginCode(ctx, " New.Person@Example.com ", "https://algebra.test"); err != nil {
		t.Fatal(err)
	}
	if len(h.mailer.sent) != 1 {
		t.Fatalf("%d emails sent, want 1", len(h.mailer.sent))
	}
	code := h.lastCode(t)

	res, err := h.svc.SignInWithLoginCode(ctx, "new.person@example.com", code, meta)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.User.Email != "new.person@example.com" || res.User.EmailVerifiedAt == nil {
		t.Fatalf("sign-in made %+v (created %v), want a new verified account", res.User, res.Created)
	}
	if sess, err := h.svc.Authenticate(ctx, res.Token); err != nil || sess.UserID != res.User.ID {
		t.Fatalf("the session doesn't authenticate: %v %+v", err, sess)
	}
	// A code works once.
	if _, err := h.svc.SignInWithLoginCode(ctx, "new.person@example.com", code, meta); !errors.Is(err, account.ErrLoginCodeGone) {
		t.Fatalf("the same code again: %v, want it gone", err)
	}
}

func TestLoginCode_AnExistingAccountIsTheSameAccount(t *testing.T) {
	h := newLoginCodeHarness(t)
	ctx := context.Background()
	made, err := h.svc.SignUp(ctx, "Ada", "ada@example.com", "hunter2hunter2", meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.RequestLoginCode(ctx, "ada@example.com", ""); err != nil {
		t.Fatal(err)
	}
	res, err := h.svc.SignInWithLoginCode(ctx, "ADA@example.com", h.lastCode(t), meta)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created || res.User.ID != made.User.ID {
		t.Fatalf("signed in as %s (created %v), want the existing account %s", res.User.ID, res.Created, made.User.ID)
	}
	if res.User.EmailVerifiedAt == nil {
		t.Fatal("a code proves the address: it should count as verified now")
	}
}

func TestLoginCode_WrongCodesAreCountedAndBurnTheCode(t *testing.T) {
	h := newLoginCodeHarness(t)
	ctx := context.Background()
	if err := h.svc.RequestLoginCode(ctx, "guess@example.com", ""); err != nil {
		t.Fatal(err)
	}
	right := h.lastCode(t)
	wrong := "000000"
	if right == wrong {
		wrong = "111111"
	}
	for i := 1; i < account.LoginCodeAttempts; i++ {
		if _, err := h.svc.SignInWithLoginCode(ctx, "guess@example.com", wrong, meta); !errors.Is(err, account.ErrLoginCodeWrong) {
			t.Fatalf("wrong guess %d: %v, want wrong", i, err)
		}
	}
	// The last try the code allows burns it.
	if _, err := h.svc.SignInWithLoginCode(ctx, "guess@example.com", wrong, meta); !errors.Is(err, account.ErrLoginCodeGone) {
		t.Fatalf("the guess that used up the tries: %v, want gone", err)
	}
	// Now even the right code is no use.
	if _, err := h.svc.SignInWithLoginCode(ctx, "guess@example.com", right, meta); !errors.Is(err, account.ErrLoginCodeGone) {
		t.Fatalf("the right code after the burn: %v, want gone", err)
	}
}

func TestLoginCode_ExpiresAfterTenMinutes(t *testing.T) {
	h := newLoginCodeHarness(t)
	ctx := context.Background()
	if err := h.svc.RequestLoginCode(ctx, "late@example.com", ""); err != nil {
		t.Fatal(err)
	}
	code := h.lastCode(t)
	*h.clock = h.clock.Add(account.LoginCodeTTL + time.Second)
	if _, err := h.svc.SignInWithLoginCode(ctx, "late@example.com", code, meta); !errors.Is(err, account.ErrLoginCodeGone) {
		t.Fatalf("an expired code: %v, want gone", err)
	}
}

func TestLoginCode_AskingAgainTooSoonIsRefusedThenReplacesTheCode(t *testing.T) {
	h := newLoginCodeHarness(t)
	ctx := context.Background()
	if err := h.svc.RequestLoginCode(ctx, "twice@example.com", ""); err != nil {
		t.Fatal(err)
	}
	first := h.lastCode(t)
	*h.clock = h.clock.Add(10 * time.Second)
	var soon *account.LoginCodeTooSoon
	if err := h.svc.RequestLoginCode(ctx, "twice@example.com", ""); !errors.As(err, &soon) || soon.RetryAfter <= 0 || soon.RetryAfter > account.LoginCodeCooldown {
		t.Fatalf("a second request after 10 s: %v, want too soon", err)
	}
	if len(h.mailer.sent) != 1 {
		t.Fatalf("%d emails sent, want 1: a refused request sends nothing", len(h.mailer.sent))
	}
	*h.clock = h.clock.Add(account.LoginCodeCooldown)
	if err := h.svc.RequestLoginCode(ctx, "twice@example.com", ""); err != nil {
		t.Fatal(err)
	}
	second := h.lastCode(t)
	if first != second {
		if _, err := h.svc.SignInWithLoginCode(ctx, "twice@example.com", first, meta); err == nil {
			t.Fatal("the first code still works after a new one was sent")
		}
	}
	if _, err := h.svc.SignInWithLoginCode(ctx, "twice@example.com", second, meta); err != nil {
		t.Fatalf("the new code: %v", err)
	}
}

func TestLoginCode_ACodeIsForOneAddress(t *testing.T) {
	h := newLoginCodeHarness(t)
	ctx := context.Background()
	if err := h.svc.RequestLoginCode(ctx, "one@example.com", ""); err != nil {
		t.Fatal(err)
	}
	code := h.lastCode(t)
	if _, err := h.svc.SignInWithLoginCode(ctx, "two@example.com", code, meta); err == nil {
		t.Fatal("a code sent to one address signed in another")
	}
}

func TestLoginCode_BadShapesAreRefusedWithoutSpendingTheCode(t *testing.T) {
	h := newLoginCodeHarness(t)
	ctx := context.Background()
	if err := h.svc.RequestLoginCode(ctx, "shape@example.com", ""); err != nil {
		t.Fatal(err)
	}
	code := h.lastCode(t)
	for _, bad := range []string{"", "12345", "1234567", "12345a", "12 456", "١٢٣٤٥٦"} {
		if _, err := h.svc.SignInWithLoginCode(ctx, "shape@example.com", bad, meta); !errors.Is(err, account.ErrLoginCodeWrong) {
			t.Fatalf("code %q: %v, want wrong", bad, err)
		}
	}
	if _, err := h.svc.SignInWithLoginCode(ctx, "shape@example.com", " "+code+" ", meta); err != nil {
		t.Fatalf("the right code, with spaces round it: %v", err)
	}
}

func TestLoginCode_AnInvalidAddressSendsNothing(t *testing.T) {
	h := newLoginCodeHarness(t)
	if err := h.svc.RequestLoginCode(context.Background(), "not an address", ""); err == nil {
		t.Fatal("expected an error for a bad address")
	}
	if len(h.mailer.sent) != 0 {
		t.Fatal("an email went to a bad address")
	}
}

func TestLoginCode_NothingStoredNamesTheAddressOrTheCode(t *testing.T) {
	h := newLoginCodeHarness(t)
	ctx := context.Background()
	if err := h.svc.RequestLoginCode(ctx, "private.person@example.com", ""); err != nil {
		t.Fatal(err)
	}
	code := h.lastCode(t)
	h.codes.mu.Lock()
	defer h.codes.mu.Unlock()
	for key, row := range h.codes.rows {
		if strings.Contains(key, "private") || strings.Contains(key, "example") || strings.Contains(row.hash, code) {
			t.Fatalf("the table holds the address or the code: key %q hash %q", key, row.hash)
		}
	}
}

func TestLoginCode_OffWhenNotSetUp(t *testing.T) {
	h := newAccountHarness(t)
	if h.svc.LoginCodesOn() {
		t.Fatal("codes are on with no store")
	}
	if err := h.svc.RequestLoginCode(context.Background(), "a@example.com", ""); !errors.Is(err, ErrLoginCodesOff) {
		t.Fatalf("request: %v, want off", err)
	}
	if _, err := h.svc.SignInWithLoginCode(context.Background(), "a@example.com", "123456", meta); !errors.Is(err, ErrLoginCodesOff) {
		t.Fatalf("sign-in: %v, want off", err)
	}
}

func TestLoginCode_CodesAreSixDigitsAndDifferent(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		c, err := account.NewLoginCode()
		if err != nil {
			t.Fatal(err)
		}
		if !account.ValidLoginCodeShape(c) {
			t.Fatalf("code %q isn't six digits", c)
		}
		seen[c] = true
	}
	if len(seen) < 150 {
		t.Fatalf("only %d different codes in 200: not random", len(seen))
	}
}
