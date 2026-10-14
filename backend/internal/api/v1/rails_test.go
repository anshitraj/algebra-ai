package v1

import (
	"encoding/json"
	"strings"
	"testing"
)

func u64(v uint64) *uint64 { return &v }

func TestRailFunded(t *testing.T) {
	cases := []struct {
		name    string
		balance *uint64
		ceiling int64
		want    bool
	}{
		{"no USDC account", nil, 1_000_000, false},
		{"an empty account", u64(0), 1_000_000, false},
		{"less than one payment", u64(999_999), 1_000_000, false},
		{"exactly one payment", u64(1_000_000), 1_000_000, true},
		{"plenty", u64(15_974_000), 1_000_000, true},
		{"no ceiling set: any USDC", u64(1), 0, true},
	}
	for _, c := range cases {
		if got := railFunded(c.balance, c.ceiling); got != c.want {
			t.Errorf("%s: funded = %v, want %v", c.name, got, c.want)
		}
	}
}

// The view any signed-in account reads carries no wallet address and no balance.
func TestRailViewNamesNoWalletAndNoBalance(t *testing.T) {
	b, err := json.Marshal(railView{Network: "solana-devnet", Configured: true, Rail: "x402-solana-devnet", Funded: true, MaxPaymentMinor: 1_000_000})
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"address", "usdc_minor", "balance", "keypair", "pubkey"} {
		if strings.Contains(string(b), banned) {
			t.Errorf("%q must not be in the rails view: %s", banned, b)
		}
	}
}
