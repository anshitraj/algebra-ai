package e2e

import (
	"net/http"
	"regexp"
	"testing"

	"github.com/google/uuid"
)

// Sign-in with an emailed code over real HTTP: the code arrives by email, the
// right one signs in (and makes the account the first time), a wrong one is
// refused, a code works once, and asking again at once is refused.

var codeInEmail = regexp.MustCompile(`\b(\d{6})\b`)

func codeSentTo(t *testing.T, box *mailbox, addr string) string {
	t.Helper()
	got := box.to(addr)
	if len(got) == 0 {
		t.Fatalf("no email was sent to %s", addr)
	}
	m := codeInEmail.FindStringSubmatch(got[len(got)-1].body)
	if m == nil {
		t.Fatalf("no code in the email: %q", got[len(got)-1].body)
	}
	return m[1]
}

func TestLoginCodeSignInOverHTTP(t *testing.T) {
	bundle, base := serve(t)
	box := &mailbox{}
	bundle.Accounts.SetMailer(box)

	status, _, providers := browserCall(t, "GET", base+"/api/v1/auth/providers", nil, nil)
	if status != 200 || providers["email_code"] != true {
		t.Fatalf("providers: %d %v", status, providers)
	}

	send := func(email string) (int, map[string]any) {
		status, _, out := browserCall(t, "POST", base+"/api/v1/auth/code/send", nil, map[string]any{"email": email})
		return status, out
	}
	verify := func(email, code string) (int, *http.Cookie, map[string]any) {
		return browserCall(t, "POST", base+"/api/v1/auth/code/verify", nil, map[string]any{"email": email, "code": code})
	}

	t.Run("a code makes the account and signs in", func(t *testing.T) {
		email := "code-" + uuid.NewString() + "@example.test"
		if status, out := send(email); status != 200 || out["ok"] != true {
			t.Fatalf("send: %d %v", status, out)
		}
		code := codeSentTo(t, box, email)

		// A wrong code is refused and leaves no session behind.
		wrong := "000000"
		if wrong == code {
			wrong = "111111"
		}
		if status, cookie, out := verify(email, wrong); status != 401 || cookie != nil {
			t.Fatalf("a wrong code: %d %v", status, out)
		}
		status, cookie, out := verify(email, code)
		if status != 201 || cookie == nil {
			t.Fatalf("the right code: %d %v", status, out)
		}
		u := userOf(t, out)
		if u["email"] != email || u["email_verified"] != true {
			t.Fatalf("the new account: %v", u)
		}
		// The cookie is a session like any other.
		if status, _, out := browserCall(t, "GET", base+"/api/v1/auth/session", cookie, nil); status != 200 || userOf(t, out)["id"] != u["id"] {
			t.Fatalf("session: %d %v", status, out)
		}
		// A code works once.
		if status, cookie, _ := verify(email, code); status != 401 || cookie != nil {
			t.Fatalf("the code a second time: %d", status)
		}
	})

	t.Run("asking again at once is refused", func(t *testing.T) {
		email := "soon-" + uuid.NewString() + "@example.test"
		if status, out := send(email); status != 200 {
			t.Fatalf("send: %d %v", status, out)
		}
		status, _, out := browserCall(t, "POST", base+"/api/v1/auth/code/send", nil, map[string]any{"email": email})
		if status != 429 {
			t.Fatalf("a second code at once: %d %v", status, out)
		}
		if n := len(box.to(email)); n != 1 {
			t.Fatalf("%d emails sent, want 1", n)
		}
	})

	t.Run("an existing account is the same account", func(t *testing.T) {
		email := "known-" + uuid.NewString() + "@example.test"
		status, _, out := browserCall(t, "POST", base+"/api/v1/auth/signup", nil, map[string]any{"name": "Kit", "email": email, "password": "correct horse battery staple"})
		if status != 201 {
			t.Fatalf("signup: %d %v", status, out)
		}
		existing := userOf(t, out)["id"]
		if status, out := send(email); status != 200 {
			t.Fatalf("send: %d %v", status, out)
		}
		status, _, out = verify(email, codeSentTo(t, box, email))
		if status != 200 || userOf(t, out)["id"] != existing {
			t.Fatalf("signed in as %v (%d), want the account %v", userOf(t, out)["id"], status, existing)
		}
	})

	t.Run("bad input", func(t *testing.T) {
		if status, _ := send("not an address"); status != 400 {
			t.Errorf("a bad address: %d, want 400", status)
		}
		if status, _, _ := verify("nobody-"+uuid.NewString()+"@example.test", "123456"); status != 401 {
			t.Errorf("a code nobody was sent: %d, want 401", status)
		}
		if status, _, _ := verify("nobody@example.test", "abc"); status != 401 {
			t.Errorf("a code that isn't six digits: %d, want 401", status)
		}
	})
}

func TestLoginCodeSignInOverHTTP_OffWhenTurnedOff(t *testing.T) {
	_, base := serveWith(t, map[string]string{"EMAIL_CODE_LOGIN": "off"})
	status, _, providers := browserCall(t, "GET", base+"/api/v1/auth/providers", nil, nil)
	if status != 200 || providers["email_code"] != false {
		t.Fatalf("providers: %d %v", status, providers)
	}
	if status, _, _ := browserCall(t, "POST", base+"/api/v1/auth/code/send", nil, map[string]any{"email": "a@example.test"}); status != 404 {
		t.Errorf("send with codes off: %d, want 404", status)
	}
	if status, _, _ := browserCall(t, "POST", base+"/api/v1/auth/code/verify", nil, map[string]any{"email": "a@example.test", "code": "123456"}); status != 404 {
		t.Errorf("verify with codes off: %d, want 404", status)
	}
}
