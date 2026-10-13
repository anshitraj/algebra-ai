package app

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/account"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/receipt"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// Email notifications: who is written to, when, and what an address that
// was typed in but never confirmed can receive (its confirmation link, and
// nothing else).

type notifyMail struct {
	to, subject, body, filename string
	file                        []byte
}

type fakeNotifyMailer struct {
	mu   sync.Mutex
	sent []notifyMail
	err  error
}

func (m *fakeNotifyMailer) Send(_ context.Context, to, subject, text string) error {
	return m.SendWithAttachment(context.Background(), to, subject, text, "", nil)
}

func (m *fakeNotifyMailer) SendWithAttachment(_ context.Context, to, subject, text, filename string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.sent = append(m.sent, notifyMail{to, subject, text, filename, data})
	return nil
}

func (m *fakeNotifyMailer) all() []notifyMail {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]notifyMail(nil), m.sent...)
}

// textOnlyMailer can't attach: the receipt still goes, with its links.
type textOnlyMailer struct{ inner *fakeNotifyMailer }

func (m textOnlyMailer) Send(ctx context.Context, to, subject, text string) error {
	return m.inner.Send(ctx, to, subject, text)
}

type fakeNotifyStore struct {
	mu   sync.Mutex
	rows map[string]account.NotifySettings
}

func (s *fakeNotifyStore) GetNotifySettings(_ context.Context, userID string) (*account.NotifySettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[userID]
	if !ok {
		return nil, shared.ErrNotFound
	}
	return &r, nil
}

func (s *fakeNotifyStore) SaveNotifySettings(_ context.Context, st *account.NotifySettings, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[st.UserID] = *st
	return nil
}

type fakeNotifyUsers map[string]*account.User

func (u fakeNotifyUsers) GetUser(_ context.Context, id string) (*account.User, error) {
	if x, ok := u[id]; ok {
		return x, nil
	}
	return nil, shared.ErrNotFound
}

// fakeReceipts answers Verify with the claims a test put under a receipt.
type fakeReceipts map[string]*receipt.IntentClaims

func (f fakeReceipts) Verify(_ context.Context, jws string) *IntentVerification {
	c, ok := f[jws]
	if !ok {
		return &IntentVerification{Reason: "unknown"}
	}
	return &IntentVerification{Valid: true, Recorded: true, Claims: c}
}

var (
	emailUser  = &account.User{ID: "user_email", Email: "ada@example.test", Name: "Ada"}
	walletUser = &account.User{ID: "user_wallet", Email: "wallet-abc@" + account.WalletEmailDomain}
	demoUser   = &account.User{ID: "user_demo", Email: "demo-abc@demo.algebra.invalid", Mode: account.ModeDemo}
)

type notifyRig struct {
	svc    *NotificationService
	mail   *fakeNotifyMailer
	clock  *time.Time
	claims fakeReceipts
}

func newNotifyRig(t *testing.T) *notifyRig {
	t.Helper()
	mail := &fakeNotifyMailer{}
	users := fakeNotifyUsers{emailUser.ID: emailUser, walletUser.ID: walletUser, demoUser.ID: demoUser}
	svc := NewNotificationService(&fakeNotifyStore{rows: map[string]account.NotifySettings{}}, users, mail, "https://algebra.example/", true, nil)
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	claims := fakeReceipts{}
	svc.SetReceipts(claims)
	svc.SetReceiptPDF(func(v *IntentView, _ *IntentVerification) []byte { return []byte("%PDF-1.4 " + v.ID) })
	return &notifyRig{svc: svc, mail: mail, clock: &now, claims: claims}
}

func (r *notifyRig) advance(d time.Duration) { *r.clock = r.clock.Add(d) }

