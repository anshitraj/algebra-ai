package config

import (
	"strings"
	"testing"
)

func prodConfig() *Config {
	return &Config{
		Env:                "production",
		DatabaseURL:        "postgres://u:p@db.example/algebra?sslmode=require",
		CORSAllowedOrigins: []string{"https://app.example"},
		Merchants:          MerchantsConfig{Enabled: []string{"swiggy_instamart", "amazon"}},
		Auth:               AuthConfig{PublicWebURL: "https://app.example", ResendAPIKey: "re_x"},
	}
}

func TestValidateProduction_AcceptsASafeConfig(t *testing.T) {
	t.Setenv("REDIS_ADDR", "redis.internal:6379")
	if err := prodConfig().ValidateProduction(); err != nil {
		t.Fatalf("safe production config rejected: %v", err)
	}
}

func TestValidateProduction_ReportsEveryUnsafeSetting(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	t.Setenv("ALLOW_MOCK_MERCHANT", "")
	c := prodConfig()
	c.Auth.PublicWebURL = "http://app.example"
	c.Auth.DevHeaderAuth = true
	c.Auth.ResendAPIKey = ""
	c.Merchants.Enabled = append(c.Merchants.Enabled, "mock")
	c.DatabaseURL = "postgres://u:p@localhost/algebra?sslmode=disable"
	c.CORSAllowedOrigins = []string{"http://localhost:3000"}

	err := c.ValidateProduction()
	if err == nil {
		t.Fatal("unsafe production config accepted")
	}
	for _, want := range []string{"PUBLIC_WEB_URL", "ALGEBRA_DEV_AUTH", "mock", "RESEND_API_KEY", "REDIS_ADDR", "sslmode=disable", "CORS_ALLOWED_ORIGINS"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %s:\n%v", want, err)
		}
	}
}

func TestValidateProduction_MockAllowedOnlyWhenExplicit(t *testing.T) {
	t.Setenv("REDIS_ADDR", "redis:6379")
	t.Setenv("ALLOW_MOCK_MERCHANT", "true")
	c := prodConfig()
	c.Merchants.Enabled = []string{"mock"}
	if err := c.ValidateProduction(); err != nil {
		t.Errorf("staging with ALLOW_MOCK_MERCHANT=true rejected: %v", err)
	}
}

func TestLoadPaySh(t *testing.T) {
	t.Run("on by default with Pay.sh's own addresses", func(t *testing.T) {
		t.Setenv("PAYSH_ENABLED", "")
		t.Setenv("PAYSH_CATALOG_URL", "")
		t.Setenv("PAYSH_DOCS_URL", "")
		var c PayShConfig
		if err := loadPaySh(&c); err != nil || !c.Enabled || c.CatalogURL != "" || c.DocsURL != "" {
			t.Errorf("%v %+v", err, c)
		}
	})
	t.Run("off turns it off, whatever the case", func(t *testing.T) {
		t.Setenv("PAYSH_ENABLED", " Off ")
		var c PayShConfig
		if err := loadPaySh(&c); err != nil || c.Enabled {
			t.Errorf("%v %+v", err, c)
		}
	})
	t.Run("https addresses are taken as given", func(t *testing.T) {
		t.Setenv("PAYSH_CATALOG_URL", " https://mirror.example/catalog.json ")
		t.Setenv("PAYSH_DOCS_URL", "https://mirror.example/api")
		var c PayShConfig
		if err := loadPaySh(&c); err != nil || c.CatalogURL != "https://mirror.example/catalog.json" || c.DocsURL != "https://mirror.example/api" {
			t.Errorf("%v %+v", err, c)
		}
	})
	for _, bad := range []string{"http://mirror.example/c", "ftp://mirror.example/c", "https://user:pw@mirror.example/c", "https:///c", "not a url", "//mirror.example/c"} {
		t.Run("refuses "+bad, func(t *testing.T) {
			t.Setenv("PAYSH_CATALOG_URL", bad)
			t.Setenv("PAYSH_DOCS_URL", "")
			var c PayShConfig
			if err := loadPaySh(&c); err == nil || !strings.Contains(err.Error(), "PAYSH_CATALOG_URL") {
				t.Errorf("got %v", err)
			}
		})
	}
}
