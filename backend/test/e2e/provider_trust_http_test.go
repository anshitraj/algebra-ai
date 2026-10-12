package e2e

import (
	"context"
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