// paid is a committed intent of owner's with its receipt on network.
func (r *notifyRig) paid(owner *account.User, id, network string, minor int64) *IntentView {
	jws := "jws-" + id
	r.claims[jws] = &receipt.IntentClaims{
		ID: "ircpt_" + id, IssuedAt: r.clock.Unix(), Provider: receipt.ProviderRef{ID: "demo:beta"},
		Authority:  receipt.AuthorityRef{Method: receipt.MethodHuman, PassID: "pass_1"},
		Settlement: &receipt.SettlementRef{Network: network, Transaction: "5z5S2V9k" + id, Amount: receipt.Money{MinorUnits: minor, Currency: "USDC"}},
		Test:       network != "solana",
	}
	return &IntentView{Intent: econ.Intent{ID: id, PrincipalID: owner.ID, Capability: "token.price", Currency: "USDC", State: econ.StateCommitted}, Receipt: jws}
}

var confirmLink = regexp.MustCompile(`https://algebra\.example/console/settings\?confirm_email=(\S+)`)

func tokenIn(t *testing.T, m notifyMail) string {
	t.Helper()
	got := confirmLink.FindStringSubmatch(m.body)
	if got == nil {
		t.Fatalf("no confirmation link in: %s", m.body)
	}
	return got[1]
}

func TestNotifications_ASignInIsEmailedToTheAddressTheySignInWith(t *testing.T) {
	r := newNotifyRig(t)
	ctx := context.Background()
	meta := account.ClientMeta{UserAgent: "Mozilla/5.0 (Windows NT 10.0) Chrome/130.0 Safari/537.36", IP: "203.0.113.7"}

	r.svc.SignedIn(ctx, emailUser, meta, false)
	r.svc.Wait()
	sent := r.mail.all()
	if len(sent) != 1 || sent[0].to != "ada@example.test" || sent[0].subject != "New sign-in to Algebra" {
		t.Fatalf("one sign-in email to the account's address: %+v", sent)
	}
	for _, want := range []string{"Hi Ada,", "13 October 2026, 09:00 UTC", "Chrome on Windows", "203.0.113.7", "https://algebra.example/console/settings"} {
		if !strings.Contains(sent[0].body, want) {
			t.Errorf("the email says %q:\n%s", want, sent[0].body)
		}
	}

	// A second session within the minute is not a second email.
	r.svc.SignedIn(ctx, emailUser, meta, false)
	r.svc.Wait()
	if n := len(r.mail.all()); n != 1 {
		t.Fatalf("one email a minute per account, got %d", n)
	}
	// The first sign-in of a new account is a welcome.
	r.advance(2 * time.Minute)
	r.svc.SignedIn(ctx, emailUser, meta, true)
	r.svc.Wait()
	if last := r.mail.all()[1]; last.subject != "Welcome to Algebra" || !strings.Contains(last.body, "/console/quickstart") {
		t.Fatalf("a welcome with where to start: %+v", last)
	}
}

