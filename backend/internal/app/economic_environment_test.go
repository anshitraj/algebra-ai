package app

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
)

// projectPass adds an agent with a pass in a project and environment to the rig.
func (rig *econRig) projectPass(t *testing.T, agentID, projectID, env string) {
	t.Helper()
	now := time.Now()
	rig.agents.put(&agent.Identity{ID: agentID, UserID: "user_1", Name: agentID, ClientID: "spend-pass:custom"})
	if err := rig.passes.Create(context.Background(), &spendpass.Pass{
		ID: "pass_" + agentID, UserID: "user_1", AgentID: agentID, Label: agentID, AgentKind: spendpass.AgentCustom, Currency: "USDC",
		BudgetMinorUnits: 10_000_000, BudgetPeriod: spendpass.PeriodTotal, CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(24 * time.Hour),
		ProjectID: projectID, Environment: env,
	}); err != nil {
		t.Fatal(err)
	}
}

func denied(t *testing.T, err error, code string) {
	t.Helper()
	var d *AuthorityDenied
	if !errors.As(err, &d) || !slices.Equal(d.ReasonCodes, []string{code}) {
		t.Fatalf("err = %v, want authority denied with %s", err, code)
	}
}

func TestEconomic_AProjectPassPaysOnlyInItsEnvironment(t *testing.T) {
	ctx := context.Background()
	rig := newEconRig(t, 0, 0)
	rig.projectPass(t, "sbx", "proj_a", "sandbox")
	rig.projectPass(t, "dev", "proj_a", "devnet")
	rig.projectPass(t, "main", "proj_a", "mainnet")

	v, _, err := rig.svc.CreateIntent(ctx, "sbx", networkSpec("e-1"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(v.Constraints.AllowedNetworks, []string{"sandbox"}) || v.ProjectID != "proj_a" {
		t.Errorf("sandbox pass: networks %v project %q", v.Constraints.AllowedNetworks, v.ProjectID)
	}
	_, _, err = rig.svc.CreateIntent(ctx, "sbx", networkSpec("e-2", "solana-devnet"))
	denied(t, err, DenyEnvironmentNetwork)

	v, _, err = rig.svc.CreateIntent(ctx, "dev", networkSpec("e-3"))
	if err != nil || !slices.Equal(v.Constraints.AllowedNetworks, []string{"solana-devnet"}) {
		t.Fatalf("devnet pass: %v %v", v, err)
	}
	_, _, err = rig.svc.CreateIntent(ctx, "dev", networkSpec("e-4", "solana"))
	denied(t, err, DenyEnvironmentNetwork)

	// Mainnet: refused until the deployment turns real-money execution on.
	_, _, err = rig.svc.CreateIntent(ctx, "main", networkSpec("e-5"))
	denied(t, err, DenyMainnetDisabled)
	rig.svc.SetMainnetExecution(true)
	if _, _, err := rig.svc.CreateIntent(ctx, "main", networkSpec("e-6")); err != nil {
		t.Fatalf("mainnet enabled: %v", err)
	}
}

func TestEconomic_APassWithoutAnEnvironmentIsUnchanged(t *testing.T) {
	rig := newEconRig(t, 1, 10_000_000)
	v, _, err := rig.svc.CreateIntent(context.Background(), "agent_1", networkSpec("legacy"))
	if err != nil || len(v.Constraints.AllowedNetworks) != 0 || v.ProjectID != "" {
		t.Fatalf("legacy pass: %v %v", v, err)
	}
}

// mainnetRail signs "payments" on mainnet, for the payment-time gate.
type mainnetRail struct{ fakeRail }

func (m *mainnetRail) Name() string { return "x402-solana" }

func (m *mainnetRail) Authorize(ctx context.Context, r *econ.Reservation, req PaymentRequest) (*PaymentAuthority, error) {
	a, err := m.fakeRail.Authorize(ctx, r, req)
	if err == nil {
		a.Evidence.Network, a.Evidence.Rail, a.Evidence.Test = "solana", "x402-solana", false
	}
	return a, err
}

func TestEconomic_NoMainnetPaymentIsSignedUnlessEnabled(t *testing.T) {
	ctx := context.Background()
	rig := newEconRig(t, 1, 10_000_000)
	rail := &mainnetRail{fakeRail: *newFakeRail()}
	rig.svc.RegisterRail(rail)
	in, _, err := rig.svc.CreateIntent(ctx, "agent_1", networkSpec("main-gate"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := rig.svc.Reserve(ctx, "agent_1", in.ID, ReserveRequest{ProviderID: "x402:risk.example", Rail: "x402-solana", QuoteMinor: 3_000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rig.svc.Begin(ctx, "agent_1", in.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	_, err = rig.svc.AuthorizePayment(ctx, "agent_1", in.ID, r.ID, PaymentRequest{})
	denied(t, err, DenyMainnetDisabled)
	v, _ := rig.svc.View(ctx, in.ID, false)
	if v.Reservations[0].Evidence.AuthorityIssued {
		t.Error("no payment authority may be recorded for a refused mainnet payment")
	}
}
