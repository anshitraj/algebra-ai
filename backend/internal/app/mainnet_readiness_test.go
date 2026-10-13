package app

import "testing"

func gateOf(t *testing.T, r MainnetReadiness, id string) MainnetGate {
	t.Helper()
	for _, g := range r.Gates {
		if g.ID == id {
			return g
		}
	}
	t.Fatalf("no gate %q", id)
	return MainnetGate{}
}

func TestMainnetReadiness_NothingConfiguredCanOnlyRead(t *testing.T) {
	r := AssessMainnet(MainnetFacts{})
	if r.CanPay || r.ReadyForCustomerFunds || len(r.WorksToday) == 0 {
		t.Fatalf("a server with no mainnet set-up reads and never pays: %+v", r)
	}
	for _, id := range []string{"rail", "program", "passes_required", "upgrade_authority", "execution"} {
		if g := gateOf(t, r, id); g.Passed || !g.Blocking || g.Detail == "" {
			t.Errorf("%s must fail, block and say why: %+v", id, g)
		}
	}
}

// The signer gate is the operator's to settle, and a Cloud KMS payer settles it.
func TestMainnetReadiness_ACloudKMSPayerPassesTheSignerGate(t *testing.T) {
	r := AssessMainnet(MainnetFacts{})
	if g := gateOf(t, r, "signer"); g.Passed || g.Blocking || g.Detail == "" {
		t.Fatalf("a key on the server fails the signer gate, without blocking, and says why: %+v", g)
	}
	r = AssessMainnet(MainnetFacts{SignerExternal: true})
	if g := gateOf(t, r, "signer"); !g.Passed || g.Detail != "" {
		t.Fatalf("a KMS payer passes: %+v", g)
	}
	// Still not ready for customers' funds: nobody has reviewed the program.
	if AssessMainnet(MainnetFacts{SignerExternal: true}).ReadyForCustomerFunds {
		t.Fatal("a KMS payer alone does not make mainnet ready")
	}
}

func TestMainnetReadiness_EveryBlockingGateIsNeededToPay(t *testing.T) {
	const payer, multisig = "GhpmTWT8ng3viaQtFNDF7T7RyrmemosWvEDetKmBEoPA", "SQDS4ep65T869zMMBKyuUq6aD6EgTu8psMjkvj52pCf"
	all := MainnetFacts{ExecutionEnabled: true, RailConfigured: true, Payer: payer, ProgramDeployed: true, PassesRequired: true,
		AuthorityKnown: true, UpgradeAuthority: multisig}
	r := AssessMainnet(all)
	if !r.CanPay {
		t.Fatalf("with every blocking gate passed the server can pay: %+v", r)
	}
	// What code cannot check keeps it short of ready, whatever is configured.
	if r.ReadyForCustomerFunds || gateOf(t, r, "review").Passed || gateOf(t, r, "signer").Passed {
		t.Fatalf("no configuration makes an unreviewed program ready for customers' funds: %+v", r)
	}

	for name, breakIt := range map[string]func(*MainnetFacts){
		"execution":         func(f *MainnetFacts) { f.ExecutionEnabled = false },
		"rail":              func(f *MainnetFacts) { f.RailConfigured = false },
		"program":           func(f *MainnetFacts) { f.ProgramDeployed = false },
		"passes_required":   func(f *MainnetFacts) { f.PassesRequired = false },
		"upgrade_authority": func(f *MainnetFacts) { f.UpgradeAuthority = payer },
	} {
		f := all
		breakIt(&f)
		r := AssessMainnet(f)
		if r.CanPay || gateOf(t, r, name).Passed {
			t.Errorf("%s: failing it must stop payment: %+v", name, gateOf(t, r, name))
		}
	}

	// An authority that could not be read is not assumed safe; an immutable program is.
	f := all
	f.AuthorityKnown = false
	if AssessMainnet(f).CanPay {
		t.Error("an upgrade authority that couldn't be read must not pass")
	}
	f = all
	f.Immutable, f.UpgradeAuthority = true, ""
	if !gateOf(t, AssessMainnet(f), "upgrade_authority").Passed {
		t.Error("an immutable program has no authority to take")
	}
}
