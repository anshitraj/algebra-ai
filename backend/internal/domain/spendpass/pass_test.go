package spendpass

import (
	"slices"
	"testing"
	"time"

	"github.com/project-algebra/algebra/policy"
)

var cats = []string{"groceries", "electronics", "alcohol"}

func i64(v int64) *int64 { return &v }

func basePass(now time.Time) Pass {
	return Pass{
		ID: "pass_1", Label: "Claude — groceries", AgentKind: AgentClaude, Currency: "INR",
		BudgetMinorUnits: 200000, BudgetPeriod: PeriodWeek,
		MaxPerPurchaseMinorUnits: i64(80000), ApproveAboveMinorUnits: i64(50000),
		AllowedCategories: []string{"groceries"}, CreatedAt: now.Add(-24 * time.Hour), ExpiresAt: now.Add(30 * 24 * time.Hour),
	}
}

func TestNormalize(t *testing.T) {
	now := time.Now()
	good := basePass(now)
	good.AllowedCategories = []string{" Groceries ", "groceries"}
	out, err := good.Normalize(now, cats)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(out.AllowedCategories, []string{"groceries"}) {
		t.Fatalf("categories not cleaned: %v", out.AllowedCategories)
	}
	bad := []func(p *Pass){
		func(p *Pass) { p.Label = "" },
		func(p *Pass) { p.BudgetMinorUnits = 0 },
		func(p *Pass) { p.BudgetPeriod = "fortnight" },
		func(p *Pass) { p.MaxPerPurchaseMinorUnits = i64(999999999) },
		func(p *Pass) { p.ApproveAboveMinorUnits = i64(-1) },
		func(p *Pass) { p.AllowedCategories = []string{"weapons"} },
		func(p *Pass) { p.ExpiresAt = now.Add(-time.Minute) },
		func(p *Pass) { p.ExpiresAt = now.Add(400 * 24 * time.Hour) },
		func(p *Pass) { p.AgentKind = "robot" },
	}
	for i, mutate := range bad {
		p := basePass(now)
		mutate(&p)
		if _, err := p.Normalize(now, cats); err == nil {
			t.Errorf("case %d: expected an error", i)
		}
	}
}

func TestEvaluate(t *testing.T) {
	now := time.Now()
	p := basePass(now)
	revoked := basePass(now)
	revoked.RevokedAt = &now
	expired := basePass(now)
	expired.ExpiresAt = now

	cases := []struct {
		name   string
		pass   Pass
		in     Purchase
		want   policy.Decision
		reason string
	}{
		{"small grocery buy", p, Purchase{Category: "groceries", AmountMinor: 20000, Currency: "INR"}, policy.Allow, ReasonOK},
		{"at the ask-me line", p, Purchase{Category: "groceries", AmountMinor: 50000}, policy.RequireApproval, ReasonApprovalRequired},
		{"over per-purchase", p, Purchase{Category: "groceries", AmountMinor: 90000}, policy.Deny, ReasonOverPerPurchase},
		{"over the week's budget", p, Purchase{Category: "groceries", AmountMinor: 30000, SpentInWindow: 180000}, policy.Deny, ReasonOverBudget},
		{"wrong category", p, Purchase{Category: "alcohol", AmountMinor: 1000}, policy.Deny, ReasonCategoryNotAllowed},
		{"no category given", p, Purchase{AmountMinor: 1000}, policy.Deny, ReasonCategoryNotAllowed},
		{"revoked", revoked, Purchase{Category: "groceries", AmountMinor: 1000}, policy.Deny, ReasonRevoked},
		{"expired", expired, Purchase{Category: "groceries", AmountMinor: 1000}, policy.Deny, ReasonExpired},
		{"other currency", p, Purchase{Category: "groceries", AmountMinor: 1000, Currency: "USD"}, policy.Deny, ReasonCurrencyMismatch},
	}
	for _, c := range cases {
		d := c.pass.Evaluate(now, c.in)
		if d.Decision != c.want || !slices.Contains(d.ReasonCodes, c.reason) {
			t.Errorf("%s: got %s %v, want %s %s", c.name, d.Decision, d.ReasonCodes, c.want, c.reason)
		}
	}

	merchants := basePass(now)
	merchants.AllowedMerchants = []string{"swiggy_instamart"}
	if d := merchants.Evaluate(now, Purchase{Merchant: "amazon", Category: "groceries", AmountMinor: 100}); d.Decision != policy.Deny {
		t.Errorf("merchant outside the pass must be denied, got %s", d.Decision)
	}
}

func TestCombineTakesTheStricter(t *testing.T) {
	allow := &policy.PolicyDecision{Decision: policy.Allow, ReasonCodes: []string{"AMOUNT_OK"}, PolicyVersion: "v1"}
	ask := &policy.PolicyDecision{Decision: policy.RequireApproval, ReasonCodes: []string{ReasonApprovalRequired}}
	deny := &policy.PolicyDecision{Decision: policy.Deny, ReasonCodes: []string{"CATEGORY_BLOCKED"}, PolicyVersion: "v1"}

	if got := Combine(allow, ask, "p1"); got.Decision != policy.RequireApproval || len(got.ReasonCodes) != 2 || got.PolicyVersion != "v1+pass:p1" {
		t.Fatalf("pass can tighten ALLOW to REQUIRE_APPROVAL: %+v", got)
	}
	if got := Combine(deny, &policy.PolicyDecision{Decision: policy.Allow, ReasonCodes: []string{ReasonOK}}, "p1"); got.Decision != policy.Deny {
		t.Fatalf("a pass can never loosen a guardrail DENY: %+v", got)
	}
}

func TestWindowStart(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	p := basePass(now)
	p.CreatedAt = now.Add(-60 * 24 * time.Hour)
	if got := p.WindowStart(now); !got.Equal(now.Add(-7 * 24 * time.Hour)) {
		t.Errorf("week window = %v", got)
	}
	p.BudgetPeriod = PeriodTotal
	if got := p.WindowStart(now); !got.Equal(p.CreatedAt) {
		t.Errorf("total window = %v", got)
	}
	p.BudgetPeriod = PeriodMonth
	p.CreatedAt = now.Add(-2 * 24 * time.Hour)
	if got := p.WindowStart(now); !got.Equal(p.CreatedAt) {
		t.Errorf("a young pass's window starts at creation, got %v", got)
	}
}
