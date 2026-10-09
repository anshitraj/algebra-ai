package bankoffers

import (
	"context"
	"testing"

	"github.com/project-algebra/algebra/internal/domain/merchant"
)

// The documented example must stay loadable, and must never show: its
// offers ended in the past.
func TestDocsExampleLoads(t *testing.T) {
	offers, err := NewFile("testdata/bank-offers.example.json", merchant.NewAllowedDomains("amazon.in", "flipkart.com")).Offers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(offers) != 2 {
		t.Fatalf("expected both example offers to validate, got %d", len(offers))
	}
	for _, o := range offers {
		if o.EndsAt.Year() > 2025 {
			t.Fatalf("example offer %q must end in the past so it can't be mistaken for a live one", o.Title)
		}
	}
}
