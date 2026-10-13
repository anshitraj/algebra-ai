package app

import (
	"context"
	"strings"
	"testing"

	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/routing"
)

// fakeVaults answers a dry run's question about a pass's vault, by network.
type fakeVaults map[string]VaultState

func (f fakeVaults) VaultStatus(_ context.Context, _ string, network string) VaultState {
	return f[chain.NormalizeNetwork(network)]
}

func devnetCand(provider string, price int64) routing.Candidate {
	c := cand(provider)
	c.Network, c.PriceMinor, c.Asset = chain.SolanaDevnet, price, "USDC"
	return c
}

// Where payments come only from a vault, a pass without one still sees what it
// would buy, refused, with the reason and the way out, before anyone presses
// Pay. The payment itself is refused too (Fund), but only after an approval.
func TestSimulate_APassWithoutAVaultSeesTheCardsRefused(t *testing.T) {
	ctx := context.Background()
	rig := newExecRig(t)
	rig.exec.SetVaults(fakeVaults{chain.SolanaDevnet: {Required: true, Code: DenyOnchainRequired}})

	sim, err := rig.exec.Simulate(ctx, SimulateRequest{AgentID: "agent_1", Spec: riskSpec("needs-vault", "cheapest"),
		Candidates: []routing.Candidate{devnetCand("alpha", 3_000), devnetCand("beta", 2_000)}})
	if err != nil {
		t.Fatal(err)
	}
	if sim.Verdict != VerdictDeny || len(sim.Reasons) != 1 || sim.Reasons[0] != DenyOnchainRequired {
		t.Fatalf("nothing can be paid without a vault: %s %v", sim.Verdict, sim.Reasons)
	}
	if sim.WouldPay != nil {
		t.Fatalf("nobody would be paid: %+v", sim.WouldPay)
	}
	if len(sim.Recommendations) != 3 || len(sim.Compared) != 2 {
		t.Fatalf("the cards stay, so a person sees what a vault would buy: %d recommendations, %d compared", len(sim.Recommendations), len(sim.Compared))
	}
	for _, r := range append(append([]Recommendation{}, sim.Recommendations...), sim.Compared...) {
		if r.Decision != "BLOCK" || len(r.Reasons) != 1 || !strings.HasPrefix(r.Reasons[0], DenyOnchainRequired+":") {
			t.Fatalf("a card without a vault is refused with the vault's reason: %+v", r)
		}
	}
	if len(sim.Candidates) != 2 || sim.Candidates[0].Decision != "BLOCK" {
		t.Fatalf("candidates: %+v", sim.Candidates)
	}
}

// The simulated sandbox has no wallet and no vault: a vault requirement on
// devnet changes nothing there.
func TestSimulate_TheSandboxNeedsNoVault(t *testing.T) {
	ctx := context.Background()
	rig := newExecRig(t)
	rig.exec.SetVaults(fakeVaults{chain.SolanaDevnet: {Required: true, Code: DenyOnchainRequired}})
	sandbox := cand("sim")
	sandbox.PriceMinor, sandbox.Asset = 4_000, "USDC"

	sim, err := rig.exec.Simulate(ctx, SimulateRequest{AgentID: "agent_1", Spec: riskSpec("sandbox-free", "cheapest"),
		Candidates: []routing.Candidate{devnetCand("alpha", 2_000), sandbox}})
	if err != nil {
		t.Fatal(err)
	}
	if sim.WouldPay == nil || sim.WouldPay.Provider != "sim" || sim.Verdict != VerdictAllow {
		t.Fatalf("the sandbox seller is payable and the devnet one is not: %s %+v", sim.Verdict, sim.WouldPay)
	}
	byProvider := map[string]Recommendation{}
	for _, r := range sim.Compared {
		byProvider[r.Provider] = r
	}
	if byProvider["alpha"].Decision != "BLOCK" || byProvider["sim"].Decision != "ALLOW" {
		t.Fatalf("compared: %+v", sim.Compared)
	}
}

// A vault that exists but holds less than the price is refused before the
// approval too, and says so in its own words.
func TestSimulate_AnUnderfundedVaultIsRefusedWithItsReason(t *testing.T) {
	ctx := context.Background()
	rig := newExecRig(t)
	rig.exec.SetVaults(fakeVaults{chain.SolanaDevnet: {Required: true, Funded: true, FundedMinor: 1_000}})

	sim, err := rig.exec.Simulate(ctx, SimulateRequest{AgentID: "agent_1", Spec: riskSpec("underfunded", "cheapest"),
		Candidates: []routing.Candidate{devnetCand("alpha", 3_000)}})
	if err != nil {
		t.Fatal(err)
	}
	if sim.Verdict != VerdictDeny || len(sim.Reasons) != 1 || sim.Reasons[0] != DenyOnchainUnfunded {
		t.Fatalf("a vault holding less than the price: %s %v", sim.Verdict, sim.Reasons)
	}
	// Enough in the vault: the same offer is payable.
	rig.exec.SetVaults(fakeVaults{chain.SolanaDevnet: {Required: true, Funded: true, FundedMinor: 10_000}})
	sim, err = rig.exec.Simulate(ctx, SimulateRequest{AgentID: "agent_1", Spec: riskSpec("funded", "cheapest"),
		Candidates: []routing.Candidate{devnetCand("alpha", 3_000)}})
	if err != nil || sim.Verdict != VerdictAllow || sim.WouldPay == nil {
		t.Fatalf("a funded vault: %v %+v", err, sim)
	}
}

// With no vault service at all (no Solana rail), a dry run behaves as before.
func TestSimulate_WithoutVaultsNothingChanges(t *testing.T) {
	rig := newExecRig(t)
	sim, err := rig.exec.Simulate(context.Background(), SimulateRequest{AgentID: "agent_1", Spec: riskSpec("plain", "cheapest"),
		Candidates: []routing.Candidate{devnetCand("alpha", 3_000)}})
	if err != nil || sim.Verdict != VerdictAllow {
		t.Fatalf("%v %+v", err, sim)
	}
}
