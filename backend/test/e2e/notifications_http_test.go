package e2e

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// Email notifications over real HTTP: a sign-in is emailed to the address
// the person signs in with, a wallet account is told it has nowhere to be
// written to until it confirms an address, and where someone's mail goes is
// theirs alone to read or change.

type mailbox struct {
	mu   sync.Mutex
	sent []mailed
}

type mailed struct{ to, subject, body, attachment string }

func (m *mailbox) Send(_ context.Context, to, subject, text string) error {
	return m.SendWithAttachment(context.Background(), to, subject, text, "", nil)
}

func (m *mailbox) SendWithAttachment(_ context.Context, to, subject, text, filename string, _ []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, mailed{to, subject, text, filename})
	return nil
}

// to is every email sent to an address so far.
func (m *mailbox) to(addr string) []mailed {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []mailed
	for _, e := range m.sent {
		if e.to == addr {
			out = append(out, e)
		}
	}
	return out
}

var confirmToken = regexp.MustCompile(`confirm_email=(\S+)`)

func TestNotificationsOverHTTP_SignInAlertsAndAConfirmedAddress(t *testing.T) {
	bundle, base := serve(t)
	box := &mailbox{}
	bundle.Notifications.SetMailer(box)
	wait := bundle.Notifications.Wait

	// Signing up is the first sign-in: a welcome, to the address signed up with.
	email, password := "ada-"+uuid.NewString()+"@example.test", "correct-horse-"+uuid.NewString()
	status, ada, out := browserCall(t, "POST", base+"/api/v1/auth/signup", nil, map[string]any{"name": "Ada", "email": email, "password": password})
	if status != 201 || ada == nil {
		t.Fatalf("sign up: %d %v", status, out)
	}
	wait()
	if got := box.to(email); len(got) != 1 || got[0].subject != "Welcome to Algebra" {
		t.Fatalf("a welcome email: %+v", got)
	}
	if u := userOf(t, out); u["notification_email"] != email || u["needs_notification_email"] != false {
		t.Fatalf("the account says where its notifications go: %v", u)
	}

	status, _, view := browserCall(t, "GET", base+"/api/v1/me/notifications", ada, nil)
	if status != 200 || view["address"] != email || view["source"] != "account" || view["needs_email"] != false || view["sign_ins"] != true || view["receipts"] != true {
		t.Fatalf("defaults: %d %v", status, view)
	}
	// This server has no mail provider configured: the console is told so.
	if view["sending"] != false {
		t.Fatalf("a server that only logs its mail says so: %v", view)
	}

	// Another address: only its confirmation link goes there.
	other := "grace-" + uuid.NewString() + "@example.test"
	status, _, view = browserCall(t, "POST", base+"/api/v1/me/notifications/email", ada, map[string]any{"email": other})
	if status != 200 || view["pending_email"] != other || view["address"] != email {
		t.Fatalf("pending, the old address still in use: %d %v", status, view)
	}
	links := box.to(other)
	if len(links) != 1 || !strings.Contains(links[0].subject, "Confirm") {
		t.Fatalf("one confirmation email: %+v", links)
	}
	token := confirmToken.FindStringSubmatch(links[0].body)
	if token == nil {
		t.Fatalf("no link in: %s", links[0].body)
	}
	if status, _, out := browserCall(t, "POST", base+"/api/v1/me/notifications/email", ada, map[string]any{"email": other}); status != 429 {
		t.Fatalf("a second confirmation email within the minute: %d %v", status, out)
	}

	// Someone else holding the link confirms nothing; nor does a wrong token.
	bob := signUp(t, base, "Bob")
	if status, _, out := browserCall(t, "POST", base+"/api/v1/me/notifications/email/confirm", bob, map[string]any{"token": token[1]}); status != 400 {
		t.Fatalf("another account with the link: %d %v", status, out)
	}
	if status, _, out := browserCall(t, "POST", base+"/api/v1/me/notifications/email/confirm", ada, map[string]any{"token": "alg_mail_nope"}); status != 400 {
		t.Fatalf("a wrong token: %d %v", status, out)
	}
	status, _, view = browserCall(t, "POST", base+"/api/v1/me/notifications/email/confirm", ada, map[string]any{"token": token[1]})
	if status != 200 || view["address"] != other || view["source"] != "added" || view["pending_email"] != nil {
		t.Fatalf("confirmed: %d %v", status, view)
	}
	if status, _, out := browserCall(t, "POST", base+"/api/v1/me/notifications/email/confirm", ada, map[string]any{"token": token[1]}); status != 400 {
		t.Fatalf("the link works once: %d %v", status, out)
	}

	// Turning sign-in alerts off, then signing in: nothing is sent.
	status, _, view = browserCall(t, "PUT", base+"/api/v1/me/notifications", ada, map[string]any{"sign_ins": false})
	if status != 200 || view["sign_ins"] != false || view["receipts"] != true {
		t.Fatalf("one kind off: %d %v", status, view)
	}
	if status, _, out := browserCall(t, "POST", base+"/api/v1/auth/login", nil, map[string]any{"email": email, "password": password}); status != 200 {
		t.Fatalf("sign in: %d %v", status, out)
	}
	wait()
	if n := len(box.to(other)) + len(box.to(email)); n != 2 {
		t.Fatalf("no sign-in alert while they are off: %d emails in all", n)
	}

	// Removing the added address goes back to the one they sign in with.
	status, _, view = browserCall(t, "DELETE", base+"/api/v1/me/notifications/email", ada, nil)
	if status != 200 || view["address"] != email || view["source"] != "account" {
		t.Fatalf("removed: %d %v", status, view)
	}

	// Where mail goes is the person's own: no session, no answer; an API key is not a session.
	if status, _, _ := browserCall(t, "GET", base+"/api/v1/me/notifications", nil, nil); status != 401 {
		t.Fatalf("without a session: %d", status)
	}
	_, project := workspace(t, base, ada)
	projectPass(t, base, ada, project, "sandbox")
	key, _ := issueKey(t, base, ada, project, map[string]any{"name": "ci", "environment": "sandbox", "scopes": []string{"read", "simulate", "execute"}})
	if status, _, _ := call(t, "GET", base+"/api/v1/me/notifications", key, nil); status != 401 {
		t.Fatalf("an API key reads nobody's notification settings: %d", status)
	}
	if status, _, _ := call(t, "POST", base+"/api/v1/me/notifications/email", key, map[string]any{"email": "x@example.test"}); status != 401 {
		t.Fatalf("an API key moves nobody's mail: %d", status)
	}

	// A sandbox payment moves nothing on a chain, so no receipt is emailed for it.
	status, _, run := call(t, "POST", base+"/api/v1/execute", key, map[string]any{
		"capability": "token.price", "input": map[string]any{"mint": "So11111111111111111111111111111111111111112"}, "budget_max_minor": 10_000,
		"window": "notify-" + uuid.NewString(),
	})
	if status != 200 || run["delivered"] != true || run["receipt"] == nil {
		t.Fatalf("a sandbox call: %d %v", status, run)
	}
	wait()
	for _, e := range append(box.to(email), box.to(other)...) {
		if strings.HasPrefix(e.subject, "Receipt") {
			t.Fatalf("a sandbox call sent a receipt email: %+v", e)
		}
	}
}

