package wiring

import (
	"crypto/rand"
	"strings"
	"testing"

	"github.com/project-algebra/algebra/internal/domain/merchant"
	"github.com/project-algebra/algebra/internal/domain/privacy"
	"github.com/project-algebra/algebra/internal/platform/config"
)

func testEncryptor(t *testing.T) *privacy.AESGCMEncryptor {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	enc, err := privacy.NewAESGCMEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	return enc
}

// Every ENABLED_MERCHANTS name must register a connector that answers to
// that same name — it is what intents' preferred/excluded merchants refer to.
func TestBuildConnectors_NamesMatchAndEveryOptionReportsStatus(t *testing.T) {
	names := []string{"mock", "zepto", "swiggy_instamart", "amazon", "flipkart", "blinkit", "generic-browser"}
	registry, err := buildConnectors(config.MerchantsConfig{Enabled: names, SessionDir: t.TempDir()}, testEncryptor(t))
	if err != nil {
		t.Fatalf("buildConnectors: %v", err)
	}
	for _, name := range names {
		c, err := registry.Get(name)
		if err != nil {
			t.Fatalf("%s not registered: %v", name, err)
		}
		if c.Name() != name {
			t.Fatalf("factory %q built a connector named %q", name, c.Name())
		}
		if _, ok := c.(merchant.StatusReporter); !ok {
			t.Fatalf("%s should explain its readiness via Status()", name)
		}
	}
	// With no credentials and no linked accounts, only the mock can search.
	for _, c := range registry.List() {
		if c.Name() != "mock" && c.Capabilities().Search {
			t.Fatalf("%s claims search without any credentials", c.Name())
		}
	}
}

func TestBuildConnectors_UnknownMerchantFailsFast(t *testing.T) {
	_, err := buildConnectors(config.MerchantsConfig{Enabled: []string{"mock", "bigbasket"}}, testEncryptor(t))
	if err == nil || !strings.Contains(err.Error(), "bigbasket") {
		t.Fatalf("expected an unknown-merchant error, got %v", err)
	}
}

func TestBuildConnectors_DefaultsWhenUnset(t *testing.T) {
	registry, err := buildConnectors(config.MerchantsConfig{SessionDir: t.TempDir()}, testEncryptor(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Get("swiggy_instamart"); err == nil {
		t.Fatal("swiggy_instamart must be opt-in")
	}
	if _, err := registry.Get("mock"); err != nil {
		t.Fatal("defaults should include the mock connector")
	}
}
