package e2e

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/project-algebra/algebra/internal/domain/spendpass"
)

// The sandbox personas (providers/sandboxpay/personas.go) sell token.price the
// way real providers do: alpha honest, beta honest and cheapest, flaky down,
// greedy over its listing, trap at $25 a call. These tests drive the real API
// over HTTP against them: routing, the guards and the spend firewall.

func priceRequest(window string) map[string]any {
	return map[string]any{
		"capability": "token.price", "input": map[string]any{"mint": "So11111111111111111111111111111111111111112"},
		"budget_max_minor": 50_000, "window": window,
	}
}

func rejectedCodes(out map[string]any) map[string]string {
	codes := map[string]string{}
	rej, _ := out["rejected"].([]any)
	for _, r := range rej {
		m, _ := r.(map[string]any)
		p, _ := m["provider"].(string)
		c, _ := m["code"].(string)
		codes[p] = c
	}
	return codes
}

func deliveredBy(out map[string]any) string {
	atts, _ := out["attempts"].([]any)
	if len(atts) == 0 {
		return ""
	}
	last, _ := atts[len(atts)-1].(map[string]any)
	res, _ := last["result"].(map[string]any)
	p, _ := res["provider"].(string)
	return p
}

func TestRouterOverHTTP_AClassIsPaidToTheBestHonestProvider(t *testing.T) {
	b, base := serve(t)
	token := issuePass(t, b)

	req := priceRequest("e2e-route-" + uuid.NewString())
	req["constraints"] = map[string]any{"allowed_networks": []string{"sandbox"}}
	status, _, out := call(t, "POST", base+"/api/v1/execute", token, req)
	if status != 200 || out["delivered"] != true {
		t.Fatalf("routed call: %d %v", status, out)
	}
	if got := deliveredBy(out); got != "sandbox:beta" {
		t.Fatalf("paid %s; the cheapest honest provider is sandbox:beta", got)
	}
	codes := rejectedCodes(out)
	for provider, want := range map[string]string{
		"sandbox:trap":   "price_outlier",
		"sandbox:greedy": "price_above_listing",
		"sandbox:flaky":  "no_quote",
	} {
		if codes[provider] != want {
			t.Errorf("%s: rejected as %q, want %q (all: %v)", provider, codes[provider], want, codes)
		}
	}
	routing, _ := out["routing"].(map[string]any)
	offers, _ := routing["offers"].([]any)
	if len(offers) != 2 {
		t.Fatalf("the plan holds beta then alpha: %v", routing)
	}
	if first := offers[0].(map[string]any)["provider"]; first != "sandbox:beta" {
		t.Errorf("ranked first: %v", first)
	}
	if out["receipt"] == "" || out["receipt"] == nil {
		t.Error("a committed intent carries a signed receipt")
	}
	intent := out["intent"].(map[string]any)
	if intent["committed_minor"] != float64(1_000) {
		t.Errorf("committed %v, want 1000 (beta's price)", intent["committed_minor"])
	}
}

func TestSimulateOverHTTP_AnswersWithoutPaying(t *testing.T) {
	b, base := serve(t)
	token := issuePass(t, b)
	req := priceRequest("e2e-sim-" + uuid.NewString())
	req["live_quotes"] = true

	status, _, sim := call(t, "POST", base+"/api/v1/policy/simulate", token, req)
	if status != 200 || sim["verdict"] != "ALLOW" {
		t.Fatalf("dry run: %d %v", status, sim)
	}
	wp, _ := sim["would_pay"].(map[string]any)
	if wp["provider"] != "sandbox:beta" || wp["price_source"] != "live" {
		t.Fatalf("would pay: %v", wp)
	}
	// Nothing was created, so the real call is a fresh intent.
	status, _, out := call(t, "POST", base+"/api/v1/execute", token, priceRequest(req["window"].(string)))
	if status != 200 || out["created"] != true {
		t.Fatalf("the dry run must leave nothing behind: %d %v", status, out)
	}
}

