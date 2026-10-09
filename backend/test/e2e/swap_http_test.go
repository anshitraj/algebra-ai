package e2e

import (
	"context"
	"testing"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
	"github.com/project-algebra/algebra/internal/platform/safehttp"
	"github.com/project-algebra/algebra/internal/platform/solana"
	"github.com/project-algebra/algebra/internal/platform/wiring"
	"github.com/project-algebra/algebra/providers/jupiter"
	"github.com/project-algebra/algebra/providers/jupiter/jupitertest"
)

var boughtMint = solana.MustPublicKey("DezXAZ8z7PnrnRJjz3wXBoRgixCa6xjnB7YaB1pPB263")

// withJupiter wires swaps into a running API the way the server does, against a
// Jupiter and a Solana node that share one chain: the wallet holds 10 USDC.
func withJupiter(t *testing.T, b *wiring.Bundle) (*jupitertest.Harness, solana.PublicKey) {
	t.Helper()
	wallet, err := solana.NewKeypair()
	if err != nil {
		t.Fatal(err)
	}
	h := jupitertest.New(t, wallet.PublicKey(), boughtMint, 10_000_000)
	rail, err := jupiter.NewRail(jupiter.RailConfig{RPC: solana.NewRPC(h.RPCURL, nil), Signer: wallet})
	if err != nil {
		t.Fatal(err)
	}
	b.Economic.RegisterRail(rail)
	b.Execution.RegisterRunner(jupiter.NewRunner(jupiter.RunnerConfig{
		Client: jupiter.NewClient(h.APIURL, "", safehttp.New(safehttp.Options{AllowLoopback: true})), Wallet: wallet.PublicKey(),
	}))
	caps, err := app.NewStaticCatalog(append(app.DefaultCapabilities(), jupiter.CapabilityInfo())...)
	if err != nil {
		t.Fatal(err)
	}
	b.Execution.SetCapabilities(caps)
	c, err := jupiter.Candidate()
	if err != nil {
		t.Fatal(err)
	}
	b.Candidates.Configured[c.Provider] = append(b.Candidates.Configured[c.Provider], c)
	return h, wallet.PublicKey()
}

func swapRequest(window, amount string) map[string]any {
	return map[string]any{
		"capability": "solana.swap", "input": map[string]any{"output_mint": boughtMint.String(), "amount_usdc": amount},
		"budget_max_minor": 3_000_000, "window": window,
	}
}

// An agent asks to buy a token with USDC. Algebra prices it, checks the pass,
// simulates the transaction Jupiter built and only then signs it, has Jupiter
// land it, and commits what the chain shows left the wallet.
func TestSwapOverHTTP_ABuyIsRoutedSimulatedSignedAndSettledFromTheChain(t *testing.T) {
	b, base := serve(t)
	h, _ := withJupiter(t, b)
	token, userID, passID := issuePassWith(t, b, 10_000_000)
	// The person has said strangers may be paid; the default is a 5 cent cap.
	if _, err := b.SpendPasses.UpdateControls(context.Background(), userID, passID, spendpass.Controls{NewProviders: spendpass.NewProvidersAllow}); err != nil {
		t.Fatal(err)
	}

	req := swapRequest("swap-1", "2.5")
	status, _, first := call(t, "POST", base+"/api/v1/execute", token, req)
	if status != 200 || first["delivered"] != true || first["receipt"] == nil {
		t.Fatalf("swap: %d %v", status, first)
	}
	intent := first["intent"].(map[string]any)
	if intent["committed_minor"] != float64(2_500_000) || intent["state"] != "COMMITTED" {
		t.Errorf("what left the wallet is what was committed: %v", intent)
	}
	resp, _ := first["response"].(map[string]any)
	if resp == nil || resp["status"] != "success" || resp["output_mint"] != boughtMint.String() || resp["signature"] == nil {
		t.Errorf("the answer says what happened: %v", first["response"])
	}
	routing, _ := first["routing"].(map[string]any)
	offers, _ := routing["offers"].([]any)
	if len(offers) != 1 || offers[0].(map[string]any)["provider"] != "jupiter" {
		t.Errorf("routed to Jupiter: %v", routing)
	}

	// The chain agrees: 2.5 USDC gone, 1 token in, one swap landed, one simulation first.
	if h.USDCBalance() != 7_500_000 || h.BoughtBalance() != 1_000_000 {
		t.Errorf("the wallet: %d USDC, %d tokens", h.USDCBalance(), h.BoughtBalance())
	}
	if orders, sims, landed := h.Counts(); landed != 1 || sims < 1 || orders < 1 {
		t.Errorf("one landed after being simulated: orders %d, simulations %d, landed %d", orders, sims, landed)
	}

	// Asking again returns the kept answer and swaps nothing more.
	status, _, again := call(t, "POST", base+"/api/v1/execute", token, req)
	if status != 200 || again["replayed"] != true {
		t.Fatalf("repeat: %d %v", status, again)
	}
	if _, _, landed := h.Counts(); landed != 1 || h.USDCBalance() != 7_500_000 {
		t.Errorf("nothing was swapped twice: landed %d, %d USDC", landed, h.USDCBalance())
	}
}

