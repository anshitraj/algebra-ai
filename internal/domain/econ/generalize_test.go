package econ

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

// goldenIntentHash was produced by the code before Constraints grew its
// routing fields. An intent that doesn't use them must keep this hash: it is
// what existing receipts commit to.
const goldenIntentHash = "sha256:4a3b55eb10dcbcc185deea014a5f537e6d9b01630d80d9f0fe086902ae5340a4"

func goldenSpec() Spec {
	return Spec{
		Capability: "solana.token-risk", Input: json.RawMessage(`{"mint":"SOL"}`), Window: "2026-09-29T10",
		Currency: "USDC", BudgetMaxMinor: 50_000, Constraints: Constraints{MaxAgeSeconds: 60, MaxLatencyMS: 2000},
		ProviderPolicy: ProviderPolicy{Strategy: "best_execution"},
	}
}

func TestExistingIntentHashIsUnchanged(t *testing.T) {
	now := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	in, err := New("eint_g", "user_1", "pass_1", "agent_a", goldenSpec(), now)
	if err != nil {
		t.Fatal(err)
	}
	if in.IntentHash != goldenIntentHash {
		t.Fatalf("intent hash changed for an intent that uses no new fields:\n got %s\nwant %s", in.IntentHash, goldenIntentHash)
	}
	if in.ProviderPolicy.Strategy != StrategyAuto {
		t.Errorf("the old name best_execution should normalize to auto, got %q", in.ProviderPolicy.Strategy)
	}
}

func TestRoutingConstraintsAreCommittedToTheHash(t *testing.T) {
	now := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	spec := goldenSpec()
	spec.Constraints.AllowedNetworks = []string{"Solana", "BASE", "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp"}
	spec.Constraints.AllowedAssets = []string{"usdc", " USDC "}
	a, err := New("eint_a", "user_1", "pass_1", "agent_a", spec, now)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(a.Constraints.AllowedNetworks, []string{"base", "solana"}) || !slices.Equal(a.Constraints.AllowedAssets, []string{"USDC"}) {
		t.Fatalf("lists should be canonical: %+v", a.Constraints)
	}
	if a.IntentHash == goldenIntentHash {
		t.Error("a new constraint must change the hash")
	}
	// The same constraints spelled differently are the same intent.
	spec.Constraints.AllowedNetworks = []string{"base", "solana"}
	spec.Constraints.AllowedAssets = []string{"USDC"}
	b, err := New("eint_b", "user_1", "pass_1", "agent_b", spec, now)
	if err != nil {
		t.Fatal(err)
	}
	if a.IntentHash != b.IntentHash {
		t.Errorf("same constraints, different hash:\n%s\n%s", a.IntentHash, b.IntentHash)
	}
	// Constraints don't change which outcome this is, only how it may be met.
	spec.Constraints = Constraints{}
	c, err := New("eint_c", "user_1", "pass_1", "agent_c", spec, now)
	if err != nil {
		t.Fatal(err)
	}
	if c.EffectKey != a.EffectKey {
		t.Errorf("two agents asking for the same outcome under different constraints must share an effect key: %s vs %s", c.EffectKey, a.EffectKey)
	}
}

func TestConstraintsRefuseNonsense(t *testing.T) {
	bad := map[string]Constraints{
		"negative age":         {MaxAgeSeconds: -1},
		"negative latency":     {MaxLatencyMS: -5},
		"slippage over 100%":   {MaxSlippageBps: 10_001},
		"negative impact":      {MaxPriceImpactBps: -1},
		"quality over 100":     {MinQuality: 101},
		"reliability over 100": {MinReliabilityPct: 101},
		"junk network":         {AllowedNetworks: []string{"not a network!"}},
		"junk asset":           {AllowedAssets: []string{"us dc"}},
		"too many networks":    {AllowedNetworks: manyNames(maxListEntries + 1)},
	}
	for name, c := range bad {
		if _, err := c.Normalize(); err == nil {
			t.Errorf("%s should be refused: %+v", name, c)
		}
	}
	good := Constraints{MaxSlippageBps: 50, MaxPriceImpactBps: 100, MinQuality: 80, MinReliabilityPct: 95, MaxLatencyMS: 1500}
	if got, err := good.Normalize(); err != nil || got.AllowedNetworks != nil || got.AllowedAssets != nil {
		t.Errorf("valid constraints should pass untouched: %+v, %v", got, err)
	}
}

func manyNames(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "net" + string(rune('a'+i))
	}
	return out
}

func TestProviderPolicyNormalize(t *testing.T) {
	ok := map[string]string{
		"": "", "auto": "auto", "Cheapest": "cheapest", " FASTEST ": "fastest", "best_execution": "auto",
	}
	for in, want := range ok {
		got, err := ProviderPolicy{Strategy: in}.Normalize()
		if err != nil || got.Strategy != want {
			t.Errorf("strategy %q = %q, %v; want %q", in, got.Strategy, err, want)
		}
	}
	if _, err := (ProviderPolicy{Strategy: "bogus"}).Normalize(); err == nil {
		t.Error("an unknown strategy must be refused")
	}
	if _, err := (ProviderPolicy{Strategy: "fixed"}).Normalize(); err == nil {
		t.Error("fixed without providers pins nothing and must be refused")
	}
	got, err := ProviderPolicy{Strategy: "fixed", Providers: []string{" X402:One ", "x402:one", "x402:Two"}}.Normalize()
	if err != nil || !slices.Equal(got.Providers, []string{"x402:one", "x402:two"}) {
		t.Errorf("providers keep their order, lower-cased, no duplicates: %+v, %v", got.Providers, err)
	}
	if _, err := (ProviderPolicy{Providers: []string{"has spaces"}}).Normalize(); err == nil {
		t.Error("a provider name with spaces must be refused")
	}
}

func TestNormalizeCapability(t *testing.T) {
	if got, err := NormalizeCapability("  Solana.Token-Risk "); err != nil || got != "solana.token-risk" {
		t.Errorf("got %q, %v", got, err)
	}
	for _, bad := range []string{"", "x", "Has Spaces", "-leading", strings.Repeat("a", 65)} {
		if _, err := NormalizeCapability(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
}
