package blinkit

import (
	"context"
	"errors"
	"testing"

	"github.com/project-algebra/algebra/internal/domain/merchant"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

func TestHandoffURL(t *testing.T) {
	c := New()
	cases := map[string]string{
		"coke zero & chips": "https://blinkit.com/s/?q=coke+zero+%26+chips",
		"  milk ":           "https://blinkit.com/s/?q=milk",
		"":                  "https://blinkit.com/",
	}
	allow := merchant.NewAllowedDomains("blinkit.com")
	for query, want := range cases {
		got := c.HandoffURL(query)
		if got != want {
			t.Fatalf("HandoffURL(%q) = %q, want %q", query, got, want)
		}
		if err := allow.ValidateURL(got); err != nil {
			t.Fatalf("handoff URL must pass the merchant URL allowlist: %v", err)
		}
	}
}

// Blinkit must never look automatable: no capability, no search, and
// checkout always hands back to the user.
func TestNothingIsAutomated(t *testing.T) {
	c := New()
	if c.Capabilities() != (merchant.Capabilities{}) {
		t.Fatalf("expected all-false capabilities, got %+v", c.Capabilities())
	}
	if _, err := c.SearchProducts(context.Background(), "milk", 5); !errors.Is(err, shared.ErrNotImplemented) {
		t.Fatalf("SearchProducts should be ErrNotImplemented, got %v", err)
	}
	res, err := c.ExecuteCheckout(context.Background(), "cart", "approval", merchant.Fulfillment{})
	if err != nil || res.Status != merchant.ExecutionUserInterventionNeeded {
		t.Fatalf("ExecuteCheckout should require user intervention, got %+v, %v", res, err)
	}
	if st := c.Status(); st.Integration != merchant.IntegrationDeepLinkHandoff {
		t.Fatalf("unexpected status %+v", st)
	}
}
