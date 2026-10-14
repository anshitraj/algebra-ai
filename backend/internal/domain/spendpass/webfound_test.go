package spendpass

import "testing"

func TestWebFoundRule(t *testing.T) {
	cases := []struct {
		rule                 WebFoundRule
		foundOnWeb, approved bool
		want                 string
		approvable           bool
	}{
		{"", true, false, ReasonWebFoundNeedsApproval, true}, // the default waits for a person
		{"", false, false, "", false},                        // a listed provider is not a web find
		{WebFoundApprove, true, true, "", false},
		{WebFoundAllow, true, false, "", false},
		{WebFoundDeny, true, false, ReasonWebFoundNotAllowed, false},
		{WebFoundDeny, true, true, ReasonWebFoundNotAllowed, false}, // a yes doesn't lift a deny
		{WebFoundDeny, false, false, "", false},
	}
	for _, c := range cases {
		got, approvable := Controls{WebFoundProviders: c.rule}.WebFound(c.foundOnWeb, c.approved)
		if got != c.want || approvable != c.approvable {
			t.Errorf("%q web=%v approved=%v: %q %v, want %q %v", c.rule, c.foundOnWeb, c.approved, got, approvable, c.want, c.approvable)
		}
	}
	if _, err := (Controls{WebFoundProviders: "sometimes"}).Normalize("USDC"); err == nil {
		t.Error("an unknown rule is refused")
	}
	if c, err := (Controls{}).Normalize("USDC"); err != nil || c.WebFoundProviders != WebFoundApprove {
		t.Errorf("unset reads as approve: %v %v", c.WebFoundProviders, err)
	}
}
