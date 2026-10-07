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
