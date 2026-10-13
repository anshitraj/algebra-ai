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

// A production server that pays on a Solana cluster pays from a person's
// vault there, never from the payer's own funds.
func TestValidateProduction_ARealRailNeedsVaultsRequired(t *testing.T) {
	t.Setenv("REDIS_ADDR", "redis.internal:6379")
	c := prodConfig()
	c.SolanaRails = []SolanaConfig{{Cluster: "devnet", Signer: "generated"}}
	err := c.ValidateProduction()
	if err == nil || !strings.Contains(err.Error(), "SPEND_PASS_REQUIRED=devnet") {
		t.Fatalf("a devnet rail whose payments aren't vault-only must be refused, got %v", err)
	}
	c.OnchainPasses.Required = map[string]bool{"devnet": true}
	if err := c.ValidateProduction(); err != nil {
		t.Fatalf("with vaults required: %v", err)
	}
	c.OnchainPasses.Disabled = true
	if err := c.ValidateProduction(); err == nil {
		t.Fatal("the vault program switched off defeats the requirement")
	}
	// No rail, nothing to require (the sandbox has no wallet).
	c = prodConfig()
	if err := c.ValidateProduction(); err != nil {
		t.Fatalf("no rail: %v", err)
	}
}

// Real money is signed by a key that never sits on the server.
func TestValidateProduction_AMainnetPayerMustBeCloudKMS(t *testing.T) {
	t.Setenv("REDIS_ADDR", "redis.internal:6379")
	c := prodConfig()
	c.OnchainPasses.Required = map[string]bool{"mainnet": true}
	c.SolanaRails = []SolanaConfig{{Cluster: "mainnet", KeypairFile: "/secrets/mainnet.json"}}
	err := c.ValidateProduction()
	if err == nil || !strings.Contains(err.Error(), "SOLANA_MAINNET_KMS_KEY_VERSION") {
		t.Fatalf("a mainnet key in a file must be refused in production, got %v", err)
	}
	c.SolanaRails = []SolanaConfig{{Cluster: "mainnet", KMSKeyVersion: "projects/p/locations/global/keyRings/r/cryptoKeys/k/cryptoKeyVersions/1"}}
	if err := c.ValidateProduction(); err != nil {
		t.Fatalf("a KMS key: %v", err)
	}
	// Devnet may still use a generated or file key.
	c.SolanaRails = []SolanaConfig{{Cluster: "devnet", Signer: "generated"}}
	c.OnchainPasses.Required = map[string]bool{"devnet": true}
	if err := c.ValidateProduction(); err != nil {
		t.Fatalf("devnet: %v", err)
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

func TestLoadSolanaRails(t *testing.T) {
	clear := func(t *testing.T) {
		for _, k := range []string{"SOLANA_DEVNET_KEYPAIR", "SOLANA_DEVNET_KEYPAIR_FILE", "SOLANA_DEVNET_RPC_URL", "SOLANA_MAINNET_KEYPAIR", "SOLANA_MAINNET_KEYPAIR_FILE", "SOLANA_MAINNET_RPC_URL", "SOLANA_ALLOW_MAINNET", "SOLANA_MAX_PAYMENT_USDC", "SOLANA_DEVNET_SIGNER", "SOLANA_DEVNET_SIGNER_PUBKEY", "SOLANA_MAINNET_SIGNER", "SOLANA_MAINNET_SIGNER_PUBKEY"} {
			t.Setenv(k, "")
		}
	}
	t.Run("none configured is none", func(t *testing.T) {
		clear(t)
		if rails, err := loadSolanaRails(SolanaConfig{}); err != nil || len(rails) != 0 {
			t.Errorf("%v %+v", err, rails)
		}
	})
	t.Run("devnet and mainnet side by side", func(t *testing.T) {
		clear(t)
		t.Setenv("SOLANA_DEVNET_KEYPAIR_FILE", ".data/devnet.json")
		t.Setenv("SOLANA_DEVNET_RPC_URL", "https://devnet.example")
		t.Setenv("SOLANA_MAINNET_KEYPAIR_FILE", ".data/mainnet.json")
		t.Setenv("SOLANA_ALLOW_MAINNET", "yes")
		t.Setenv("SOLANA_MAX_PAYMENT_USDC", "0.25")
		rails, err := loadSolanaRails(SolanaConfig{})
		if err != nil || len(rails) != 2 || rails[0].Cluster != "devnet" || rails[0].RPCURL != "https://devnet.example" || rails[1].Cluster != "mainnet" || rails[1].MaxPaymentMinor != 250_000 {
			t.Errorf("%v %+v", err, rails)
		}
	})
	t.Run("mainnet needs the explicit yes", func(t *testing.T) {
		clear(t)
		t.Setenv("SOLANA_MAINNET_KEYPAIR", "[1,2,3]")
		if _, err := loadSolanaRails(SolanaConfig{}); err == nil || !strings.Contains(err.Error(), "SOLANA_ALLOW_MAINNET") {
			t.Errorf("got %v", err)
		}
	})
	t.Run("a cluster configured twice is refused", func(t *testing.T) {
		clear(t)
		t.Setenv("SOLANA_DEVNET_KEYPAIR_FILE", ".data/devnet.json")
		if _, err := loadSolanaRails(SolanaConfig{Cluster: "devnet", KeypairFile: "x"}); err == nil || !strings.Contains(err.Error(), "twice") {
			t.Errorf("got %v", err)
		}
	})
	t.Run("a generated devnet signer needs no wallet", func(t *testing.T) {
		clear(t)
		t.Setenv("SOLANA_DEVNET_SIGNER", "generated")
		t.Setenv("SOLANA_DEVNET_SIGNER_PUBKEY", "PinnedAddress111")
		rails, err := loadSolanaRails(SolanaConfig{})
		if err != nil || len(rails) != 1 || rails[0].Cluster != "devnet" || rails[0].Signer != "generated" || rails[0].SignerPubkey != "PinnedAddress111" || rails[0].Keypair != "" {
			t.Errorf("%v %+v", err, rails)
		}
	})
	t.Run("a generated signer and a wallet together are refused", func(t *testing.T) {
		clear(t)
		t.Setenv("SOLANA_DEVNET_SIGNER", "generated")
		t.Setenv("SOLANA_DEVNET_KEYPAIR_FILE", ".data/devnet.json")
		if _, err := loadSolanaRails(SolanaConfig{}); err == nil || !strings.Contains(err.Error(), "not several") {
			t.Errorf("got %v", err)
		}
	})
	t.Run("a generated signer on mainnet is refused", func(t *testing.T) {
		clear(t)
		t.Setenv("SOLANA_ALLOW_MAINNET", "yes")
		t.Setenv("SOLANA_MAINNET_SIGNER", "generated")
		if _, err := loadSolanaRails(SolanaConfig{}); err == nil || !strings.Contains(err.Error(), "devnet only") {
			t.Errorf("got %v", err)
		}
	})
	t.Run("an unknown signer kind is refused", func(t *testing.T) {
		clear(t)
		t.Setenv("SOLANA_DEVNET_SIGNER", "magic")
		if _, err := loadSolanaRails(SolanaConfig{}); err == nil || !strings.Contains(err.Error(), "SOLANA_DEVNET_SIGNER") {
			t.Errorf("got %v", err)
		}
	})
	t.Run("a Cloud KMS key stands in for a wallet", func(t *testing.T) {
		clear(t)
		const v = "projects/p/locations/global/keyRings/r/cryptoKeys/k/cryptoKeyVersions/1"
		t.Setenv("SOLANA_ALLOW_MAINNET", "yes")
		t.Setenv("SOLANA_MAINNET_KMS_KEY_VERSION", v)
		rails, err := loadSolanaRails(SolanaConfig{})
		if err != nil || len(rails) != 1 || rails[0].Cluster != "mainnet" || rails[0].KMSKeyVersion != v || rails[0].Keypair != "" {
			t.Errorf("%v %+v", err, rails)
		}
	})
	t.Run("a KMS key and a wallet together are refused", func(t *testing.T) {
		clear(t)
		t.Setenv("SOLANA_ALLOW_MAINNET", "yes")
		t.Setenv("SOLANA_MAINNET_KMS_KEY_VERSION", "projects/p/locations/global/keyRings/r/cryptoKeys/k/cryptoKeyVersions/1")
		t.Setenv("SOLANA_MAINNET_KEYPAIR_FILE", ".data/mainnet.json")
		if _, err := loadSolanaRails(SolanaConfig{}); err == nil || !strings.Contains(err.Error(), "not both") {
			t.Errorf("got %v", err)
		}
	})
	t.Run("a KMS value that is not one exact key version is refused at start", func(t *testing.T) {
		clear(t)
		t.Setenv("SOLANA_ALLOW_MAINNET", "yes")
		for _, bad := range []string{"my-key", "projects/p/locations/global/keyRings/r/cryptoKeys/k"} {
			t.Setenv("SOLANA_MAINNET_KMS_KEY_VERSION", bad)
			if _, err := loadSolanaRails(SolanaConfig{}); err == nil || !strings.Contains(err.Error(), "exact Cloud KMS key version") {
				t.Errorf("%q: got %v", bad, err)
			}
		}
	})
	t.Run("the legacy single rail still counts", func(t *testing.T) {
		clear(t)
		if rails, err := loadSolanaRails(SolanaConfig{Cluster: "devnet", KeypairFile: "x", MaxPaymentMinor: 1}); err != nil || len(rails) != 1 || rails[0].Cluster != "devnet" {
			t.Errorf("%v %+v", err, rails)
		}
	})
}

func TestMainnetExecutionNeedsOnChainPassesRequired(t *testing.T) {
	if err := checkMainnetExecution(false, nil); err != nil {
		t.Errorf("mainnet execution off needs nothing: %v", err)
	}
	if err := checkMainnetExecution(true, map[string]bool{"mainnet": true}); err != nil {
		t.Errorf("on, with passes required on mainnet: %v", err)
	}
	for name, required := range map[string]map[string]bool{"nothing required": nil, "devnet only": {"devnet": true}} {
		if err := checkMainnetExecution(true, required); err == nil {
			t.Errorf("%s: MAINNET_EXECUTION=on must be refused unless SPEND_PASS_REQUIRED includes mainnet", name)
		}
	}
}
