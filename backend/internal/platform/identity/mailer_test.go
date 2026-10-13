package identity

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// What Resend is sent: the sender, one recipient, the text, the key as a
// bearer token, and a file as base64 when there is one.

func TestResendMailerSendsTextAndAnAttachment(t *testing.T) {
	// The stand-in for Resend runs on its own goroutine: what it saw is read under a lock.
	var mu sync.Mutex
	var got []map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			t.Errorf("not JSON: %s", body)
		}
		mu.Lock()
		auth = r.Header.Get("Authorization")
		got = append(got, m)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"email_1"}`))
	}))
	defer srv.Close()
	m := NewResendMailer("re_test_key", "Algebra <no-reply@example.test>")
	m.endpoint = srv.URL

	if err := m.Send(context.Background(), "ada@example.test", "New sign-in to Algebra", "You signed in."); err != nil {
		t.Fatal(err)
	}
	pdf := []byte("%PDF-1.4 a receipt")
	if err := m.SendWithAttachment(context.Background(), "ada@example.test", "Receipt", "Attached.", "algebra-receipt-eint_1.pdf", pdf); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if auth != "Bearer re_test_key" || len(got) != 2 {
		t.Fatalf("auth %q, %d requests", auth, len(got))
	}
	plain, with := got[0], got[1]
	if plain["from"] != "Algebra <no-reply@example.test>" || plain["subject"] != "New sign-in to Algebra" || plain["text"] != "You signed in." {
		t.Fatalf("plain email: %v", plain)
	}
	if to, _ := plain["to"].([]any); len(to) != 1 || to[0] != "ada@example.test" {
		t.Fatalf("recipient: %v", plain["to"])
	}
	if _, has := plain["attachments"]; has {
		t.Fatal("a plain email carries no attachment")
	}
	files, _ := with["attachments"].([]any)
	if len(files) != 1 {
		t.Fatalf("attachments: %v", with["attachments"])
	}
	f, _ := files[0].(map[string]any)
	if f["filename"] != "algebra-receipt-eint_1.pdf" || f["content"] != base64.StdEncoding.EncodeToString(pdf) {
		t.Fatalf("attachment: %v", f)
	}
}

func TestResendMailerReportsARefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"The example.test domain is not verified"}`))
	}))
	defer srv.Close()
	m := NewResendMailer("re_test_key", "Algebra <no-reply@example.test>")
	m.endpoint = srv.URL
	err := m.Send(context.Background(), "ada@example.test", "s", "t")
	if err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "not verified") {
		t.Fatalf("the refusal and its reason come back: %v", err)
	}
}

func TestResendMailerSendsHTMLBesideTheText(t *testing.T) {
	var mu sync.Mutex
	var got []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		mu.Lock()
		got = append(got, m)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"id":"email_1"}`))
	}))
	defer srv.Close()
	m := NewResendMailer("re_test_key", "Algebra <no-reply@auth.example.test>")
	m.endpoint = srv.URL

	if err := m.SendHTML(context.Background(), "ada@example.test", "New sign-in to Algebra", "You signed in.", "<!doctype html><p>You signed in.</p>", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := m.SendHTML(context.Background(), "ada@example.test", "Receipt", "Attached.", "<!doctype html><p>Attached.</p>", "r.pdf", []byte("%PDF")); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("%d requests", len(got))
	}
	// A client shows the HTML when it can and the text when it can't: both are always sent.
	for i, e := range got {
		if e["text"] == "" || e["text"] == nil || !strings.HasPrefix(e["html"].(string), "<!doctype html>") {
			t.Fatalf("email %d carries both bodies: %v", i, e)
		}
	}
	if _, has := got[0]["attachments"]; has {
		t.Fatal("no file, no attachment")
	}
	files, _ := got[1]["attachments"].([]any)
	if len(files) != 1 || files[0].(map[string]any)["filename"] != "r.pdf" {
		t.Fatalf("the file is attached: %v", got[1]["attachments"])
	}
}
