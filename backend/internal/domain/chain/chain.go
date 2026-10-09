// Package chain names networks and assets one way across Algebra, so a
// provider that says "solana:5eykt4Us…" (x402 v2, CAIP-2) and one that says
// "solana" (x402 v1) compare equal, and so a token that merely calls itself
// USDC is never mistaken for the real one.
//
// It is a leaf package (no imports from Algebra) because intents, routing,
// policy and the payment rails all need the same names.
package chain

import (
	"fmt"
	"math"
	"strings"
)

// Canonical network names. Algebra pays on Solana. Base and Arc are named
// because the catalogs it reads (Circle's Agent Marketplace, Coinbase's
// Bazaar) list endpoints there too; any other chain is just a string.
const (
	Solana       = "solana"
	SolanaDevnet = "solana-devnet"
	Base         = "base"
	BaseSepolia  = "base-sepolia"
	// Arc is Circle's own chain, where USDC is the native gas token.
	Arc        = "arc"
	ArcTestnet = "arc-testnet"
	// Sandbox is Algebra's simulated network (providers/sandboxpay): no chain.
	Sandbox = "sandbox"
)

// caip2 maps CAIP-2 chain IDs to canonical names. Solana's reference is the
// first 32 characters of the genesis hash and is case-sensitive base58, so
// lookups here are exact: a differently-cased lookalike is not the chain.
var caip2 = map[string]string{
	"solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp": Solana,
	"solana:EtWTRABZaYq6iMfeYKouRu166VU2xqa1": SolanaDevnet,
	"eip155:8453":    Base,
	"eip155:84532":   BaseSepolia,
	"eip155:5042":    Arc,
	"eip155:5042002": ArcTestnet,
}

// NormalizeNetwork returns the canonical name for a network as a provider or
// a person wrote it: x402 v1 short names and CAIP-2 IDs resolve to the same
// name. Unknown networks are lower-cased, not rejected: policy decides
// whether they're allowed, not spelling.
func NormalizeNetwork(raw string) string {
	raw = strings.TrimSpace(raw)
	if n, ok := caip2[raw]; ok {
		return n
	}
	return strings.ToLower(raw)
}

// IsTestNetwork reports whether a network moves no real money. Payments on
// these are real transactions on a real chain, but they are never presented
// as real spend.
func IsTestNetwork(network string) bool {
	switch NormalizeNetwork(network) {
	case SolanaDevnet, BaseSepolia, ArcTestnet, Sandbox:
		return true
	}
	return false
}

// NormalizeAsset upper-cases a token symbol ("usdc" becomes "USDC").
func NormalizeAsset(raw string) string {
	return strings.ToUpper(strings.TrimSpace(raw))
}

// usdc holds Circle's native USDC on each network Algebra knows. A payment
// requirement that names USDC at any other address is a different token.
var usdc = map[string]string{
	Solana:       "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
	SolanaDevnet: "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU",
	Base:         "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
	BaseSepolia:  "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
	// On Arc, USDC is the native token, exposed as an ERC-20 at a fixed
	// system address (as Circle's own listings name it).
	Arc:        "0x3600000000000000000000000000000000000000",
	ArcTestnet: "0x3600000000000000000000000000000000000000",
}

// AssetAddress returns the canonical contract or mint address of a
// well-known asset on a network. Only USDC is known today.
func AssetAddress(network, symbol string) (string, bool) {
	if NormalizeAsset(symbol) != "USDC" {
		return "", false
	}
	a, ok := usdc[NormalizeNetwork(network)]
	return a, ok
}

// IsSolana reports whether a network is a Solana cluster.
func IsSolana(network string) bool {
	n := NormalizeNetwork(network)
	return n == Solana || n == SolanaDevnet
}

// SameAddress compares two addresses on a network: EVM hex addresses are
// case-insensitive (the case is only a checksum), Solana's base58 are exact.
func SameAddress(network, a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if IsSolana(network) {
		return a == b
	}
	return strings.EqualFold(a, b)
}

// USDCDecimals is how many decimal places USDC has on every network Algebra
// knows: one USDC is 1,000,000 minor units.
const USDCDecimals = 6

// ParseUnits reads a decimal amount such as "0.05" into integer minor units
// for an asset with the given number of decimals. It is strict on purpose:
// digits and at most one dot, no sign, no exponent, and no more fractional
// digits than the asset has. A limit that is silently rounded is a limit the
// person didn't set.
func ParseUnits(s string, decimals int) (int64, error) {
	s = strings.TrimSpace(s)
	whole, frac, hasDot := strings.Cut(s, ".")
	if whole == "" || (hasDot && frac == "") {
		return 0, fmt.Errorf("%q isn't an amount like 0.05", s)
	}
	if len(frac) > decimals {
		return 0, fmt.Errorf("%q has more than %d decimal places", s, decimals)
	}
	var n int64
	for _, part := range []string{whole, frac + strings.Repeat("0", decimals-len(frac))} {
		for _, c := range part {
			if c < '0' || c > '9' {
				return 0, fmt.Errorf("%q isn't an amount like 0.05", s)
			}
			d := int64(c - '0')
			if n > (math.MaxInt64-d)/10 {
				return 0, fmt.Errorf("%q is too large", s)
			}
			n = n*10 + d
		}
	}
	return n, nil
}

// FormatUnits renders minor units as a decimal amount without trailing
// zeros: 50000 with 6 decimals is "0.05", 1000000 is "1".
func FormatUnits(v int64, decimals int) string {
	neg := v < 0
	u := uint64(v)
	if neg {
		u = -u
	}
	s := fmt.Sprintf("%d", u)
	if decimals > 0 {
		for len(s) <= decimals {
			s = "0" + s
		}
		s = strings.TrimRight(s[:len(s)-decimals]+"."+s[len(s)-decimals:], "0")
		s = strings.TrimSuffix(s, ".")
	}
	if neg {
		return "-" + s
	}
	return s
}
