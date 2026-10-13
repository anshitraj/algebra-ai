package e2e

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// What the person is shown before anything is paid, and what no request can
// change: a HOLD waits for a person, a BLOCK pays nothing, and a payment that
// worked is not a verified seller.

func candidateOf(sim map[string]any, provider string) map[string]any {
	cs, _ := sim["candidates"].([]any)
	for _, c := range cs {
		m, _ := c.(map[string]any)
		if m["provider"] == provider {
			return m
		}
	}
	return nil
}

func TestDecisionOverHTTP_TheDryRunSaysWhatItWouldPayAndWhoItIs(t *testing.T) {
	b, base := serve(t)
	token := issuePass(t, b)

	simulate := func(strategy string) map[string]any {
		req := priceRequest("e2e-dec-" + uuid.NewString())
		req["live_quotes"] = true
		req["constraints"] = map[string]any{"allowed_networks": []string{"sandbox"}}
		if strategy != "" {
			req["provider_policy"] = map[string]any{"strategy": strategy}
		}
		status, _, sim := call(t, "POST", base+"/api/v1/policy/simulate", token, req)
		if status != 200 {
			t.Fatalf("simulate: %d %v", status, sim)
		}
		return sim
	}

	// All three strategies answer, each naming how it ranked.
	for strategy, mode := range map[string]string{"cheapest": "CHEAPEST", "fastest": "FASTEST", "balanced": "AUTO", "": "AUTO"} {
		sim := simulate(strategy)
		if sim["strategy"] != mode || sim["decision"] != "ALLOW" {
			t.Errorf("strategy %q: strategy=%v decision=%v", strategy, sim["strategy"], sim["decision"])
		}
		plan, _ := sim["plan"].(map[string]any)
		if plan["mode"] != mode {
			t.Errorf("strategy %q: plan mode %v", strategy, plan["mode"])
		}
	}

	// What would be paid, with the facts the person decides on.
	sim := simulate("cheapest")
	pay, _ := sim["would_pay"].(map[string]any)
	if pay["provider"] != "sandbox:beta" || pay["decision"] != "ALLOW" || pay["total_minor"] != float64(1_000) || pay["network"] != "sandbox" {
		t.Fatalf("would pay: %v", pay)
	}
	seller, _ := pay["seller"].(map[string]any)
	if seller["known"] != true || seller["trust"] != "unverified" || seller["payment_tested"] != false {
		t.Fatalf("before any payment the seller is listed and nothing more: %v", seller)
	}
	// A refused provider is a BLOCK with the reason.
	if trap := candidateOf(sim, "sandbox:trap"); trap == nil || trap["decision"] != "BLOCK" || !strings.Contains(stringify(trap["reasons"]), "price_outlier") {
		t.Errorf("the trap-priced seller: %v", trap)
	}

	// Pay beta once. The payment worked: that is tested, and nothing more.
	req := priceRequest("e2e-dec-pay-" + uuid.NewString())
	req["constraints"] = map[string]any{"allowed_networks": []string{"sandbox"}}
	req["provider_policy"] = map[string]any{"strategy": "cheapest"}
	if status, _, out := call(t, "POST", base+"/api/v1/execute", token, req); status != 200 || out["delivered"] != true {
		t.Fatalf("pay beta: %d %v", status, out)
	}
	beta := candidateOf(simulate("cheapest"), "sandbox:beta")
	seller, _ = beta["seller"].(map[string]any)
	if seller["payment_tested"] != true {
		t.Fatalf("a delivered payment is recorded as tested: %v", seller)
	}
	if seller["trust"] != "unverified" {
		t.Fatalf("a payment that worked must never verify a seller's identity: trust is %v", seller["trust"])
	}
}

