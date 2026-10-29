package e2e

import (
	"testing"

	"github.com/google/uuid"
)

// Cross-checking over real HTTP and Postgres: two providers are paid for the same
// question, and the answers Algebra kept are compared. The sandbox sellers all
// derive their price from the token alone, so they agree; what makes a difference
// is covered by the unit tests, which can make sellers disagree.
func TestCrossCheckOverHTTP_TwoProvidersAnswerOneQuestion(t *testing.T) {
	b, base := serve(t)
	token := issuePass(t, b)
	other := issuePass(t, b) // another person

	ask := func(provider string, fresh bool) string {
		t.Helper()
		req := priceRequest("e2e-check-" + uuid.NewString())
		req["providers"] = []string{provider}
		if fresh {
			req["fresh"] = true
		}
		status, _, out := call(t, "POST", base+"/api/v1/execute", token, req)
		if status != 200 || out["delivered"] != true || out["reused"] != nil {
			t.Fatalf("paying %s: %d %v", provider, status, out)
		}
		return out["intent"].(map[string]any)["id"].(string)
	}
	alpha, beta := ask("sandbox:alpha", false), ask("sandbox:beta", false)

	status, hdr, got := call(t, "POST", base+"/api/v1/cross-check", token, map[string]any{"intent_ids": []string{alpha, beta}})
	if status != 200 || got["verdict"] != "agree" || got["capability"] != "token.price" || got["compared"] != float64(1) || got["agreed"] != float64(1) {
		t.Fatalf("two sandbox sellers agree on the one price: %d %v", status, got)
	}
	answers, _ := got["answers"].([]any)
	if len(answers) != 2 || answers[0].(map[string]any)["provider"] != "sandbox:alpha" || answers[1].(map[string]any)["intent_id"] != beta {
		t.Errorf("who was compared: %v", got["answers"])
	}
	if got["values_are_untrusted_provider_data"] != true || hdr.Get("Cache-Control") != "no-store" {
		t.Errorf("what is built from the providers' answers is marked so: %v %q", got["values_are_untrusted_provider_data"], hdr.Get("Cache-Control"))
	}

	// What can't be a check is refused with the reason, and nothing is paid for asking.
	for name, tc := range map[string]struct {
		who  string
		ids  []string
		want int
	}{
		"one intent":           {token, []string{alpha}, 400},
		"nothing":              {token, nil, 400},
		"the same one twice":   {token, []string{alpha, alpha}, 400},
		"an intent that isn't": {token, []string{alpha, "eint_nope"}, 404},
		"another person's":     {other, []string{alpha, beta}, 404},
		"nobody":               {"not-a-token", []string{alpha, beta}, 401},
	} {
		if status, _, out := call(t, "POST", base+"/api/v1/cross-check", tc.who, map[string]any{"intent_ids": tc.ids}); status != tc.want {
			t.Errorf("%s: %d %v, want %d", name, status, out, tc.want)
		}
	}
	// Two answers from one provider are not a check.
	alphaAgain := ask("sandbox:alpha", true)
	status, _, out := call(t, "POST", base+"/api/v1/cross-check", token, map[string]any{"intent_ids": []string{alpha, alphaAgain}})
	if status != 400 || out["error"] == nil {
		t.Errorf("the same provider twice: %d %v", status, out)
	}
}
