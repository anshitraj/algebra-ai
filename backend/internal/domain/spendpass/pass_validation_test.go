package spendpass

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func usdcPass(now time.Time) Pass {
	p := basePass(now)
	p.Currency = "USDC"
	p.BudgetMinorUnits = 1_000_000
	p.MaxPerPurchaseMinorUnits = i64(100_000)
	p.ApproveAboveMinorUnits = i64(50_000)
	p.AllowedCategories = []string{}
	return p
}

func TestNormalizeRefusesWhatLooksLikeAMistake(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name   string
		mutate func(p *Pass)
		want   string
	}{
		{"a budget of 9 billion USDC", func(p *Pass) { p.BudgetMinorUnits = 9_000_000_000_000_000 }, "at most 1000000 USDC"},
		{"a budget just over the maximum", func(p *Pass) { p.BudgetMinorUnits = 1_000_000_000_001 }, "at most 1000000 USDC"},
		{"a name with a right-to-left override", func(p *Pass) { p.Label = "Claude ‮evil" }, "direction-changing"},
		{"a name with a zero-width space", func(p *Pass) { p.Label = "pay​ment" }, "direction-changing"},
		{"a name with a newline", func(p *Pass) { p.Label = "one\ntwo" }, "direction-changing"},
		{"a provider ID with a space", func(p *Pass) { p.AllowedMerchants = []string{"demo alpha"} }, "isn't a provider ID"},
		{"a provider ID that is markup", func(p *Pass) { p.AllowedMerchants = []string{"<script>"} }, "isn't a provider ID"},
		{"a provider ID of 65 characters", func(p *Pass) { p.AllowedMerchants = []string{strings.Repeat("a", 65)} }, "isn't a provider ID"},
		{"101 providers", func(p *Pass) {
			for i := 0; i < 101; i++ {
				p.AllowedMerchants = append(p.AllowedMerchants, fmt.Sprintf("paysh:p%d", i))
			}
		}, "at most 100 providers"},
	}
	for _, c := range cases {
		p := usdcPass(now)
		c.mutate(&p)
		_, err := p.Normalize(now, cats)
		if err == nil {
			t.Errorf("%s: expected an error", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %q should mention %q", c.name, err, c.want)
		}
	}
}

func TestNormalizeKeepsWhatIsFine(t *testing.T) {
	now := time.Now()
	p := usdcPass(now)
	p.BudgetMinorUnits = 1_000_000_000_000 // exactly the maximum
	p.Label = "💸 Claude — paid APIs"       // an emoji is a character, not a control one
	p.AllowedMerchants = []string{" Paysh:Birdeye.Data ", "paysh:birdeye.data", "demo:alpha", ""}
	out, err := p.Normalize(now, cats)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(out.AllowedMerchants, []string{"paysh:birdeye.data", "demo:alpha"}) {
		t.Fatalf("providers not cleaned: %v", out.AllowedMerchants)
	}
	// 80 characters is counted as characters, not bytes
	p = usdcPass(now)
	p.Label = strings.Repeat("é", 80)
	if _, err := p.Normalize(now, cats); err != nil {
		t.Fatalf("80 characters should do: %v", err)
	}
	p.Label = strings.Repeat("é", 81)
	if _, err := p.Normalize(now, cats); err == nil {
		t.Fatal("81 characters should not")
	}
}
