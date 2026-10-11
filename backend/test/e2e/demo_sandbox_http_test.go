package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/project-algebra/algebra/internal/domain/account"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
	"github.com/project-algebra/algebra/internal/platform/wiring"
)

// A demo sign-in is free and open to anyone, so its agent pays in the sandbox
// and nowhere else, whatever it asks for. These drive the real API over HTTP
// with a demo account's Spend Pass.

// demoPassToken starts a demo account, as the sign-in page's one-click button
// does, gives it a USDC pass, and returns the pass's token.
func demoPassToken(t *testing.T, b *wiring.Bundle) string {
	t.Helper()
	if b.Demo == nil {
		t.Skip("demo accounts are off")
	}
	ctx := context.Background()
	res, err := b.Demo.Start(ctx, account.ClientMeta{IP: "198.51.100.7", UserAgent: "e2e"})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := b.SpendPasses.Create(ctx, res.User.ID, spendpass.Pass{
		Label: "demo", AgentKind: spendpass.AgentCustom, Currency: "USDC", BudgetMinorUnits: 1_000_000, BudgetPeriod: spendpass.PeriodTotal,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	return issued.Token
}

func TestDemoAccountOverHTTP_PaysInTheSandboxOnly(t *testing.T) {
	b, base := serve(t)
	token := demoPassToken(t, b)

	// What the agent asks for decides nothing: a real network is refused before
	// anything is reserved.
	for _, network := range []string{"solana-devnet", "solana"} {
		req := priceRequest("e2e-demo-" + network + "-" + uuid.NewString())
		req["constraints"] = map[string]any{"allowed_networks": []string{network}}
		status, _, out := call(t, "POST", base+"/api/v1/execute", token, req)
		if status != 403 {
			t.Fatalf("asking a demo account to pay on %s: %d %v, want 403", network, status, out)
		}
		if codes, _ := out["reason_codes"].([]any); len(codes) != 1 || codes[0] != "DEMO_SANDBOX_ONLY" {
			t.Errorf("reason codes on %s: %v, want [DEMO_SANDBOX_ONLY]", network, out["reason_codes"])
		}
	}

	// Naming no network, it runs in the sandbox, and the intent says so.
	status, _, out := call(t, "POST", base+"/api/v1/execute", token, priceRequest("e2e-demo-"+uuid.NewString()))
	if status != 200 || out["delivered"] != true {
		t.Fatalf("a demo account's routed call: %d %v", status, out)
	}
	if got := deliveredBy(out); !strings.HasPrefix(got, "sandbox:") {
		t.Errorf("paid %q, want a sandbox provider", got)
	}
	intent, _ := out["intent"].(map[string]any)
	constraints, _ := intent["constraints"].(map[string]any)
	if nets, _ := constraints["allowed_networks"].([]any); len(nets) != 1 || nets[0] != "sandbox" {
		t.Errorf("the intent's allowed networks = %v, want [sandbox]", constraints["allowed_networks"])
	}

	// The wallets behind the real rails are not shown to it.
	status, _, rails := call(t, "GET", base+"/api/v1/rails", token, nil)
	if status != 200 {
		t.Fatalf("rails: %d %v", status, rails)
	}
	if list, _ := rails["rails"].([]any); len(list) != 0 {
		t.Errorf("a demo account must see no rails, got %v", list)
	}
	if rails["sandbox"] != true {
		t.Errorf("the sandbox is on, got %v", rails["sandbox"])
	}
}

func TestLiveAccountOverHTTP_KeepsItsRailsAndItsChoiceOfNetwork(t *testing.T) {
	b, base := serve(t)
	token := issuePass(t, b)

	status, _, rails := call(t, "GET", base+"/api/v1/rails", token, nil)
	if status != 200 {
		t.Fatalf("rails: %d %v", status, rails)
	}
	if list, _ := rails["rails"].([]any); len(list) != 2 {
		t.Errorf("a live account sees both clusters (configured or not), got %v", list)
	}

	// A live account may name the sandbox, as before.
	req := priceRequest("e2e-live-" + uuid.NewString())
	req["constraints"] = map[string]any{"allowed_networks": []string{"sandbox"}}
	if status, _, out := call(t, "POST", base+"/api/v1/execute", token, req); status != 200 || out["delivered"] != true {
		t.Fatalf("a live account's sandbox call: %d %v", status, out)
	}
}
