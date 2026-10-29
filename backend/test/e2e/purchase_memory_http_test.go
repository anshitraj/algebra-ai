package e2e

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
)

// Purchase memory over real HTTP and Postgres (internal/app/purchase_memory.go):
// the same work with the same input, asked again in another window while the
// earlier answer is fresh, is answered with that answer and pays nothing;
// "fresh" pays for a new one; other input is other work.
func TestExecuteOverHTTP_TheSameWorkIsNotBoughtTwiceWhileFresh(t *testing.T) {
	b, base := serve(t)
	token := issuePass(t, b)
	const sol = "So11111111111111111111111111111111111111112"
	const usdc = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"

	ask := func(mint string, extra map[string]any) map[string]any {
		t.Helper()
		req := map[string]any{
			"capability": "token.price", "input": map[string]any{"mint": mint},
			"budget_max_minor": 50_000, "window": "e2e-memory-" + uuid.NewString(),
		}
		for k, v := range extra {
			req[k] = v
		}
		status, _, out := call(t, "POST", base+"/api/v1/execute", token, req)
		if status != 200 || out["delivered"] != true {
			t.Fatalf("execute: %d %v", status, out)
		}
		return out
	}
	intentOf := func(out map[string]any) map[string]any {
		in, _ := out["intent"].(map[string]any)
		if in == nil {
			t.Fatalf("no intent in %v", out)
		}
		return in
	}

	first := ask(sol, nil)
	id, paid := intentOf(first)["id"].(string), intentOf(first)["committed_minor"].(float64)
	if first["created"] != true || first["reused"] != nil || first["replayed"] != nil || paid <= 0 {
		t.Fatalf("the first request is a purchase: %v", first)
	}

	// Asked again in another window: the earlier answer, for nothing.
	again := ask(sol, nil)
	reused, _ := again["reused"].(map[string]any)
	if again["stopped"] != "reused" || again["replayed"] != true || reused == nil ||
		reused["intent_id"] != id || reused["paid_minor"] != paid || reused["fresh_for_seconds"] != float64(60) {
		t.Fatalf("a repeat while fresh is answered from the earlier purchase: %v", again)
	}
	if intentOf(again)["id"] != id || intentOf(again)["committed_minor"] != paid || intentOf(again)["attempts"] != intentOf(first)["attempts"] {
		t.Errorf("no new intent and nothing paid again: %v", intentOf(again))
	}
	if fmt.Sprint(again["response"]) != fmt.Sprint(first["response"]) {
		t.Errorf("the same answer:\n first %v\n again %v", first["response"], again["response"])
	}

	// "fresh" pays for a new one.
	fresh := ask(sol, map[string]any{"fresh": true})
	if fresh["created"] != true || fresh["reused"] != nil || intentOf(fresh)["id"] == id || intentOf(fresh)["committed_minor"].(float64) <= 0 {
		t.Errorf("fresh is a new purchase: %v", fresh)
	}

	// Other input is other work.
	other := ask(usdc, nil)
	if other["created"] != true || other["reused"] != nil || intentOf(other)["id"] == id {
		t.Errorf("another mint is a new purchase: %v", other)
	}
}