func TestNotifications_AWalletAccountGetsNothingUntilItConfirmsAnAddress(t *testing.T) {
	r := newNotifyRig(t)
	ctx := context.Background()

	v, err := r.svc.View(ctx, walletUser)
	if err != nil || !v.NeedsEmail || v.Address != "" || !v.CanAdd {
		t.Fatalf("a wallet sign-in has nowhere to be written to, and is told so: %+v %v", v, err)
	}
	r.svc.SignedIn(ctx, walletUser, account.ClientMeta{}, false)
	r.svc.ReceiptSigned(ctx, r.paid(walletUser, "eint_1", "solana-devnet", 1000))
	r.svc.Wait()
	if n := len(r.mail.all()); n != 0 {
		t.Fatalf("the placeholder address is never written to, got %d emails", n)
	}

	// Asking for an address sends it one thing: the link.
	v, err = r.svc.SetEmail(ctx, walletUser, "  Grace@Example.Test ")
	if err != nil || v.PendingEmail != "grace@example.test" || v.Address != "" || !v.NeedsEmail {
		t.Fatalf("pending, not yet in use: %+v %v", v, err)
	}
	sent := r.mail.all()
	if len(sent) != 1 || sent[0].to != "grace@example.test" || !strings.Contains(sent[0].subject, "Confirm") {
		t.Fatalf("one confirmation email: %+v", sent)
	}
	token := tokenIn(t, sent[0])
	r.advance(2 * time.Minute)
	r.svc.SignedIn(ctx, walletUser, account.ClientMeta{}, false)
	r.svc.ReceiptSigned(ctx, r.paid(walletUser, "eint_2", "solana-devnet", 1000))
	r.svc.Wait()
	if n := len(r.mail.all()); n != 1 {
		t.Fatalf("an unconfirmed address gets no notifications, got %d emails", n)
	}

	// A wrong token, and another account holding the right one, confirm nothing.
	if _, err := r.svc.ConfirmEmail(ctx, walletUser, "alg_mail_wrong"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("a wrong token is refused: %v", err)
	}
	if _, err := r.svc.ConfirmEmail(ctx, emailUser, token); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("the link confirms only for the account that asked: %v", err)
	}
	v, err = r.svc.ConfirmEmail(ctx, walletUser, token)
	if err != nil || v.Address != "grace@example.test" || v.Source != "added" || v.NeedsEmail || v.PendingEmail != "" {
		t.Fatalf("confirmed and in use: %+v %v", v, err)
	}
	if _, err := r.svc.ConfirmEmail(ctx, walletUser, token); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("the link works once: %v", err)
	}

	r.advance(2 * time.Minute)
	r.svc.SignedIn(ctx, walletUser, account.ClientMeta{}, false)
	r.svc.Wait()
	if last := r.mail.all(); len(last) != 2 || last[1].to != "grace@example.test" || last[1].subject != "New sign-in to Algebra" {
		t.Fatalf("now the sign-in alert goes there: %+v", last)
	}

	// Removing it leaves the account with nowhere again.
	v, err = r.svc.RemoveEmail(ctx, walletUser)
	if err != nil || v.Address != "" || !v.NeedsEmail {
		t.Fatalf("removed: %+v %v", v, err)
	}
}

func TestNotifications_ConfirmationLinksAreRateLimitedExpireAndReplaceEachOther(t *testing.T) {
	r := newNotifyRig(t)
	ctx := context.Background()

	if _, err := r.svc.SetEmail(ctx, walletUser, "grace@example.test"); err != nil {
		t.Fatal(err)
	}
	first := tokenIn(t, r.mail.all()[0])
	var limited *RateLimited
	if _, err := r.svc.SetEmail(ctx, walletUser, "other@example.test"); !errors.As(err, &limited) || limited.Retry <= 0 {
		t.Fatalf("one confirmation email a minute: %v", err)
	}
	if n := len(r.mail.all()); n != 1 {
		t.Fatalf("the refused request sent nothing, got %d emails", n)
	}

	r.advance(61 * time.Second)
	if _, err := r.svc.SetEmail(ctx, walletUser, "other@example.test"); err != nil {
		t.Fatal(err)
	}
	second := tokenIn(t, r.mail.all()[1])
	if _, err := r.svc.ConfirmEmail(ctx, walletUser, first); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("a replaced link is dead: %v", err)
	}

	r.advance(25 * time.Hour)
	if _, err := r.svc.ConfirmEmail(ctx, walletUser, second); !errors.Is(err, ErrInvalidRequest) || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("a link lasts a day: %v", err)
	}
	if v, _ := r.svc.View(ctx, walletUser); v.PendingEmail != "" || v.Address != "" {
		t.Fatalf("an expired request isn't shown as pending: %+v", v)
	}
}

func TestNotifications_WhatSetEmailRefuses(t *testing.T) {
	r := newNotifyRig(t)
	ctx := context.Background()
	for name, c := range map[string]struct {
		u     *account.User
		email string
	}{
		"a demo account":           {demoUser, "grace@example.test"},
		"not an address":           {walletUser, "grace"},
		"an undeliverable address": {walletUser, "someone@wallet.algebra.invalid"},
	} {
		if _, err := r.svc.SetEmail(ctx, c.u, c.email); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s is refused: %v", name, err)
		}
	}
	if n := len(r.mail.all()); n != 0 {
		t.Fatalf("a refused request sends nothing, got %d emails", n)
	}

	// Naming the address they sign in with needs no link: it is theirs already.
	v, err := r.svc.SetEmail(ctx, emailUser, "ADA@example.test")
	if err != nil || v.Address != "ada@example.test" || v.Source != "account" || v.PendingEmail != "" || len(r.mail.all()) != 0 {
		t.Fatalf("back to the account's own address, with no email: %+v %v", v, err)
	}
}