func TestKillSwitchOverHTTP_AFrozenPassPaysNothing(t *testing.T) {
	b, base := serve(t)
	token, userID, _ := issuePassWith(t, b, 1_000_000)
	ctx := context.Background()

	if n, err := b.SpendPasses.KillSwitch(ctx, userID, true); err != nil || n != 1 {
		t.Fatalf("freeze: %d %v", n, err)
	}
	status, _, out := call(t, "POST", base+"/api/v1/execute", token, priceRequest("e2e-kill-"+uuid.NewString()))
	if status != 403 {
		t.Fatalf("a frozen pass must be refused: %d %v", status, out)
	}
	if _, err := b.SpendPasses.KillSwitch(ctx, userID, false); err != nil {
		t.Fatal(err)
	}
	status, _, out = call(t, "POST", base+"/api/v1/execute", token, priceRequest("e2e-thaw-"+uuid.NewString()))
	if status != 200 || out["delivered"] != true {
		t.Fatalf("after thawing: %d %v", status, out)
	}
}

func TestNewProviderGateOverHTTP_TheFirstPaymentWaitsForThePerson(t *testing.T) {
	b, base := serve(t)
	token, userID, passID := issuePassWith(t, b, 1_000_000)
	ctx := context.Background()
	if _, err := b.SpendPasses.UpdateControls(ctx, userID, passID, spendpass.Controls{NewProviders: spendpass.NewProvidersApprove}); err != nil {
		t.Fatal(err)
	}

	req := priceRequest("e2e-gate-" + uuid.NewString())
	req["providers"] = []string{"sandbox:alpha"}
	status, _, out := call(t, "POST", base+"/api/v1/execute", token, req)
	if status != 409 || out["intent_state"] != "AWAITING_APPROVAL" {
		t.Fatalf("a never-paid provider waits for approval: %d %v", status, out)
	}
	id, _ := out["intent_id"].(string)
	if _, err := b.Economic.Approve(ctx, userID, id); err != nil {
		t.Fatal(err)
	}
	status, _, out = call(t, "POST", base+"/api/v1/economic-intents/"+id+"/execute", token, map[string]any{"providers": []string{"sandbox:alpha"}})
	if status != 200 || out["delivered"] != true || deliveredBy(out) != "sandbox:alpha" {
		t.Fatalf("after approval: %d %v", status, out)
	}
	// Paid once, alpha is known: the next call goes straight through.
	req["window"] = "e2e-gate2-" + uuid.NewString()
	if status, _, out = call(t, "POST", base+"/api/v1/execute", token, req); status != 200 || out["delivered"] != true {
		t.Fatalf("second call to a provider already paid: %d %v", status, out)
	}
}

func TestVelocityOverHTTP_ALoopingAgentIsStopped(t *testing.T) {
	b, base := serve(t)
	token, userID, passID := issuePassWith(t, b, 1_000_000)
	if _, err := b.SpendPasses.UpdateControls(context.Background(), userID, passID, spendpass.Controls{MaxCallsPerMinute: 2}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		if status, _, out := call(t, "POST", base+"/api/v1/execute", token, priceRequest(fmt.Sprintf("e2e-loop-%d-%s", i, uuid.NewString()))); status != 200 {
			t.Fatalf("call %d: %d %v", i, status, out)
		}
	}
	status, _, out := call(t, "POST", base+"/api/v1/execute", token, priceRequest("e2e-loop-3-"+uuid.NewString()))
	if status != 409 || out["reason"] != "rate_limited" {
		t.Fatalf("the third paid call in a minute must be refused: %d %v", status, out)
	}
}

func TestClassesOverHTTP(t *testing.T) {
	// Monid's catalog is a snapshot built into the binary: classes to list,
	// and still nothing leaves this machine.
	_, base := serveWith(t, map[string]string{"MONID_CATALOG": "on"})
	status, _, out := call(t, "GET", base+"/api/v1/classes", "", nil)
	classes, _ := out["classes"].([]any)
	if status != 200 || len(classes) < 14 {
		t.Fatalf("classes: %d %d", status, len(classes))
	}
	status, _, out = call(t, "GET", base+"/api/v1/classes/token.price", "", nil)
	if status != 200 || out["class"] == nil {
		t.Fatalf("one class: %d %v", status, out)
	}
	if status, _, _ := call(t, "GET", base+"/api/v1/classes/no.such", "", nil); status != 404 {
		t.Errorf("unknown class: %d", status)
	}
}
