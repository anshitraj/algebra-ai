package app

import (
	"context"
	"fmt"
	"net/mail"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
)

// Lead is one Book a Demo enquiry. It comes from an unauthenticated form, so
// every field is untrusted text: bounded, stored, and shown only as text.
type Lead struct {
	ID             string    `json:"id"`
	BusinessName   string    `json:"business_name"`
	WorkEmail      string    `json:"work_email"`
	Website        string    `json:"website,omitempty"`
	UseCase        string    `json:"use_case"`
	EstimatedUsage string    `json:"estimated_usage,omitempty"`
	Message        string    `json:"message,omitempty"`
	UserID         string    `json:"user_id,omitempty"`
	SourceIP       string    `json:"-"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
}

// LeadUsageBands are the answers the form offers for expected volume.
var LeadUsageBands = []string{"", "under-1k", "1k-10k", "10k-100k", "over-100k", "unsure"}

// LeadStore persists enquiries (migrations/0023_demo_requests.sql).
type LeadStore interface {
	CreateLead(ctx context.Context, l *Lead) error
	Leads(ctx context.Context, limit int) ([]Lead, error)
}

// LeadService takes Book a Demo enquiries and keeps them for the operator.
type LeadService struct {
	store LeadStore
	now   func() time.Time
}

func NewLeadService(store LeadStore) *LeadService {
	return &LeadService{store: store, now: time.Now}
}

// cleanLeadText trims s, replaces control characters (keeping new lines when
// multiline) and reports whether it fits in max runes.
func cleanLeadText(s string, max int, multiline bool) (string, bool) {
	s = strings.Map(func(r rune) rune {
		if r == '\n' && multiline {
			return r
		}
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.TrimSpace(s))
	return s, len([]rune(s)) <= max
}

// Submit validates an enquiry and stores it. Nothing is sent anywhere else:
// the row is the record, read by the operator.
func (s *LeadService) Submit(ctx context.Context, in Lead) (*Lead, error) {
	bad := func(msg string) (*Lead, error) { return nil, fmt.Errorf("%w: %s", ErrInvalidRequest, msg) }
	var ok bool
	if in.BusinessName, ok = cleanLeadText(in.BusinessName, 120, false); !ok || in.BusinessName == "" {
		return bad("business name is required (up to 120 characters)")
	}
	in.WorkEmail = strings.TrimSpace(in.WorkEmail)
	addr, err := mail.ParseAddress(in.WorkEmail)
	if err != nil || addr.Address != in.WorkEmail || len(in.WorkEmail) > 254 ||
		!strings.Contains(in.WorkEmail[strings.LastIndex(in.WorkEmail, "@")+1:], ".") {
		return bad("a work email address is required")
	}
	if in.Website, ok = cleanLeadText(in.Website, 200, false); !ok {
		return bad("website is too long (up to 200 characters)")
	}
	if in.Website != "" {
		site := in.Website
		if !strings.Contains(site, "://") {
			site = "https://" + site
		}
		u, err := url.Parse(site)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || !strings.Contains(u.Hostname(), ".") {
			return bad("website must be a web address such as example.com")
		}
		in.Website = site
	}
	if in.UseCase, ok = cleanLeadText(in.UseCase, 2000, true); !ok || in.UseCase == "" {
		return bad("tell us what you want to build (up to 2,000 characters)")
	}
	if !slices.Contains(LeadUsageBands, in.EstimatedUsage) {
		return bad("estimated usage must be one of: under-1k, 1k-10k, 10k-100k, over-100k, unsure")
	}
	if in.Message, ok = cleanLeadText(in.Message, 4000, true); !ok {
		return bad("message is too long (up to 4,000 characters)")
	}
	in.ID, in.Status, in.CreatedAt = newID("lead"), "new", s.now().UTC()
	if err := s.store.CreateLead(ctx, &in); err != nil {
		return nil, err
	}
	return &in, nil
}

// List returns the newest enquiries, for the operator.
func (s *LeadService) List(ctx context.Context, limit int) ([]Lead, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	return s.store.Leads(ctx, limit)
}
