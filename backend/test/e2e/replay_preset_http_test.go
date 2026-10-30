package e2e

import (
	"testing"
)

// "Test this pass" over real HTTP and Postgres: a Spend Pass with no task yet is
// put through the chaos scenarios on the kinds of work agents usually buy, under
// its own limits, against the sandbox sellers. Nobody is paid for it.
func TestReplayPresetOverHTTP_APassIsTestedWithoutPayingAnyone(t *testing.T) {
	_, base := serve(t)
	alice, bob := signUp(t, base, "Alice"), signUp(t, base, "Bob")
	_, project := workspace(t, base, alice)
	pass := projectPass(t, base, alice, project, "sandbox")
	path := base + "/api/v1/me/passes/" + pass + "/replay"

	status, _, rep := browserCall(t, "POST", path, alice, nil)
	if status != 200 || rep["preset"] != true || rep["spend_pass_id"] != pass || rep["job_pass_id"] != nil {
		t.Fatalf("a preset run is of the pass, not of a task: %d %v", status, rep)
	}
	purchases, _ := rep["purchases"].([]any)
	if len(purchases) == 0 {
		t.Fatalf("the sandbox sellers do token price: %v", rep)
	}
	first, _ := purchases[0].(map[string]any)
	findings, _ := first["findings"].([]any)
	if first["capability"] != "token.price" || first["title"] != "Token price" || first["intent_id"] != nil || len(findings) != 6 {
		t.Fatalf("nothing was bought, six scenarios were run: %v", first)
	}
	by := map[string]map[string]any{}
	for _, f := range findings {
		m, _ := f.(map[string]any)
		by[m["scenario"].(string)] = m
	}
	for _, sc := range []string{"overcharge", "honeypot"} {
		if by[sc]["outcome"] != "caught" || by[sc]["safe"] != true {
			t.Errorf("%s is stopped by the router and the pass: %v", sc, by[sc])
		}
	}
	if total := rep["safe"].(float64) + rep["unsafe"].(float64); total != float64(6*len(purchases)) {
		t.Errorf("every scenario of every purchase is counted once: %v of %d purchases", total, len(purchases))
	}

	// Nobody was paid: the pass spent nothing.
	status, _, list := browserCall(t, "GET", base+"/api/v1/me/passes", alice, nil)
	passes, _ := list["passes"].([]any)
	if status != 200 || len(passes) == 0 {
		t.Fatalf("passes: %d %v", status, list)
	}
	for _, p := range passes {
		if m, _ := p.(map[string]any); m["id"] == pass && m["spent_minor_units"] != float64(0) {
			t.Errorf("a test pays nobody: %v", m)
		}
	}

	// Someone else's pass is not there to test, nor is one without a session.
	if status, _, out := browserCall(t, "POST", path, bob, nil); status != 404 {
		t.Errorf("another person's pass: %d %v", status, out)
	}
	if status, _, out := browserCall(t, "POST", path, nil, nil); status != 401 {
		t.Errorf("nobody signed in: %d %v", status, out)
	}

	// A pass that was ended can't be tested, and says why.
	if status, _, out := browserCall(t, "POST", base+"/api/v1/me/passes/"+pass+"/revoke", alice, nil); status != 200 {
		t.Fatalf("revoke: %d %v", status, out)
	}
	if status, _, out := browserCall(t, "POST", path, alice, nil); status != 403 || out["error"] == nil {
		t.Errorf("a revoked pass: %d %v", status, out)
	}
}
