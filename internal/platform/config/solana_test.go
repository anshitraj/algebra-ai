package config

import (
	"strings"
	"testing"
)

func solanaEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for _, k := range []string{"SOLANA_CLUSTER", "SOLANA_RPC_URL", "SOLANA_KEYPAIR", "SOLANA_KEYPAIR_FILE", "SOLANA_MAX_PAYMENT_USDC", "SOLANA_ALLOW_MAINNET"} {
		t.Setenv(k, "")
	}
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func TestSolanaIsOffUnlessAClusterIsNamed(t *testing.T) {
	solanaEnv(t, map[string]string{"SOLANA_KEYPAIR_FILE": "/x"})
	var c SolanaConfig
	if err := loadSolana(&c); err != nil || c.Cluster != "" {
		t.Errorf("no cluster, no rail, and a stray keypair setting changes nothing: %+v %v", c, err)
	}
}

func TestSolanaDevnetDefaults(t *testing.T) {
	solanaEnv(t, map[string]string{"SOLANA_CLUSTER": " DevNet ", "SOLANA_KEYPAIR_FILE": ".data/dev.json"})
	var c SolanaConfig
	if err := loadSolana(&c); err != nil {
		t.Fatal(err)
	}
	if c.Cluster != "devnet" || c.KeypairFile != ".data/dev.json" || c.MaxPaymentMinor != 1_000_000 || c.RPCURL != "" {
		t.Errorf("config: %+v", c)
	}
}

func TestSolanaMainnetNeedsAnExplicitYes(t *testing.T) {
	base := map[string]string{"SOLANA_CLUSTER": "mainnet", "SOLANA_KEYPAIR_FILE": "k.json"}
	solanaEnv(t, base)
	var c SolanaConfig
	if err := loadSolana(&c); err == nil || !strings.Contains(err.Error(), "SOLANA_ALLOW_MAINNET=yes") {
		t.Fatalf("mainnet without the acknowledgement must stop startup: %v", err)
	}
	for _, v := range []string{"true", "1", "y", "on", "YES please"} {
		solanaEnv(t, map[string]string{"SOLANA_CLUSTER": "mainnet", "SOLANA_KEYPAIR_FILE": "k.json", "SOLANA_ALLOW_MAINNET": v})
		if err := loadSolana(&SolanaConfig{}); err == nil {
			t.Errorf("%q is not the acknowledgement", v)
		}
	}
	solanaEnv(t, map[string]string{"SOLANA_CLUSTER": "mainnet-beta", "SOLANA_KEYPAIR_FILE": "k.json", "SOLANA_ALLOW_MAINNET": "YES"})
	var ok SolanaConfig
	if err := loadSolana(&ok); err != nil || ok.Cluster != "mainnet" {
		t.Errorf("acknowledged: %+v %v", ok, err)
	}
}

func TestSolanaRefusesBadSettings(t *testing.T) {
	for name, kv := range map[string]map[string]string{
		"unknown cluster": {"SOLANA_CLUSTER": "testnet", "SOLANA_KEYPAIR_FILE": "k"},
		"no wallet":       {"SOLANA_CLUSTER": "devnet"},
		"bad ceiling":     {"SOLANA_CLUSTER": "devnet", "SOLANA_KEYPAIR_FILE": "k", "SOLANA_MAX_PAYMENT_USDC": "abc"},
		"zero ceiling":    {"SOLANA_CLUSTER": "devnet", "SOLANA_KEYPAIR_FILE": "k", "SOLANA_MAX_PAYMENT_USDC": "0"},
		"negative":        {"SOLANA_CLUSTER": "devnet", "SOLANA_KEYPAIR_FILE": "k", "SOLANA_MAX_PAYMENT_USDC": "-1"},
		"too precise":     {"SOLANA_CLUSTER": "devnet", "SOLANA_KEYPAIR_FILE": "k", "SOLANA_MAX_PAYMENT_USDC": "0.0000001"},
	} {
		solanaEnv(t, kv)
		if err := loadSolana(&SolanaConfig{}); err == nil {
			t.Errorf("%s must stop startup", name)
		}
	}
	solanaEnv(t, map[string]string{"SOLANA_CLUSTER": "devnet", "SOLANA_KEYPAIR": "[1,2,3]", "SOLANA_MAX_PAYMENT_USDC": "0.25", "SOLANA_RPC_URL": " https://rpc.example/ "})
	var c SolanaConfig
	if err := loadSolana(&c); err != nil || c.MaxPaymentMinor != 250_000 || c.RPCURL != "https://rpc.example/" || c.Keypair != "[1,2,3]" {
		t.Errorf("a valid custom config: %+v %v", c, err)
	}
}
