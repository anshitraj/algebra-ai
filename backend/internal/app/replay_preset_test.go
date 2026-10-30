package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
)

// presetSellers are three sellers of the kinds of work a preset run tests, on
// the network a Testnet pass pays on, and nobody for web search.
func presetSellers(network string) CandidateSource {
	priced := func(capability, provider string, price int64) routing.Candidate {
		c := cand(provider)
		c.Capability, c.Network, c.PriceMinor, c.Asset = capability, network, price, "USDC"
		return c
	}
	return func(_ context.Context, capability string) []routing.Candidate {
		switch capability {
		case "token.price":
			return []routing.Candidate{priced(capability, "alpha", 3_000), priced(capability, "beta", 2_000)}
		case "solana.token-risk":
			return []routing.Candidate{priced(capability, "gamma", 5_000)}
		}
		return nil
	}
}

func presetPass(t *testing.T, rig *execRig) *spendpass.Pass {
	t.Helper()
	p, err := rig.passes.Get(context.Background(), "pass_agent_1")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// A Spend Pass with no task behind it is tested on the kinds of work agents
// usually buy: each at its cheapest listed price, six scenarios each, and
// nothing is contacted or paid.
func TestReplayPreset_APassWithNoTaskIsTestedOnWhatAgentsBuy(t *testing.T) {
	ctx := context.Background()
	rig := newExecRig(t)
	pass := presetPass(t, rig)
	paidBefore := rig.rail.authorized.Load()

	rep, err := rig.exec.ReplayPreset(ctx, pass, presetSellers("solana-devnet"))
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Preset || rep.PassID != pass.ID || rep.JobID != "" || rep.Label != pass.Label {
		t.Errorf("a preset run is of the pass, not of a task: %+v", rep)
	}
	if len(rep.Purchases) != 2 {
		t.Fatalf("token price and token risk have sellers, web search has none: %+v", rep.Purchases)
	}
	price, risk := rep.Purchases[0], rep.Purchases[1]
	if price.Capability != "token.price" || price.Title != "Token price" || price.Provider != "beta" || price.PaidMinor != 2_000 || price.IntentID != "" {
		t.Errorf("the cheapest seller stands in for the one the agent paid: %+v", price)
	}
	if risk.Capability != "solana.token-risk" || risk.Provider != "gamma" || len(risk.Findings) != 6 || len(price.Findings) != 6 {
		t.Errorf("six scenarios each: %+v", risk)
	}
	if len(rep.Skipped) != 1 || rep.Skipped[0].Capability != "web.search" || rep.Skipped[0].Title != "Web search" || rep.Skipped[0].Reason == "" {
		t.Errorf("a kind of work nobody sells is left out and said so: %+v", rep.Skipped)
	}

	by := map[string]ReplayFinding{}
	for _, f := range price.Findings {
		by[f.Scenario] = f
	}
	if f := by[ChaosOvercharge]; f.Outcome != ReplayCaught || !strings.Contains(f.Detail, RejectPriceAboveListing) || f.AtRiskMinor != 8_000 {
		t.Errorf("overcharge is four times the cheapest listing: %+v", f)
	}
	if f := by[ChaosHoneypot]; f.Outcome != ReplayCaught || f.AtRiskMinor != honeypotMinor {
		t.Errorf("25 USDC is more than this pass's whole budget: %+v", f)
	}
	if f := by[ChaosWebLure]; f.Outcome != ReplayHeld {
		t.Errorf("a web find waits for a yes by default: %+v", f)
	}
	if f := by[ChaosSellerDown]; f.Outcome != ReplayFallsBack || !strings.Contains(f.Detail, "alpha") || strings.Contains(f.Title, "task") {
		t.Errorf("with the cheapest down it falls back to the next seller, and no task is mentioned: %+v", f)
	}
	if f := by[ChaosLoop]; !f.Safe {
		t.Errorf("loop: %+v", f)
	}
	if rep.Safe+rep.Unsafe != 12 || rep.Unsafe != 0 || rep.ProtectedMinor == 0 || rep.AtRiskMinor < rep.ProtectedMinor {
		t.Errorf("totals over both purchases: %+v", rep)
	}
	if rig.rail.authorized.Load() != paidBefore {
		t.Errorf("a preset run pays nothing: authorized=%d", rig.rail.authorized.Load())
	}
	for _, p := range []string{"alpha", "beta", "gamma"} {
		if rig.runner.ran(p) != 0 || rig.runner.quoted(p) != 0 {
			t.Errorf("a preset run contacts nobody: %s ran=%d quoted=%d", p, rig.runner.ran(p), rig.runner.quoted(p))
		}
	}
}

// The same pass gets the same report: the cheapest seller of a tie is the
// first by name, not whichever the map gave first.
func TestReplayPreset_TwoRunsPickTheSameSeller(t *testing.T) {
	rig := newExecRig(t)
	pass := presetPass(t, rig)
	tied := func(_ context.Context, capability string) []routing.Candidate {
		var out []routing.Candidate
		for _, p := range []string{"zeta", "alpha", "mid"} {
			c := cand(p)
			c.Capability, c.Network, c.PriceMinor, c.Asset = capability, "solana-devnet", 1_000, "USDC"
			out = append(out, c)
		}
		return out
	}
	for i := 0; i < 5; i++ {
		rep, err := rig.exec.ReplayPreset(context.Background(), pass, tied)
		if err != nil {
			t.Fatal(err)
		}
		if len(rep.Purchases) != 3 || rep.Purchases[0].Provider != "alpha" {
			t.Fatalf("run %d: %+v", i, rep.Purchases)
		}
	}
}

// The ask is the widest the pass allows one purchase, so a rule on the pass is
// what the run measures: a per-purchase limit stops a seller asking more.
func TestReplayPreset_TheAskIsTheWidestThePassAllows(t *testing.T) {
	cap := int64(50_000)
	zero := int64(0)
	for name, tc := range map[string]struct {
		pass spendpass.Pass
		want int64
	}{
		"per-purchase limit": {spendpass.Pass{BudgetMinorUnits: 10_000_000, MaxPerPurchaseMinorUnits: &cap}, 50_000},
		"whole budget":       {spendpass.Pass{BudgetMinorUnits: 10_000_000}, 10_000_000},
		"a zero limit":       {spendpass.Pass{BudgetMinorUnits: 7_000, MaxPerPurchaseMinorUnits: &zero}, 7_000},
		"neither":            {spendpass.Pass{}, 1_000_000},
	} {
		if got := presetBudgetMinor(&tc.pass); got != tc.want {
			t.Errorf("%s: %d, want %d", name, got, tc.want)
		}
	}

	ctx := context.Background()
	rig := newExecRig(t)
	rig.passes.passes["pass_agent_1"].MaxPerPurchaseMinorUnits = &cap
	rep, err := rig.exec.ReplayPreset(ctx, presetPass(t, rig), presetSellers("solana-devnet"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range rep.Purchases {
		for _, f := range p.Findings {
			if f.Scenario == ChaosHoneypot && f.Outcome != ReplayCaught {
				t.Errorf("25 USDC is over a 0.05 USDC per-purchase limit: %+v", f)
			}
		}
	}
}

// A pass is tested on the networks it can pay on: a sandbox pass has nothing to
// buy from a Testnet seller, and the run says what it skipped.
func TestReplayPreset_OnlySellersThePassCanPayCount(t *testing.T) {
	ctx := context.Background()
	rig := newExecRig(t)
	rig.passes.passes["pass_agent_1"].Environment = "sandbox"

	rep, err := rig.exec.ReplayPreset(ctx, presetPass(t, rig), presetSellers("solana-devnet"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Purchases) != 0 || len(rep.Skipped) != 3 || rep.Safe != 0 || rep.Unsafe != 0 {
		t.Fatalf("a Testnet seller isn't one a sandbox pass can pay: %+v", rep)
	}
	if !strings.Contains(rep.Skipped[0].Reason, "sandbox") {
		t.Errorf("the reason names the pass's networks: %q", rep.Skipped[0].Reason)
	}

	rep, err = rig.exec.ReplayPreset(ctx, presetPass(t, rig), presetSellers("sandbox"))
	if err != nil || len(rep.Purchases) != 2 {
		t.Fatalf("sandbox sellers: %+v %v", rep, err)
	}
}

// What can't be tested is said, in words a person can act on, instead of
// returning a report where every scenario was refused for the same reason.
func TestReplayPreset_RefusesWhatCannotBeTested(t *testing.T) {
	ctx := context.Background()
	rig := newExecRig(t)
	src := presetSellers("solana-devnet")
	now := time.Now()

	shopping := presetPass(t, rig)
	shopping.Currency = "INR"
	frozen := presetPass(t, rig)
	frozen.FrozenAt = &now
	revoked := presetPass(t, rig)
	revoked.RevokedAt = &now
	mainnet := presetPass(t, rig)
	mainnet.Environment = "mainnet"
	rig.passes.passes["pass_agent_1"].Environment = "mainnet"

	for name, tc := range map[string]struct {
		pass *spendpass.Pass
		want string
	}{
		"shopping": {shopping, "USDC"},
		"frozen":   {frozen, "unfreeze"},
		"revoked":  {revoked, "revoked"},
		"mainnet":  {mainnet, "Mainnet payments are off"},
	} {
		_, err := rig.exec.ReplayPreset(ctx, tc.pass, src)
		if !errors.Is(err, ErrInvalidRequest) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
