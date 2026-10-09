package config

import (
	"strings"
	"testing"
)

func TestDirectoriesAreOnUnlessTurnedOff(t *testing.T) {
	t.Setenv("CDP_BAZAAR_ENABLED", "")
	var c CircleConfig
	if err := loadDirectory(&c, "CDP_BAZAAR_ENABLED", "CDP_DISCOVERY_URL"); err != nil || !c.Enabled || c.DiscoveryURL != "" {
		t.Fatalf("on by default, with the directory's own URL: %v %+v", err, c)
	}
	for _, off := range []string{"off", " OFF "} {
		t.Setenv("CDP_BAZAAR_ENABLED", off)
		if err := loadDirectory(&c, "CDP_BAZAAR_ENABLED", "CDP_DISCOVERY_URL"); err != nil || c.Enabled {
			t.Errorf("%q turns it off: %v %+v", off, err, c)
		}
	}
}

func TestDirectoryURLsMustBeHTTPSWithoutCredentials(t *testing.T) {
	for _, bad := range []string{"http://api.example.com/x", "https://user:pw@api.example.com/x", "ftp://api.example.com", "https:///nohost", "not a url"} {
		t.Setenv("CDP_DISCOVERY_URL", bad)
		var c CircleConfig
		if err := loadDirectory(&c, "CDP_BAZAAR_ENABLED", "CDP_DISCOVERY_URL"); err == nil || !strings.Contains(err.Error(), "CDP_DISCOVERY_URL") {
			t.Errorf("%q must be refused, naming the variable: %v", bad, err)
		}
	}
	t.Setenv("CDP_DISCOVERY_URL", "https://api.example.com/discovery/resources")
	var c CircleConfig
	if err := loadDirectory(&c, "CDP_BAZAAR_ENABLED", "CDP_DISCOVERY_URL"); err != nil || c.DiscoveryURL != "https://api.example.com/discovery/resources" {
		t.Errorf("a plain https URL is accepted: %v %+v", err, c)
	}
}

func TestJupiterIsOffUnlessTurnedOnAndItsLimitsAreChecked(t *testing.T) {
	for _, name := range []string{"JUPITER_SWAP_ENABLED", "JUPITER_API_KEY", "JUPITER_BASE_URL", "JUPITER_MAX_SWAP_USDC", "JUPITER_MAX_SLIPPAGE_BPS"} {
		t.Setenv(name, "")
	}
	var c JupiterConfig
	if err := loadJupiter(&c); err != nil || c.Enabled || c.MaxSwapMinor != 0 || c.MaxSlippageBps != 0 || c.BaseURL != "" {
		t.Fatalf("off, and with defaults to fill in: %v %+v", err, c)
	}
	t.Setenv("JUPITER_SWAP_ENABLED", "yes")
	if err := loadJupiter(&c); err != nil || c.Enabled {
		t.Errorf("only \"on\" turns it on: %v %+v", err, c)
	}
	t.Setenv("JUPITER_SWAP_ENABLED", " ON ")
	t.Setenv("JUPITER_API_KEY", " secret ")
	t.Setenv("JUPITER_MAX_SWAP_USDC", "2.50")
	t.Setenv("JUPITER_MAX_SLIPPAGE_BPS", "75")
	t.Setenv("JUPITER_BASE_URL", "https://swap.example.com/v2/")
	if err := loadJupiter(&c); err != nil || !c.Enabled || c.APIKey != "secret" || c.MaxSwapMinor != 2_500_000 || c.MaxSlippageBps != 75 || c.BaseURL != "https://swap.example.com/v2" {
		t.Errorf("%v %+v", err, c)
	}
	for name, bad := range map[string]string{
		"JUPITER_MAX_SWAP_USDC": "0", "JUPITER_MAX_SLIPPAGE_BPS": "301", "JUPITER_BASE_URL": "http://swap.example.com",
	} {
		t.Setenv(name, bad)
		if err := loadJupiter(&c); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s=%q must be refused, naming the variable: %v", name, bad, err)
		}
		t.Setenv(name, "")
	}
	t.Setenv("JUPITER_MAX_SWAP_USDC", "1.0000001")
	if err := loadJupiter(&c); err == nil {
		t.Error("a limit that would be rounded is refused")
	}
}
