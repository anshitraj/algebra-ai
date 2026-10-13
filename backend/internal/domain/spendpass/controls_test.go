package spendpass

import "testing"

func TestUnverifiedRule(t *testing.T) {
	cases := []struct {
		rule               UnverifiedRule
		verified, approved bool
		want               string
		approvable         bool
	}{
		{"", false, false, "", false}, // the default allows: verification is shown, not required
		{UnverifiedAllow, false, false, "", false},
		{UnverifiedApprove, true, false, "", false},
		{UnverifiedApprove, false, false, ReasonUnverifiedNeedsApproval, true},
		{UnverifiedApprove, false, true, "", false},
		{UnverifiedDeny, true, false, "", false},
		{UnverifiedDeny, false, false, ReasonUnverifiedNotAllowed, false},
		{UnverifiedDeny, false, true, ReasonUnverifiedNotAllowed, false}, // a yes doesn't lift a deny
	}
	for _, c := range cases {
		got, approvable := Controls{UnverifiedProviders: c.rule}.Unverified(c.verified, c.approved)
		if got != c.want || approvable != c.approvable {
			t.Errorf("%q verified=%v approved=%v: %q %v, want %q %v", c.rule, c.verified, c.approved, got, approvable, c.want, c.approvable)
		}
	}
	if _, err := (Controls{UnverifiedProviders: "sometimes"}).Normalize("USDC"); err == nil {
		t.Error("an unknown rule is refused")
	}
	if c, err := (Controls{}).Normalize("USDC"); err != nil || c.UnverifiedProviders != UnverifiedAllow {
		t.Errorf("unset reads as allow: %v %v", c.UnverifiedProviders, err)
	}
}