func TestNotificationsOverHTTP_AWalletAccountIsAskedForAnEmail(t *testing.T) {
	issuer, pemKey := newPrivyIssuer(t)
	bundle, base := serveWith(t, map[string]string{"PRIVY_APP_ID": e2ePrivyApp, "PRIVY_VERIFICATION_KEY": pemKey})
	box := &mailbox{}
	bundle.Notifications.SetMailer(box)
	signIn := func(tok string) (int, *http.Cookie, map[string]any) {
		return browserCall(t, "POST", base+"/api/v1/auth/privy", nil, map[string]any{"identity_token": tok})
	}

	// A wallet and no email: nowhere to write to, and the account says so.
	did, wallet := "did:privy:"+uuid.NewString(), randomSolanaAddress(t)
	status, cookie, out := signIn(issuer.token(e2ePrivyApp, did, embeddedWallet(wallet)))
	if status != 201 || cookie == nil {
		t.Fatalf("wallet sign-in: %d %v", status, out)
	}
	if u := userOf(t, out); u["needs_notification_email"] != true || u["notification_email"] != "" {
		t.Fatalf("a wallet account needs an email for notifications: %v", u)
	}
	bundle.Notifications.Wait()
	box.mu.Lock()
	n := len(box.sent)
	box.mu.Unlock()
	if n != 0 {
		t.Fatalf("nothing is sent to a placeholder address, got %d emails", n)
	}

	// They add one and confirm it: the banner's reason is gone.
	added := "wallet-owner-" + uuid.NewString() + "@example.test"
	if status, _, out := browserCall(t, "POST", base+"/api/v1/me/notifications/email", cookie, map[string]any{"email": added}); status != 200 {
		t.Fatalf("adding an address: %d %v", status, out)
	}
	token := confirmToken.FindStringSubmatch(box.to(added)[0].body)
	if status, _, out := browserCall(t, "POST", base+"/api/v1/me/notifications/email/confirm", cookie, map[string]any{"token": token[1]}); status != 200 {
		t.Fatalf("confirming: %d %v", status, out)
	}
	status, _, out = browserCall(t, "GET", base+"/api/v1/auth/session", cookie, nil)
	if u := userOf(t, out); status != 200 || u["needs_notification_email"] != false || u["notification_email"] != added || u["email"] != "" {
		t.Fatalf("notifications go to the added address; the account's own email is still none: %v", u)
	}

	// Another wallet account, whose person verified an email with Privy later:
	// that address is used without a second confirmation, and the sign-in is emailed there.
	did2, linked := "did:privy:"+uuid.NewString(), "linked-"+uuid.NewString()+"@example.com"
	wallet2 := randomSolanaAddress(t)
	if status, _, out := signIn(issuer.token(e2ePrivyApp, did2, embeddedWallet(wallet2))); status != 201 {
		t.Fatalf("second wallet sign-in: %d %v", status, out)
	}
	status, _, out = signIn(issuer.token(e2ePrivyApp, did2, embeddedWallet(wallet2), map[string]any{"type": "email", "address": linked}))
	if u := userOf(t, out); status != 200 || u["needs_notification_email"] != false || u["notification_email"] != linked {
		t.Fatalf("a Privy-verified email is adopted: %d %v", status, out)
	}
	bundle.Notifications.Wait()
	if got := box.to(linked); len(got) != 1 || got[0].subject != "New sign-in to Algebra" {
		t.Fatalf("the sign-in alert goes to the adopted address: %+v", got)
	}
}
