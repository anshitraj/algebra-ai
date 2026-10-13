package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type memLeads struct {
	mu    sync.Mutex
	leads []Lead
}

func (m *memLeads) CreateLead(_ context.Context, l *Lead) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.leads = append(m.leads, *l)
	return nil
}

func (m *memLeads) Leads(context.Context, int) ([]Lead, error) { return m.leads, nil }

type sentMail struct{ to, subject, text string }

type chanMailer struct {
	sent chan sentMail
	err  error
}

func (c chanMailer) Send(_ context.Context, to, subject, text string) error {
	c.sent <- sentMail{to, subject, text}
	return c.err
}

func TestLeads_TheOperatorIsEmailedAfterTheEnquiryIsStored(t *testing.T) {
	store := &memLeads{}
	svc := NewLeadService(store)
	mail := chanMailer{sent: make(chan sentMail, 1), err: errors.New("the mail provider is down")}
	svc.SetNotify(mail, "sales@algebra.test", slog.New(slog.NewTextHandler(io.Discard, nil)))

	l, err := svc.Submit(context.Background(), Lead{BusinessName: "Acme Agents", WorkEmail: "dana@acme.example", UseCase: "token data for our agents", EstimatedUsage: "1k-10k"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-mail.sent:
		if m.to != "sales@algebra.test" || !strings.Contains(m.subject, "Acme Agents") || !strings.Contains(m.text, l.ID) || !strings.Contains(m.text, "dana@acme.example") {
			t.Fatalf("the email names the enquiry: %+v", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the operator was not emailed")
	}
	// A failed email doesn't lose the enquiry: it was stored first.
	if len(store.leads) != 1 {
		t.Fatalf("stored: %d", len(store.leads))
	}
}

func TestLeads_NoNotificationWithoutAnAddress(t *testing.T) {
	svc := NewLeadService(&memLeads{})
	mail := chanMailer{sent: make(chan sentMail, 1)}
	svc.SetNotify(mail, "", nil)
	if _, err := svc.Submit(context.Background(), Lead{BusinessName: "Acme", WorkEmail: "dana@acme.example", UseCase: "x", EstimatedUsage: "unsure"}); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-mail.sent:
		t.Fatalf("nothing is sent without LEADS_NOTIFY_EMAIL: %+v", m)
	case <-time.After(200 * time.Millisecond):
	}
}
