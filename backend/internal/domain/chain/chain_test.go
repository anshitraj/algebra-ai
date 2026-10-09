package chain

import "testing"

func TestNormalizeNetwork(t *testing.T) {
	cases := map[string]string{
		"solana":   Solana,
		" Solana ": Solana,
		"solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp": Solana,
		"solana:EtWTRABZaYq6iMfeYKouRu166VU2xqa1": SolanaDevnet,
		"solana-devnet":  SolanaDevnet,
		"eip155:8453":    Base,
		"BASE":           Base,
		"eip155:84532":   BaseSepolia,
		"eip155:5042":    Arc,
		"eip155:5042002": ArcTestnet,
		"eip155:1":       "eip155:1",
		"sandbox":        Sandbox,
		"SomeNewChain":   "somenewchain",
		"":               "",
	}
	for in, want := range cases {
		if got := NormalizeNetwork(in); got != want {
			t.Errorf("NormalizeNetwork(%q) = %q, want %q", in, got, want)
		}
	}
}

// A CAIP-2 Solana reference is case-sensitive base58: a lower-cased lookalike
// must not be treated as mainnet.
func TestCAIP2LookalikeIsNotMainnet(t *testing.T) {
	got := NormalizeNetwork("solana:5eykt4usfv8p8njdtrepy1vzqkqzkvdp")
	if got == Solana {
		t.Fatalf("a differently-cased CAIP-2 reference resolved to mainnet: %q", got)
	}
}

func TestIsTestNetwork(t *testing.T) {
	for _, n := range []string{"solana-devnet", "solana:EtWTRABZaYq6iMfeYKouRu166VU2xqa1", "base-sepolia", "arc-testnet", "eip155:5042002", "sandbox"} {
		if !IsTestNetwork(n) {
			t.Errorf("%s moves no real money", n)
		}
	}
	for _, n := range []string{"solana", "base", "arc", "eip155:5042", "unknown-chain"} {
		if IsTestNetwork(n) {
			t.Errorf("%s must not be treated as a test network", n)
		}
	}
}

func TestAssetAddress(t *testing.T) {
	a, ok := AssetAddress("solana", "usdc")
	if !ok || a != "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v" {
		t.Errorf("solana USDC = %q, %v", a, ok)
	}
	if a, ok := AssetAddress("solana-devnet", "USDC"); !ok || a != "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU" {
		t.Errorf("devnet USDC = %q, %v", a, ok)
	}
	if _, ok := AssetAddress("solana", "BONK"); ok {
		t.Error("only USDC addresses are known")
	}
	if a, ok := AssetAddress("eip155:5042", "USDC"); !ok || a != "0x3600000000000000000000000000000000000000" {
		t.Errorf("Arc USDC = %q, %v", a, ok)
	}
	if _, ok := AssetAddress("ethereum", "USDC"); ok {
		t.Error("Algebra names no chain beyond Solana, Base and Arc")
	}
	if _, ok := AssetAddress("somechain", "USDC"); ok {
		t.Error("an unknown network has no known USDC")
	}
}

func TestSameAddress(t *testing.T) {
	if !SameAddress("base", "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913", "0x833589fcd6edb6e08f4c7c32d4f71b54bda02913") {
		t.Error("EVM addresses compare case-insensitively")
	}
	if SameAddress("solana", "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v", "epjfwdd5aufqsseqm2qn1xzybapc8g4wegkzwytdt1v") {
		t.Error("Solana addresses are case-sensitive base58")
	}
}

func TestParseUnits(t *testing.T) {
	ok := map[string]int64{
		"0.05": 50_000, "1": 1_000_000, "0": 0, "0.000001": 1, "12.5": 12_500_000, "100": 100_000_000, " 0.1 ": 100_000,
		"9223372036854.775807": 9_223_372_036_854_775_807,
	}
	for in, want := range ok {
		if got, err := ParseUnits(in, USDCDecimals); err != nil || got != want {
			t.Errorf("ParseUnits(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{
		"", " ", ".", ".5", "5.", "-1", "+1", "1e3", "0x10", "1,5", "1.2.3", "abc", "0.0000001", "1 000",
		"9223372036854.775808", "99999999999999999999",
	} {
		if got, err := ParseUnits(in, USDCDecimals); err == nil {
			t.Errorf("ParseUnits(%q) = %d, must be refused", in, got)
		}
	}
	if got, err := ParseUnits("1.5", 9); err != nil || got != 1_500_000_000 {
		t.Errorf("SOL has 9 decimals: %d %v", got, err)
	}
	if got, err := ParseUnits("7", 0); err != nil || got != 7 {
		t.Errorf("0 decimals: %d %v", got, err)
	}
	if _, err := ParseUnits("7.1", 0); err == nil {
		t.Error("no fractional digits are allowed at 0 decimals")
	}
}

func TestFormatUnits(t *testing.T) {
	for v, want := range map[int64]string{50_000: "0.05", 1_000_000: "1", 1: "0.000001", 12_500_000: "12.5", 0: "0", -50_000: "-0.05", 123_456_789: "123.456789"} {
		if got := FormatUnits(v, USDCDecimals); got != want {
			t.Errorf("FormatUnits(%d) = %q, want %q", v, got, want)
		}
	}
	for _, v := range []int64{0, 1, 999_999, 1_000_000, 50_000, 123_456_789, 9_223_372_036_854_775_807} {
		back, err := ParseUnits(FormatUnits(v, USDCDecimals), USDCDecimals)
		if err != nil || back != v {
			t.Errorf("round trip %d -> %q -> %d (%v)", v, FormatUnits(v, USDCDecimals), back, err)
		}
	}
	if FormatUnits(5, 0) != "5" {
		t.Error("0 decimals")
	}
}
