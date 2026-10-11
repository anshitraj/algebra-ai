package app

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/project-algebra/algebra/internal/domain/account"
	"github.com/project-algebra/algebra/internal/domain/econ"
)

// networkSpec is an intent for the rig's capability that names the networks it may pay on.
// The window tells intents apart: the same outcome twice is one intent.
func networkSpec(window string, networks ...string) econ.Spec {
	return econ.Spec{
		Capability: "solana.token-risk", Input: json.RawMessage(`{"mint":"SOL"}`), Window: window, Currency: "USDC", BudgetMaxMinor: 50_000,
		Constraints: econ.Constraints{AllowedNetworks: networks},
	}
}

func TestEconomic_DemoAccountPaysInTheSandboxOnly(t *testing.T) {
	ctx := context.Background()
	rig := newEconRig(t, 1, 10_000_000)
	rig.svc.SetAccountModes(fakeModes{"user_1": account.ModeDemo})

	t.Run("an intent that names no network is limited to the sandbox", func(t *testing.T) {
		v, _, err := rig.svc.CreateIntent(ctx, "agent_1", networkSpec("w-none"))
		if err != nil {
			t.Fatal(err)
		}
		if got := v.Constraints.AllowedNetworks; !slices.Equal(got, []string{"sandbox"}) {
			t.Errorf("allowed networks = %v, want [sandbox]", got)
		}
	})
	t.Run("naming the sandbox is fine", func(t *testing.T) {
		v, _, err := rig.svc.CreateIntent(ctx, "agent_1", networkSpec("w-sandbox", "sandbox"))
		if err != nil {
			t.Fatal(err)
		}
		if got := v.Constraints.AllowedNetworks; !slices.Equal(got, []string{"sandbox"}) {
			t.Errorf("allowed networks = %v, want [sandbox]", got)
		}
	})
	for name, networks := range map[string][]string{
		"devnet":                      {"solana-devnet"},
		"mainnet":                     {"solana"},
		"the sandbox and a real one":  {"sandbox", "solana"},
		"devnet spelled as a CAIP-2":  {"solana:EtWTRABZaYq6iMfeYKouRu166VU2xqa1"},
		"mainnet spelled as a CAIP-2": {"solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp"},
	} {
		t.Run("asking for "+name+" is refused", func(t *testing.T) {
			_, _, err := rig.svc.CreateIntent(ctx, "agent_1", networkSpec("w-"+name, networks...))
			var denied *AuthorityDenied
			if !errors.As(err, &denied) || !slices.Equal(denied.ReasonCodes, []string{DenyDemoSandboxOnly}) {
				t.Fatalf("err = %v, want authority denied with %s", err, DenyDemoSandboxOnly)
			}
		})
	}
	t.Run("the person's own route is held to it too", func(t *testing.T) {
		_, _, err := rig.svc.CreateIntentForPass(ctx, "user_1", "pass_agent_1", networkSpec("w-console", "solana-devnet"))
		var denied *AuthorityDenied
		if !errors.As(err, &denied) {
			t.Fatalf("err = %v, want authority denied", err)
		}
	})
}

func TestEconomic_LiveAccountKeepsItsChoiceOfNetwork(t *testing.T) {
	ctx := context.Background()
	rig := newEconRig(t, 1, 10_000_000)
	rig.svc.SetAccountModes(fakeModes{"user_1": account.ModeLive})

	v, _, err := rig.svc.CreateIntent(ctx, "agent_1", networkSpec("w-live"))
	if err != nil {
		t.Fatal(err)
	}
	if got := v.Constraints.AllowedNetworks; len(got) != 0 {
		t.Errorf("a live account's intent that names no network must stay open, got %v", got)
	}
	v, _, err = rig.svc.CreateIntent(ctx, "agent_1", networkSpec("w-live-devnet", "solana-devnet"))
	if err != nil {
		t.Fatal(err)
	}
	if got := v.Constraints.AllowedNetworks; !slices.Equal(got, []string{"solana-devnet"}) {
		t.Errorf("allowed networks = %v, want [solana-devnet]", got)
	}
}

func TestEconomic_AnAccountWhoseModeCantBeReadIsNotTreatedAsLive(t *testing.T) {
	rig := newEconRig(t, 1, 10_000_000)
	rig.svc.SetAccountModes(fakeModes{}) // knows no one
	if _, _, err := rig.svc.CreateIntent(context.Background(), "agent_1", networkSpec("w-unknown", "solana")); err == nil {
		t.Fatal("an intent for an account whose mode can't be read must be refused, not allowed through as a live one")
	}
	if got, _ := rig.svc.List(context.Background(), "user_1", 10); len(got) != 0 {
		t.Errorf("nothing may be created, got %d intents", len(got))
	}
}

// The fake rail signs payments on the sandbox network.
func TestEconomic_PaymentOnAnExcludedNetworkIsNeverReleased(t *testing.T) {
	ctx := context.Background()
	rig := newEconRig(t, 1, 10_000_000)

	run := func(t *testing.T, window string, networks ...string) (*IntentView, error) {
		t.Helper()
		in, _, err := rig.svc.CreateIntent(ctx, "agent_1", networkSpec(window, networks...))
		if err != nil {
			t.Fatal(err)
		}
		r, err := rig.svc.Reserve(ctx, "agent_1", in.ID, reserveReq)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = rig.svc.Begin(ctx, "agent_1", in.ID, r.ID); err != nil {
			t.Fatal(err)
		}
		_, err = rig.svc.AuthorizePayment(ctx, "agent_1", in.ID, r.ID, PaymentRequest{})
		v, verr := rig.svc.View(ctx, in.ID, false)
		if verr != nil {
			t.Fatal(verr)
		}
		return v, err
	}

	t.Run("an intent limited to devnet gets nothing signed on the sandbox", func(t *testing.T) {
		v, err := run(t, "w-devnet", "solana-devnet")
		var denied *AuthorityDenied
		if !errors.As(err, &denied) || !slices.Equal(denied.ReasonCodes, []string{DenyNetworkNotAllowed}) {
			t.Fatalf("err = %v, want authority denied with %s", err, DenyNetworkNotAllowed)
		}
		if v.Reservations[0].Evidence.AuthorityIssued {
			t.Error("the attempt must not be recorded as having received payment authority")
		}
	})
	t.Run("an intent that allows the network is paid", func(t *testing.T) {
		v, err := run(t, "w-sandbox", "sandbox")
		if err != nil {
			t.Fatal(err)
		}
		if !v.Reservations[0].Evidence.AuthorityIssued {
			t.Error("payment authority should have been released")
		}
	})
	t.Run("an intent with no network limit is paid", func(t *testing.T) {
		if _, err := run(t, "w-open"); err != nil {
			t.Fatal(err)
		}
	})
}
