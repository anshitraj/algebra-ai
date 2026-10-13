package e2e

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/project-algebra/algebra/internal/domain/intel"
	"github.com/project-algebra/algebra/providers/sandboxpay"
)

// Demo C: the sandbox seller "shifty" lists one payee and its 402 asks to be
// paid at another, as a listing taken over would. The registry pins the
// listing's payee when the API starts, so the very first request is refused
// and the endpoint blocked; nothing is ever paid to it.

func TestProviderTrustOverHTTP_APayeeChangeIsRefusedThenBlocked(t *testing.T) {
	b, base := serve(t)
	token := issuePass(t, b)
	ctx := context.Background()
	if b.Intel == nil || b.SandboxPersonas == nil {
		t.Skip("provider intelligence or the sandbox sellers are off")
	}
	id := intel.ID("sandbox:shifty", base+"/api/v1/sandbox/x402/prices/shifty", sandboxpay.Network)

	// Listed at startup, before any scheduled refresh, with the listing's payee pinned.
	d, err := b.Intel.Endpoint(ctx, id)
	if err != nil {
		t.Fatalf("shifty is not in the registry at startup: %v", err)
	}
	if d.Trust != intel.TrustUnverified || d.PinnedPayTo != "sandbox-provider-shifty" {
		t.Fatalf("at startup: trust %s, pinned %q", d.Trust, d.PinnedPayTo)
	}

	named := func() map[string]any {
		req := priceRequest("e2e-shifty-" + uuid.NewString())
		req["constraints"] = map[string]any{"allowed_networks": []string{"sandbox"}}
		req["providers"] = []string{"sandbox:shifty"}
		status, _, out := call(t, "POST", base+"/api/v1/execute", token, req)
		if status == 200 || out["delivered"] == true {
			t.Fatalf("shifty was paid: %d %v", status, out)
		}
		return out
	}

	// First request: refused for the new payee, and the endpoint is blocked.
	if code := rejectedCodes(named())["sandbox:shifty"]; code != intel.ReasonPayeeChanged {
		t.Fatalf("first request refused as %q, want %s", code, intel.ReasonPayeeChanged)
	}
	d, err = b.Intel.Endpoint(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if d.Trust != intel.TrustBlocked || d.TrustReason != intel.ReasonPayeeChanged || d.Levels.Executable {
		t.Fatalf("after the refusal: trust %s (%s), levels %+v", d.Trust, d.TrustReason, d.Levels)
	}
	var flagged bool
	for _, c := range d.Changes {
		if c.Kind == intel.ChangePayeeChanged && c.Suspicious && c.After["pay_to"] == "sandbox-attacker-shifty" {
			flagged = true
		}
	}
	if !flagged {
		t.Fatalf("no suspicious payee change recorded: %+v", d.Changes)
	}

	// From then on it is blocked outright.
	if code := rejectedCodes(named())["sandbox:shifty"]; code != intel.ReasonBlocked {
		t.Fatalf("second request refused as %q, want %s", code, intel.ReasonBlocked)
	}

	// Routing the class pays an honest seller and lists shifty among the refused.
	req := priceRequest("e2e-shifty-route-" + uuid.NewString())
	req["constraints"] = map[string]any{"allowed_networks": []string{"sandbox"}}
	status, _, out := call(t, "POST", base+"/api/v1/execute", token, req)
	if status != 200 || out["delivered"] != true || deliveredBy(out) != "sandbox:beta" {
		t.Fatalf("routed call: %d paid %q %v", status, deliveredBy(out), out)
	}
	if code := rejectedCodes(out)["sandbox:shifty"]; code != intel.ReasonBlocked {
		t.Errorf("routed call: shifty refused as %q, want %s", code, intel.ReasonBlocked)
	}

	// A person unblocks it: the decision is recorded with who made it, and
	// the new payee is accepted. The next refresh finds the listing still
	// naming the old payee, so the endpoint is blocked again.
	if _, err := b.Intel.SetTrust(ctx, id, intel.TrustUnverified, "e2e: accepted the new wallet", "user_e2e"); err != nil {
		t.Fatal(err)
	}
	d, _ = b.Intel.Endpoint(ctx, id)
	if d.Trust != intel.TrustUnverified || d.PinnedPayTo != "sandbox-attacker-shifty" || d.Changes[0].Actor != "user_e2e" {
		t.Fatalf("after unblocking: trust %s, pinned %q, last change by %q", d.Trust, d.PinnedPayTo, d.Changes[0].Actor)
	}
	if run := b.Intel.Sync(ctx, "e2e", nil); run.Status == "failed" {
		t.Fatalf("refresh: %+v", run)
	}
	if d, _ = b.Intel.Endpoint(ctx, id); d.Trust != intel.TrustBlocked {
		t.Fatalf("after the refresh: trust %s, want blocked", d.Trust)
	}
}

func TestProviderRegistryOverHTTP_OpenToReadDecidedByAPerson(t *testing.T) {
	b, base := serveWith(t, map[string]string{"ALGEBRA_OPERATOR_TOKEN": ""})
	if b.Intel == nil || b.SandboxPersonas == nil {
		t.Skip("provider intelligence or the sandbox sellers are off")
	}
	token := issuePass(t, b)
	id := intel.ID("sandbox:alpha", base+"/api/v1/sandbox/x402/prices/alpha", sandboxpay.Network)

	status, _, out := call(t, "GET", base+"/api/v1/intel/status", "", nil)
	st, _ := out["status"].(map[string]any)
	if status != 200 || st == nil || out["can_manage"] != false {
		t.Fatalf("status: %d %v", status, out)
	}
	if !strings.Contains(stringify(st["sources"]), `"sandbox"`) {
		t.Errorf("the sandbox catalog is not among the sources: %v", st["sources"])
	}

	// Narrowed to this server's own alpha: on a long-lived database the sandbox sellers of every earlier test
	// server are listed too, and a page holds at most 200.
	alphaURL := base + "/api/v1/sandbox/x402/prices/alpha"
	status, _, out = call(t, "GET", base+"/api/v1/intel/endpoints?source=sandbox&limit=200&q="+url.QueryEscape(alphaURL), "", nil)
	var found map[string]any
	eps, _ := out["endpoints"].([]any)
	for _, e := range eps {
		if m, _ := e.(map[string]any); m["id"] == id {
			found = m
		}
	}
	if status != 200 || found == nil {
		t.Fatalf("listing: %d, alpha (%s) not found among %d", status, id, len(eps))
	}
	if lv, _ := found["levels"].(map[string]any); lv["listed"] != true || lv["executable"] != true {
		t.Errorf("alpha's levels: %v", found["levels"])
	}
	if status, _, _ := call(t, "GET", base+"/api/v1/intel/endpoints?level=trusted", "", nil); status != 400 {
		t.Errorf("an unknown level: %d, want 400", status)
	}

	// An agent can read the registry but never decide about it.
	for _, p := range []string{"/api/v1/intel/endpoints/" + id + "/trust", "/api/v1/intel/refresh"} {
		method := "PUT"
		if strings.HasSuffix(p, "refresh") {
			method = "POST"
		}
		if status, _, out := call(t, method, base+p, token, map[string]any{"trust": "verified"}); status != 403 {
			t.Errorf("%s %s with an agent token: %d %v, want 403", method, p, status, out)
		}
	}

	// A person blocks it; the decision is in its history with who made it.
	who := signUp(t, base, "Ops")
	status, _, out = browserCall(t, "PUT", base+"/api/v1/intel/endpoints/"+id+"/trust", who, map[string]any{"trust": "blocked", "note": "e2e: looks wrong"})
	if status != 200 || out["trust"] != "blocked" || out["trust_reason"] != intel.ReasonManual {
		t.Fatalf("block: %d %v", status, out)
	}
	status, _, out = call(t, "GET", base+"/api/v1/intel/changes?endpoint="+id, "", nil)
	changes, _ := out["changes"].([]any)
	if status != 200 || len(changes) == 0 {
		t.Fatalf("changes: %d %v", status, out)
	}
	last, _ := changes[0].(map[string]any)
	if last["kind"] != string(intel.ChangeTrust) || last["actor"] == "system" || last["actor"] == "" {
		t.Errorf("the latest change is %v, want a person's trust decision", last)
	}

	// Blocked by a person, it is refused when named.
	req := priceRequest("e2e-alpha-blocked-" + uuid.NewString())
	req["constraints"] = map[string]any{"allowed_networks": []string{"sandbox"}}
	req["providers"] = []string{"sandbox:alpha"}
	if status, _, out := call(t, "POST", base+"/api/v1/execute", token, req); status == 200 || rejectedCodes(out)["sandbox:alpha"] != intel.ReasonBlocked {
		t.Errorf("a blocked endpoint named: %d %v", status, rejectedCodes(out))
	}
}

// A paid delivery makes a seller "payment tested". It never verifies it: that
// takes every check of the standard and a person's record of the evidence.
func TestProviderTrustOverHTTP_APaidDeliveryTestsASellerAndOnlyAReviewVerifiesIt(t *testing.T) {
	b, base := serveWith(t, map[string]string{"ALGEBRA_OPERATOR_TOKEN": ""})
	if b.Intel == nil || b.SandboxPersonas == nil {
		t.Skip("provider intelligence or the sandbox sellers are off")
	}
	token := issuePass(t, b)
	beta := intel.ID("sandbox:beta", base+"/api/v1/sandbox/x402/prices/beta", sandboxpay.Network)
	alpha := intel.ID("sandbox:alpha", base+"/api/v1/sandbox/x402/prices/alpha", sandboxpay.Network)
	detail := func(id string) map[string]any {
		t.Helper()
		status, _, out := call(t, "GET", base+"/api/v1/intel/endpoints/"+id, "", nil)
		if status != 200 {
			t.Fatalf("endpoint %s: %d %v", id, status, out)
		}
		return out
	}
	unmet := func(d map[string]any) []string {
		var ids []string
		checks, _ := d["verification"].([]any)
		if len(checks) == 0 {
			t.Fatalf("an endpoint carries the standard's checklist: %v", d)
		}
		for _, c := range checks {
			if m := c.(map[string]any); m["passed"] != true {
				ids = append(ids, m["id"].(string))
			}
		}
		return ids
	}

	// Algebra pays beta and it delivers.
	req := priceRequest("e2e-tested-" + uuid.NewString())
	req["constraints"] = map[string]any{"allowed_networks": []string{"sandbox"}}
	req["providers"] = []string{"sandbox:beta"}
	if status, _, out := call(t, "POST", base+"/api/v1/execute", token, req); status != 200 || out["delivered"] != true {
		t.Fatalf("execute: %d %v", status, out)
	}
	d := detail(beta)
	lv, _ := d["levels"].(map[string]any)
	if lv["tested"] != true || lv["verified"] != false || d["trust"] != "unverified" || d["verified_at"] != nil {
		t.Fatalf("paid and delivered is payment tested, never verified: levels %v trust %v", lv, d["trust"])
	}
	if left := unmet(d); len(left) != 0 {
		t.Fatalf("beta now meets every automatic check, yet: %v", left)
	}

	// Verifying is a person's, and needs the evidence they reviewed.
	who := signUp(t, base, "Reviewer")
	trust := base + "/api/v1/intel/endpoints/" + beta + "/trust"
	for _, note := range []string{"", "looks fine"} {
		if status, _, out := browserCall(t, "PUT", trust, who, map[string]any{"trust": "verified", "note": note}); status != 400 || !strings.Contains(stringify(out), "evidence") {
			t.Fatalf("verifying with note %q: %d %v, want 400 asking for the evidence", note, status, out)
		}
	}
	evidence := "Algebra's own sandbox seller, served by this process; reviewed for the e2e test"
	status, _, out := browserCall(t, "PUT", trust, who, map[string]any{"trust": "verified", "note": evidence})
	if status != 200 || out["trust"] != "verified" || out["trust_reason"] != intel.ReasonIdentityReviewed {
		t.Fatalf("verify with evidence: %d %v", status, out)
	}
	status, _, out = call(t, "GET", base+"/api/v1/intel/changes?endpoint="+beta+"&kind=trust_changed", "", nil)
	changes, _ := out["changes"].([]any)
	if status != 200 || len(changes) != 1 {
		t.Fatalf("one trust decision on record: %d %v", status, out)
	}
	if after, _ := changes[0].(map[string]any)["after"].(map[string]any); after["note"] != evidence {
		t.Fatalf("the decision keeps the evidence: %v", changes[0])
	}

	// Alpha was never paid: no evidence, however good, verifies it.
	if left := strings.Join(unmet(detail(alpha)), ","); !strings.Contains(left, "tested") {
		t.Fatalf("alpha is untested, yet its unmet checks are %q", left)
	}
	status, _, out = browserCall(t, "PUT", base+"/api/v1/intel/endpoints/"+alpha+"/trust", who, map[string]any{"trust": "verified", "note": evidence})
	if status != 400 || !strings.Contains(stringify(out), "no paid call has delivered") {
		t.Fatalf("verifying an untested endpoint: %d %v", status, out)
	}
}