func TestNotifications_AFailedConfirmationEmailCanBeRetriedAtOnce(t *testing.T) {
	r := newNotifyRig(t)
	ctx := context.Background()
	r.mail.err = errors.New("resend returned 403")
	if _, err := r.svc.SetEmail(ctx, walletUser, "grace@example.test"); err == nil || strings.Contains(err.Error(), "403") {
		t.Fatalf("the person is told it wasn't sent, without the provider's words: %v", err)
	}
	r.mail.err = nil
	if _, err := r.svc.SetEmail(ctx, walletUser, "grace@example.test"); err != nil {
		t.Fatalf("nothing reached the inbox, so no minute to wait: %v", err)
	}
}

func TestNotifications_AReceiptIsEmailedForAPaymentOnAChain(t *testing.T) {
	r := newNotifyRig(t)
	ctx := context.Background()

	r.svc.ReceiptSigned(ctx, r.paid(emailUser, "eint_dev", "solana-devnet", 1000))
	r.svc.Wait()
	sent := r.mail.all()
	if len(sent) != 1 || sent[0].to != "ada@example.test" {
		t.Fatalf("one receipt email: %+v", sent)
	}
	m := sent[0]
	if m.subject != "Receipt: 0.001 USDC paid to demo:beta (devnet, test USDC)" {
		t.Errorf("subject: %q", m.subject)
	}
	if m.filename != "algebra-receipt-eint_dev.pdf" || string(m.file) != "%PDF-1.4 eint_dev" {
		t.Errorf("the PDF is attached: %q %q", m.filename, m.file)
	}
	for _, want := range []string{
		"0.001 USDC", "demo:beta", "token.price", "Solana devnet (test USDC, no real value)", "5z5S2V9keint_dev",
		"https://explorer.solana.com/tx/5z5S2V9keint_dev?cluster=devnet", "https://solscan.io/tx/5z5S2V9keint_dev?cluster=devnet",
		"Approved by you", "ircpt_eint_dev", "https://algebra.example/console/executions/eint_dev/invoice", "https://algebra.example/verify",
		"not a tax invoice",
	} {
		if !strings.Contains(m.body, want) {
			t.Errorf("the email says %q:\n%s", want, m.body)
		}
	}

	// The sandbox moves nothing; a demo account has no inbox; a free call paid nothing.
	r.svc.ReceiptSigned(ctx, r.paid(emailUser, "eint_sbx", "sandbox", 1000))
	r.svc.ReceiptSigned(ctx, r.paid(demoUser, "eint_demo", "solana-devnet", 1000))
	r.svc.ReceiptSigned(ctx, r.paid(emailUser, "eint_free", "solana-devnet", 0))
	r.svc.ReceiptSigned(ctx, &IntentView{Intent: econ.Intent{ID: "eint_none", PrincipalID: emailUser.ID}})
	r.svc.Wait()
	if n := len(r.mail.all()); n != 1 {
		t.Fatalf("only a payment on a chain is emailed, got %d emails", n)
	}

	// Mainnet says so by not saying test.
	r.svc.ReceiptSigned(ctx, r.paid(emailUser, "eint_main", "solana", 2_500_000))
	r.svc.Wait()
	main := r.mail.all()[1]
	if main.subject != "Receipt: 2.5 USDC paid to demo:beta" || !strings.Contains(main.body, "Solana mainnet") || strings.Contains(main.body, "cluster=devnet") {
		t.Fatalf("a mainnet receipt: %q\n%s", main.subject, main.body)
	}
}

