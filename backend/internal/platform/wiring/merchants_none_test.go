package wiring

import (
	"testing"

	"github.com/project-algebra/algebra/internal/platform/config"
)

// An API-only deployment turns the shopping connectors off with
// ENABLED_MERCHANTS=none, and so has no mock test store to remove first.
func TestBuildConnectorsRegistersNothingForNone(t *testing.T) {
	reg, err := buildConnectors(config.MerchantsConfig{Enabled: []string{config.NoMerchants}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(reg.List()); n != 0 {
		t.Errorf("no connector at all, got %d", n)
	}
	if (config.MerchantsConfig{Enabled: []string{config.NoMerchants}}).IsEnabled("mock") {
		t.Error("none enables no mock store, which is what production refuses to start with")
	}
	// Unset still means the defaults, and a made-up name is still an error.
	if reg, err := buildConnectors(config.MerchantsConfig{}, nil); err != nil || len(reg.List()) == 0 {
		t.Errorf("the defaults: %v", err)
	}
	if _, err := buildConnectors(config.MerchantsConfig{Enabled: []string{"nope"}}, nil); err == nil {
		t.Error("an unknown merchant is still refused")
	}
}
