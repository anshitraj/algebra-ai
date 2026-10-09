// Package bankoffers loads the operator-curated bank/card offers that
// DiscoveryService.FindDeals shows next to each merchant's own deals.
//
// Amazon and Flipkart publish these ("10% instant discount with HDFC Bank
// credit cards, up to ₹1,250 on orders over ₹5,000") only on their offer
// pages, not through any API, and Algebra never scrapes them. So a person
// copies the terms into a JSON file (see testdata/bank-offers.example.json beside this file):
//
//	{"offers": [{"merchant": "flipkart", "bank": "HDFC Bank", "title": "...",
//	  "discount_percent": 10, "max_discount_minor_units": 125000,
//	  "min_order_minor_units": 500000, "starts_at": "...", "ends_at": "...",
//	  "terms_url": "https://www.flipkart.com/...", "verified_at": "2026-09-24"}]}
//
// Every offer needs an end date and a terms link on the merchant's own
// domain, so nothing stale or unsourced reaches a user. The file is re-read
// when it changes on disk; a broken edit keeps the last good set.
package bankoffers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/project-algebra/algebra/connectors/sanitize"
	"github.com/project-algebra/algebra/internal/domain/deal"
	"github.com/project-algebra/algebra/internal/domain/merchant"
)

const maxFileBytes = 1 << 20

type fileFormat struct {
	Offers []deal.BankOffer `json:"offers"`
}

// File is an app.BankOfferSource backed by a JSON file.
type File struct {
	path    string
	allowed *merchant.AllowedDomains

	mu      sync.Mutex
	modTime time.Time
	size    int64
	offers  []deal.BankOffer
	loaded  bool
}

// NewFile reads offers from path. allowed bounds where terms_url may point
// (the merchant URL allowlist); nil accepts any public https link.
func NewFile(path string, allowed *merchant.AllowedDomains) *File {
	return &File{path: path, allowed: allowed}
}

// Offers returns every valid offer in the file, re-reading it if it changed
// since the last call.
func (f *File) Offers(context.Context) ([]deal.BankOffer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	info, err := os.Stat(f.path)
	if err != nil {
		if f.loaded {
			return f.offers, nil
		}
		return nil, fmt.Errorf("bankoffers: %w", err)
	}
	if f.loaded && info.ModTime().Equal(f.modTime) && info.Size() == f.size {
		return f.offers, nil
	}
	offers, err := f.load()
	if err != nil {
		if f.loaded {
			slog.Warn("bankoffers: keeping the last good offers file", "path", f.path, "error", err)
			return f.offers, nil
		}
		return nil, err
	}
	f.offers, f.modTime, f.size, f.loaded = offers, info.ModTime(), info.Size(), true
	return f.offers, nil
}

func (f *File) load() ([]deal.BankOffer, error) {
	raw, err := os.ReadFile(f.path)
	if err != nil {
		return nil, fmt.Errorf("bankoffers: %w", err)
	}
	if len(raw) > maxFileBytes {
		return nil, fmt.Errorf("bankoffers: %s is larger than %d bytes", f.path, maxFileBytes)
	}
	var file fileFormat
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return nil, fmt.Errorf("bankoffers: %s: %w", f.path, err)
	}
	out := make([]deal.BankOffer, 0, len(file.Offers))
	for i, o := range file.Offers {
		clean, err := f.validate(o)
		if err != nil {
			slog.Warn("bankoffers: skipping offer", "path", f.path, "index", i, "error", err)
			continue
		}
		out = append(out, clean)
	}
	return out, nil
}

// validate checks one offer and returns it normalized.
func (f *File) validate(o deal.BankOffer) (deal.BankOffer, error) {
	o.Merchant = strings.ToLower(strings.TrimSpace(o.Merchant))
	o.Bank = sanitize.Text(o.Bank, 60)
	o.Title = sanitize.Text(o.Title, 160)
	cardTypes := o.CardTypes[:0:0]
	for _, t := range o.CardTypes {
		if t = strings.ToLower(sanitize.Text(t, 30)); t != "" {
			cardTypes = append(cardTypes, t)
		}
	}
	o.CardTypes = cardTypes

	switch {
	case o.Merchant == "":
		return o, errors.New("merchant is required")
	case o.Bank == "":
		return o, errors.New("bank is required")
	case o.Title == "":
		return o, errors.New("title is required")
	case (o.DiscountPercent > 0) == (o.FlatDiscountMinorUnits > 0):
		return o, errors.New("set exactly one of discount_percent and flat_discount_minor_units")
	case o.DiscountPercent < 0 || o.DiscountPercent > 100:
		return o, errors.New("discount_percent must be 1-100")
	case o.MaxDiscountMinorUnits < 0 || o.MinOrderMinorUnits < 0 || o.FlatDiscountMinorUnits < 0:
		return o, errors.New("amounts can't be negative")
	case o.EndsAt.IsZero():
		return o, errors.New("ends_at is required — an offer with no end date can't be shown")
	case !o.StartsAt.IsZero() && !o.StartsAt.Before(o.EndsAt):
		return o, errors.New("starts_at must be before ends_at")
	}
	if err := f.checkTermsURL(o.TermsURL); err != nil {
		return o, err
	}
	return o, nil
}

func (f *File) checkTermsURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("terms_url is required — link the merchant's own offer terms")
	}
	if f.allowed != nil {
		if err := f.allowed.ValidateURL(raw); err != nil {
			return fmt.Errorf("terms_url: %w", err)
		}
		return nil
	}
	if _, err := merchant.ValidatePublicHTTPSURL(raw); err != nil {
		return fmt.Errorf("terms_url: %w", err)
	}
	return nil
}