func TestDecisionOverHTTP_ABlockedSellerIsABlockAndNeverPaid(t *testing.T) {
	b, base := serve(t)
	token := issuePass(t, b)
	named := func() (int, map[string]any) {
		req := priceRequest("e2e-block-" + uuid.NewString())
		req["constraints"] = map[string]any{"allowed_networks": []string{"sandbox"}}
		req["providers"] = []string{"sandbox:shifty"}
		status, _, out := call(t, "POST", base+"/api/v1/execute", token, req)
		return status, out
	}
	// Its first request is refused for the new payee and blocks it.
	if status, out := named(); status == 200 || out["delivered"] == true {
		t.Fatalf("shifty was paid: %d %v", status, out)
	}
	// The dry run now says BLOCK, with the reason and the seller's state.
	req := priceRequest("e2e-block-sim-" + uuid.NewString())
	req["live_quotes"] = true
	req["providers"] = []string{"sandbox:shifty"}
	req["constraints"] = map[string]any{"allowed_networks": []string{"sandbox"}}
	status, _, sim := call(t, "POST", base+"/api/v1/policy/simulate", token, req)
	if status != 200 || sim["decision"] != "BLOCK" || sim["verdict"] != "DENY" {
		t.Fatalf("dry run: %d %v", status, sim)
	}
	c := candidateOf(sim, "sandbox:shifty")
	seller, _ := c["seller"].(map[string]any)
	if c == nil || c["decision"] != "BLOCK" || !strings.Contains(stringify(c["reasons"]), "PROVIDER_BLOCKED") || seller["trust"] != "blocked" {
		t.Fatalf("blocked seller: %v", c)
	}
	// And a real request still pays nothing, however many times it is made.
	for i := 0; i < 2; i++ {
		if status, out := named(); status == 200 || out["delivered"] == true {
			t.Fatalf("a blocked seller was paid on attempt %d: %d %v", i+2, status, out)
		}
	}
}

func TestDecisionOverHTTP_AHoldWaitsForAPersonAndNoRequestSkipsIt(t *testing.T) {
	_, base := serve(t)
	alice := signUp(t, base, "Alice")
	_, project := workspace(t, base, alice)
	status, _, pass := browserCall(t, "POST", base+"/api/v1/me/passes", alice, map[string]any{
		"label": "ask me", "agent_kind": "custom", "currency": "USDC", "budget_minor_units": 1_000_000, "budget_period": "total",
		"approve_above_minor_units": 0, "project_id": project, "environment": "sandbox",
	})
	if status != 201 {
		t.Fatalf("pass: %d %v", status, pass)
	}
	secret, _ := issueKey(t, base, alice, project, map[string]any{"name": "ci", "environment": "sandbox"})
	window := "e2e-hold-" + uuid.NewString()

	run := func(extra map[string]any) (int, map[string]any) {
		req := priceRequest(window)
		for k, v := range extra {
			req[k] = v
		}
		status, _, out := call(t, "POST", base+"/api/v1/execute", secret, req)
		return status, out
	}
	status, out := run(nil)
	if status != 409 || out["reason"] != "approval_required" {
		t.Fatalf("the pass asks every time: %d %v", status, out)
	}
	intent, _ := out["intent_id"].(string)
	// Saying "no approval needed" in the request changes nothing.
	if status, out := run(map[string]any{"require_approval": false, "approved": true, "approval": "granted"}); status != 409 || out["reason"] != "approval_required" {
		t.Fatalf("a request must not skip an approval: %d %v", status, out)
	}
	// The key can't approve; nothing was paid.
	if status, _, _ := call(t, "POST", base+"/api/v1/me/economic-intents/"+intent+"/approve", secret, nil); status == 200 {
		t.Fatal("an API key approved a held payment")
	}
	status, _, held := browserCall(t, "GET", base+"/api/v1/me/economic-intents/"+intent, alice, nil)
	if status != 200 || held["state"] != "AWAITING_APPROVAL" || held["committed_minor"] != float64(0) {
		t.Fatalf("still held, nothing paid: %d %v", status, held)
	}
	// The person says yes; then, and only then, it pays once.
	if status, _, out := browserCall(t, "POST", base+"/api/v1/me/economic-intents/"+intent+"/approve", alice, nil); status != 200 {
		t.Fatalf("approve: %d %v", status, out)
	}
	status, out = run(nil)
	if status != 200 || out["delivered"] != true {
		t.Fatalf("after approval: %d %v", status, out)
	}
}
