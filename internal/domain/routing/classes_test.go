package routing

import (
	"encoding/json"
	"testing"
)

func TestClassifyMatchesWhatCatalogsCallIt(t *testing.T) {
	for _, tc := range []struct {
		path, desc, want string
	}{
		{"x402/defi/token_security", "Get security analysis and rug pull risk indicators for a token", "solana.token-risk"},
		{"api/sol-token-safety", "Safety verdict for one Solana token mint", "solana.token-risk"},
		{"x402/defi/price", "Get real-time price for a token", "token.price"},
		{"api/v1/developer/wallet/balances", "Retrieve current token balances for one or more wallet addresses.", "wallet.balances"},
		{"search", "Search the web with Exa and return ranked results", "web.search"},
		{"v1/webpage/to-markdown", "", "web.scrape"},
		{"v1/chat/completions", "OpenAI-compatible chat", "llm.chat"},
		{"weather/current", "", "weather.forecast"},
	} {
		got, ok := Classify(tc.path, tc.desc)
		if !ok || got != tc.want {
			t.Errorf("Classify(%q, %q) = %q, %v; want %q", tc.path, tc.desc, got, ok, tc.want)
		}
	}
}

func TestClassifyLeavesOutLookAlikes(t *testing.T) {
	for _, tc := range []struct{ path, desc string }{
		{"drug/adverse-events", "Adverse events for a drug"},                                 // "risk" in a different trade
		{"x402/defi/history_price", "Get historical price series for a token or pair"},       // history, not a spot price
		{"v1/rugs/recent", "Recent rug pulls"},                                               // a feed, not a check of one token
		{"api/token-count", "Count exact LLM tokens for a string"},                           // not a model call
		{"api/token-safety", "Is this EVM token safe to trade? Works on Base and Ethereum."}, // EVM only
		{"api/web-screenshot", "Capture a PNG screenshot of a public web page"},
		{"api/v1/twitter/search", "Search tweets"},
	} {
		if got, ok := Classify(tc.path, tc.desc); ok && got != "" {
			if c, _ := ClassByID(got); c.ID == "solana.token-risk" || c.ID == "token.price" || c.ID == "llm.chat" || c.ID == "web.scrape" || c.ID == "web.search" {
				t.Errorf("Classify(%q, %q) = %q; want no match for this one", tc.path, tc.desc, got)
			}
		}
	}
}

func TestAdaptRenamesAndFixesChain(t *testing.T) {
	c, _ := ClassByID("solana.token-risk")
	a, ok := c.Adapt([]string{"chain", "tokenAddress"}, []string{"tokenAddress"})
	if !ok {
		t.Fatal("tokenAddress + chain should adapt")
	}
	out, err := a.Apply(json.RawMessage(`{"mint":"So11111111111111111111111111111111111111112"}`))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	_ = json.Unmarshal(out, &got)
	if got["tokenAddress"] != "So11111111111111111111111111111111111111112" || got["chain"] != "solana" || len(got) != 2 {
		t.Fatalf("adapted input = %s", out)
	}
}

func TestAdaptRefusesWhatItCantFill(t *testing.T) {
	c, _ := ClassByID("solana.token-risk")
	if _, ok := c.Adapt([]string{"limit", "window_s"}, nil); ok {
		t.Fatal("an endpoint with no mint-like parameter can't take the class's input")
	}
	if _, ok := c.Adapt([]string{"mint", "date"}, []string{"mint", "date"}); ok {
		t.Fatal("an endpoint that requires a parameter the class can't fill isn't callable")
	}
}

func TestAdapterLeavesOutUnmappedFields(t *testing.T) {
	a := &InputAdapter{Rename: map[string]string{"mint": "address"}}
	out, err := a.Apply(json.RawMessage(`{"mint":"M","extra":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"address":"M"}` {
		t.Fatalf("got %s", out)
	}
}

func TestTemplateFillsNestedPlaceholders(t *testing.T) {
	a := &InputAdapter{Template: json.RawMessage(`{"addresses":[{"network":"solana-mainnet","address":"{{mint}}"}]}`)}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	out, err := a.Apply(json.RawMessage(`{"mint":"M"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"addresses":[{"address":"M","network":"solana-mainnet"}]}` {
		t.Fatalf("got %s", out)
	}
	if _, err := a.Apply(json.RawMessage(`{"wallet":"W"}`)); err == nil {
		t.Fatal("a placeholder for a missing field must fail, not send an empty value")
	}
}

func TestCheckInputNamesMissingFields(t *testing.T) {
	c, _ := ClassByID("token.price")
	if err := c.CheckInput(json.RawMessage(`{"mint":"M"}`)); err != nil {
		t.Fatal(err)
	}
	if err := c.CheckInput(json.RawMessage(`{"address":"M"}`)); err == nil {
		t.Fatal("token.price takes mint, not address")
	}
}

func TestEveryClassIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Classes() {
		if seen[c.ID] {
			t.Errorf("duplicate class %s", c.ID)
		}
		seen[c.ID] = true
		if _, err := (Capability{ID: c.ID, Kind: c.Kind}).Normalize(); err != nil {
			t.Errorf("%s: %v", c.ID, err)
		}
		if err := c.CheckInput(c.Sample); err != nil {
			t.Errorf("%s: its own sample fails: %v", c.ID, err)
		}
		if a := c.Canonical(); a.Validate() != nil {
			t.Errorf("%s: canonical adapter invalid", c.ID)
		}
	}
}
