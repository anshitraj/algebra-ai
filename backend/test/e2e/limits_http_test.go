package e2e

import (
	"strings"
	"testing"
)

// Plan limits and the trial are per organization, so the organizations one
// person may own are bounded and the trial is once per person: otherwise a
// new organization would be a new free project and a new trial.
func TestLimitsOverHTTP_OrganizationsPerPersonAndOneTrialEach(t *testing.T) {
	_, base := serve(t)
	alice := signUp(t, base, "Alice")
	first, _ := workspace(t, base, alice)

	// The workspace is one; two more may be made.
	var made []string
	for _, name := range []string{"Second org", "Third org"} {
		status, _, out := browserCall(t, "POST", base+"/api/v1/orgs", alice, map[string]any{"name": name})
		if status != 201 {
			t.Fatalf("org %q: %d %v", name, status, out)
		}
		id, _ := out["id"].(string)
		if id == "" {
			if o, ok := out["organization"].(map[string]any); ok {
				id, _ = o["id"].(string)
			}
		}
		made = append(made, id)
	}
	status, _, out := browserCall(t, "POST", base+"/api/v1/orgs", alice, map[string]any{"name": "Fourth org"})
	if status != 409 || !strings.Contains(stringify(out), "at most 3 organizations") {
		t.Fatalf("a fourth organization must be refused, naming the limit: %d %v", status, out)
	}

	// The trial: in one of her organizations, then never again in another.
	if status, _, out := browserCall(t, "POST", base+"/api/v1/orgs/"+first+"/trial", alice, nil); status != 200 {
		t.Fatalf("first trial: %d %v", status, out)
	}
	status, _, out = browserCall(t, "POST", base+"/api/v1/orgs/"+made[0]+"/trial", alice, nil)
	if status != 409 || !strings.Contains(stringify(out), "once per person") {
		t.Fatalf("a second trial in another organization must be refused: %d %v", status, out)
	}

	// Someone else still gets their own.
	bob := signUp(t, base, "Bob")
	bobOrg, _ := workspace(t, base, bob)
	if status, _, out := browserCall(t, "POST", base+"/api/v1/orgs/"+bobOrg+"/trial", bob, nil); status != 200 {
		t.Fatalf("another person's trial: %d %v", status, out)
	}
}