// The first thing an agent buys from a provider the person has never paid waits
// for the person when it is more than a few cents: the default for every pass.
func TestSwapOverHTTP_AFirstSwapWaitsForThePersonByDefault(t *testing.T) {
	b, base := serve(t)
	h, _ := withJupiter(t, b)
	token, _, _ := issuePassWith(t, b, 10_000_000)

	status, _, out := call(t, "POST", base+"/api/v1/execute", token, swapRequest("swap-gated", "2.5"))
	if status == 200 && out["delivered"] == true {
		t.Fatalf("a stranger over the cap is not paid on the agent's word: %v", out)
	}
	if _, _, landed := h.Counts(); landed != 0 || h.USDCBalance() != 10_000_000 {
		t.Errorf("nothing moved: landed %d, %d USDC", landed, h.USDCBalance())
	}
}

// A Jupiter that builds a transaction doing more than it says gets no signature,
// and no money moves, however the request was phrased.
func TestSwapOverHTTP_ATransactionThatDoesMoreThanItSaysIsNeverSigned(t *testing.T) {
	for name, mode := range map[string]jupitertest.Behavior{
		"spends three times the amount":     jupitertest.Drain,
		"approves a stranger to spend more": jupitertest.Delegate,
		"delivers 10% less than it quoted":  jupitertest.Underdeliver,
		"would fail on chain":               jupitertest.Fail,
	} {
		b, base := serve(t)
		h, _ := withJupiter(t, b)
		h.Mode = mode
		token, userID, passID := issuePassWith(t, b, 10_000_000)
		if _, err := b.SpendPasses.UpdateControls(context.Background(), userID, passID, spendpass.Controls{NewProviders: spendpass.NewProvidersAllow}); err != nil {
			t.Fatal(err)
		}
		status, _, out := call(t, "POST", base+"/api/v1/execute", token, swapRequest("swap-hostile", "2.5"))
		if status == 200 && out["delivered"] == true {
			t.Errorf("%s: must not be delivered: %v", name, out)
		}
		if _, _, landed := h.Counts(); landed != 0 || h.USDCBalance() != 10_000_000 || h.BoughtBalance() != 0 {
			t.Errorf("%s: nothing may move: landed %d, %d USDC, %d tokens", name, landed, h.USDCBalance(), h.BoughtBalance())
		}
		if intent, _ := out["intent"].(map[string]any); intent != nil && intent["committed_minor"] != nil && intent["committed_minor"] != float64(0) {
			t.Errorf("%s: nothing was committed: %v", name, intent)
		}
	}
}

// The rail's own ceiling holds whatever the pass would allow.
func TestSwapOverHTTP_APassBudgetBoundsWhatCanBeBought(t *testing.T) {
	b, base := serve(t)
	h, _ := withJupiter(t, b)
	token, userID, passID := issuePassWith(t, b, 1_000_000) // $1
	if _, err := b.SpendPasses.UpdateControls(context.Background(), userID, passID, spendpass.Controls{NewProviders: spendpass.NewProvidersAllow}); err != nil {
		t.Fatal(err)
	}
	status, _, out := call(t, "POST", base+"/api/v1/execute", token, swapRequest("swap-over", "2.5"))
	if status == 200 && out["delivered"] == true {
		t.Errorf("a $2.50 swap on a $1 pass: %v", out)
	}
	if _, _, landed := h.Counts(); landed != 0 {
		t.Error("nothing landed")
	}
}
