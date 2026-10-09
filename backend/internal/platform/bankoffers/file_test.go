package bankoffers

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/merchant"
)

const goodFile = `{"offers":[
 {"merchant":"Flipkart","bank":"HDFC Bank","card_types":["Credit","debit_emi"],"title":"10% off with HDFC Bank credit cards",
  "discount_percent":10,"max_discount_minor_units":125000,"min_order_minor_units":500000,
  "starts_at":"2026-09-23T00:00:00+05:30","ends_at":"2026-10-02T23:59:59+05:30",
  "terms_url":"https://www.flipkart.com/pages/offers","verified_at":"2026-09-24"},
 {"merchant":"amazon","bank":"SBI","title":"Flat ₹1,500 off with SBI credit cards","flat_discount_minor_units":150000,
  "min_order_minor_units":2500000,"ends_at":"2026-10-05T23:59:59+05:30","terms_url":"https://www.amazon.in/b?node=1"},
 {"merchant":"amazon","bank":"ICICI","title":"No end date","discount_percent":10,"terms_url":"https://www.amazon.in/x"},
 {"merchant":"amazon","bank":"Axis","title":"Off-domain terms","discount_percent":10,"ends_at":"2026-10-05T00:00:00Z","terms_url":"https://coupons.example.com/axis"},
 {"merchant":"amazon","bank":"Kotak","title":"Both kinds","discount_percent":10,"flat_discount_minor_units":100,"ends_at":"2026-10-05T00:00:00Z","terms_url":"https://www.amazon.in/y"},
 {"merchant":"amazon","bank":"","title":"No bank","discount_percent":10,"ends_at":"2026-10-05T00:00:00Z","terms_url":"https://www.amazon.in/z"}
]}`

func write(t *testing.T, path, body string, mod time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func TestOffers_ValidatesEachEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "offers.json")
	write(t, path, goodFile, time.Now())
	f := NewFile(path, merchant.NewAllowedDomains("amazon.in", "flipkart.com"))

	offers, err := f.Offers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(offers) != 2 {
		t.Fatalf("got %d offers, want 2 valid ones: %+v", len(offers), offers)
	}
	hdfc := offers[0]
	if hdfc.Merchant != "flipkart" || hdfc.CardTypes[0] != "credit" || hdfc.EndsAt.IsZero() || hdfc.VerifiedAt != "2026-09-24" {
		t.Fatalf("offer not normalized: %+v", hdfc)
	}
	if offers[1].Bank != "SBI" || offers[1].FlatDiscountMinorUnits != 150000 {
		t.Fatalf("unexpected second offer %+v", offers[1])
	}
}

func TestOffers_ReloadsOnChangeAndKeepsLastGoodSet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "offers.json")
	t0 := time.Now().Add(-time.Hour)
	write(t, path, goodFile, t0)
	f := NewFile(path, nil)
	if offers, err := f.Offers(context.Background()); err != nil || len(offers) != 3 {
		// Without an allowlist the off-domain (but public https) terms link is accepted.
		t.Fatalf("got %d offers, %v", len(offers), err)
	}

	write(t, path, `{"offers":[{"merchant":"amazon","bank":"HDFC","title":"Only one","discount_percent":5,"ends_at":"2026-10-05T00:00:00Z","terms_url":"https://www.amazon.in/o"}]}`, t0.Add(time.Minute))
	if offers, err := f.Offers(context.Background()); err != nil || len(offers) != 1 || offers[0].Title != "Only one" {
		t.Fatalf("edit not picked up: %+v, %v", offers, err)
	}

	write(t, path, `{"offers": [ broken`, t0.Add(2*time.Minute))
	if offers, err := f.Offers(context.Background()); err != nil || len(offers) != 1 {
		t.Fatalf("a broken edit must keep the last good set, got %+v, %v", offers, err)
	}
}

func TestOffers_MissingFileAndUnknownFields(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewFile(filepath.Join(dir, "nope.json"), nil).Offers(context.Background()); err == nil {
		t.Fatal("expected an error for a missing file")
	}
	path := filepath.Join(dir, "typo.json")
	write(t, path, `{"offers":[],"offerz":[]}`, time.Now())
	if _, err := NewFile(path, nil).Offers(context.Background()); err == nil {
		t.Fatal("expected an error for an unknown top-level field (likely a typo)")
	}
}