func TestNotifications_ReceiptEmailsStopAtTheHourlyCap(t *testing.T) {
	r := newNotifyRig(t)
	ctx := context.Background()
	for i := range ReceiptEmailsPerHour + 5 {
		r.svc.ReceiptSigned(ctx, r.paid(emailUser, fmt.Sprintf("eint_%d", i), "solana-devnet", 1000))
	}
	r.svc.Wait()
	if n := len(r.mail.all()); n != ReceiptEmailsPerHour {
		t.Fatalf("an agent paying fast doesn't flood the inbox: %d emails, cap %d", n, ReceiptEmailsPerHour)
	}
	// Another person's cap is their own, and the hour passes.
	r.svc.ReceiptSigned(ctx, r.paid(walletUser, "eint_w", "solana-devnet", 1000)) // no address: nothing
	r.advance(61 * time.Minute)
	r.svc.ReceiptSigned(ctx, r.paid(emailUser, "eint_later", "solana-devnet", 1000))
	r.svc.Wait()
	if n := len(r.mail.all()); n != ReceiptEmailsPerHour+1 {
		t.Fatalf("an hour later receipts are emailed again: %d", n)
	}
}

func TestNotifications_EachKindCanBeTurnedOff(t *testing.T) {
	r := newNotifyRig(t)
	ctx := context.Background()
	off := false
	if v, err := r.svc.SetKinds(ctx, emailUser, &off, nil); err != nil || v.SignIns || !v.Receipts {
		t.Fatalf("sign-in alerts off, receipts untouched: %+v %v", v, err)
	}
	r.svc.SignedIn(ctx, emailUser, account.ClientMeta{}, false)
	r.svc.ReceiptSigned(ctx, r.paid(emailUser, "eint_a", "solana-devnet", 1000))
	r.svc.Wait()
	if sent := r.mail.all(); len(sent) != 1 || !strings.HasPrefix(sent[0].subject, "Receipt:") {
		t.Fatalf("only the receipt: %+v", sent)
	}
	if v, err := r.svc.SetKinds(ctx, emailUser, nil, &off); err != nil || v.SignIns || v.Receipts {
		t.Fatalf("both off: %+v %v", v, err)
	}
	r.svc.ReceiptSigned(ctx, r.paid(emailUser, "eint_b", "solana-devnet", 1000))
	r.svc.Wait()
	if n := len(r.mail.all()); n != 1 {
		t.Fatalf("nothing more is sent, got %d", n)
	}
}

func TestNotifications_AMailerThatCannotAttachStillSendsTheReceipt(t *testing.T) {
	r := newNotifyRig(t)
	r.svc.mailer = textOnlyMailer{inner: r.mail}
	r.svc.ReceiptSigned(context.Background(), r.paid(emailUser, "eint_t", "solana-devnet", 1000))
	r.svc.Wait()
	sent := r.mail.all()
	if len(sent) != 1 || sent[0].filename != "" || !strings.Contains(sent[0].body, "/console/executions/eint_t/invoice") {
		t.Fatalf("the receipt goes with its links: %+v", sent)
	}
}

func TestNotifications_TextSomeoneElseWroteStaysOnOneLine(t *testing.T) {
	r := newNotifyRig(t)
	v := r.paid(emailUser, "eint_x", "solana-devnet", 1000)
	r.claims[v.Receipt].Provider.ID = "evil:provider\r\nBcc: someone@example.test"
	r.svc.ReceiptSigned(context.Background(), v)
	r.svc.Wait()
	if s := r.mail.all()[0].subject; strings.ContainsAny(s, "\r\n") {
		t.Fatalf("a provider's ID can't break the subject line: %q", s)
	}
}

func TestDeliverable(t *testing.T) {
	for email, want := range map[string]bool{
		"ada@example.test": true, "Ada@Mail.Example.com": true,
		"wallet-1@wallet.algebra.invalid": false, "demo-1@demo.algebra.invalid": false, "deleted-user_1@deleted.invalid": false,
		"x@invalid": false, "": false, "no-at-sign": false, "trailing@": false,
	} {
		if got := account.Deliverable(email); got != want {
			t.Errorf("Deliverable(%q) = %v, want %v", email, got, want)
		}
	}
}
