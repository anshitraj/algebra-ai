package e2e

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Organizations, projects and API keys over real HTTP: a key works only in its
// own project and environment, spends only through that project's pass, can
// never manage keys, and nothing of one person's is visible to another.

func signUp(t *testing.T, base, name string) *http.Cookie {
	t.Helper()
	status, cookie, out := browserCall(t, "POST", base+"/api/v1/auth/signup", nil, map[string]any{
		"name": name, "email": strings.ToLower(name) + "-" + uuid.NewString() + "@example.test", "password": "correct-horse-" + uuid.NewString(),
	})
	if status != 201 && status != 200 || cookie == nil {
		t.Fatalf("sign up %s: %d %v", name, status, out)
	}
	return cookie
}

// workspace returns the person's first organization and its first project.
func workspace(t *testing.T, base string, who *http.Cookie) (orgID, projectID string) {
	t.Helper()
	status, _, out := browserCall(t, "GET", base+"/api/v1/orgs", who, nil)
	orgs, _ := out["organizations"].([]any)
	if status != 200 || len(orgs) != 1 {
		t.Fatalf("orgs: %d %v", status, out)
	}
	org := orgs[0].(map[string]any)
	if org["role"] != "owner" {
		t.Fatalf("the creator owns the workspace: %v", org)
	}
	orgID = org["id"].(string)
	status, _, out = browserCall(t, "GET", base+"/api/v1/orgs/"+orgID+"/projects", who, nil)
	projects, _ := out["projects"].([]any)
	if status != 200 || len(projects) != 1 {
		t.Fatalf("projects: %d %v", status, out)
	}
	return orgID, projects[0].(map[string]any)["id"].(string)
}

func projectPass(t *testing.T, base string, who *http.Cookie, projectID, env string) string {
	t.Helper()
	status, _, out := browserCall(t, "POST", base+"/api/v1/me/passes", who, map[string]any{
		"label": "ci " + env, "agent_kind": "custom", "currency": "USDC", "budget_minor_units": 1_000_000, "budget_period": "total",
		"project_id": projectID, "environment": env, "controls": map[string]any{"new_providers": "allow"},
	})
	if status != 201 {
		t.Fatalf("pass: %d %v", status, out)
	}
	return out["id"].(string)
}

func issueKey(t *testing.T, base string, who *http.Cookie, projectID string, body map[string]any) (secret, keyID string) {
	t.Helper()
	status, _, out := browserCall(t, "POST", base+"/api/v1/projects/"+projectID+"/keys", who, body)
	if status != 201 {
		t.Fatalf("key: %d %v", status, out)
	}
	k := out["key"].(map[string]any)
	return k["secret"].(string), k["id"].(string)
}

func TestTenancyOverHTTP_AKeyRunsInItsProjectAndEnvironmentOnly(t *testing.T) {
	_, base := serve(t)
	alice := signUp(t, base, "Alice")
	_, project := workspace(t, base, alice)
	pass := projectPass(t, base, alice, project, "sandbox")
	secret, keyID := issueKey(t, base, alice, project, map[string]any{"name": "ci", "environment": "sandbox"})
	if !strings.HasPrefix(secret, "alg_sk_sandbox_") {
		t.Fatalf("key shape: %q", secret[:12])
	}

	// The key lists without its secret.
	status, _, out := browserCall(t, "GET", base+"/api/v1/projects/"+project+"/keys", alice, nil)
	if status != 200 || strings.Contains(stringify(out), secret) {
		t.Fatalf("listing keys must never show a secret: %d", status)
	}

	// A paid sandbox request with the key runs under the project's pass.
	window := "e2e-key-" + uuid.NewString()
	status, _, out = call(t, "POST", base+"/api/v1/execute", secret, priceRequest(window))
	if status != 200 || out["delivered"] != true {
		t.Fatalf("execute with key: %d %v", status, out)
	}
	intent := out["intent"].(map[string]any)
	if intent["project_id"] != project || intent["spend_pass_id"] != pass {
		t.Fatalf("the intent belongs to the project's pass: %v", intent)
	}
	paid := intent["committed_minor"]

	// The same request again pays nothing more.
	status, _, out = call(t, "POST", base+"/api/v1/execute", secret, priceRequest(window))
	if status != 200 {
		t.Fatalf("repeat: %d %v", status, out)
	}
	again := out["intent"].(map[string]any)
	if again["id"] != intent["id"] || again["committed_minor"] != paid || again["attempts"] != float64(1) {
		t.Fatalf("a repeat must be the same intent with one attempt: %v", again)
	}

	// A sandbox key can't reach devnet.
	req := priceRequest("e2e-key-devnet-" + uuid.NewString())
	req["constraints"] = map[string]any{"allowed_networks": []string{"solana-devnet"}}
	status, _, out = call(t, "POST", base+"/api/v1/execute", secret, req)
	if status != 403 || !strings.Contains(stringify(out["reason_codes"]), "ENVIRONMENT_NETWORK_MISMATCH") {
		t.Fatalf("sandbox key asking for devnet: %d %v", status, out)
	}

	// A key can't manage keys, even its own project's.
	status, _, _ = call(t, "POST", base+"/api/v1/projects/"+project+"/keys", secret, map[string]any{"name": "x", "environment": "sandbox"})
	if status != 403 {
		t.Fatalf("a key minting keys: %d", status)
	}

	// Rotation: the new secret works, the old one stops.
	status, _, out = browserCall(t, "POST", base+"/api/v1/keys/"+keyID+"/rotate", alice, nil)
	if status != 201 {
		t.Fatalf("rotate: %d %v", status, out)
	}
	rotated := out["key"].(map[string]any)["secret"].(string)
	if status, _, _ := call(t, "GET", base+"/api/v1/pass", secret, nil); status != 401 {
		t.Fatalf("the rotated-out key must stop working: %d", status)
	}
	if status, _, out := call(t, "GET", base+"/api/v1/pass", rotated, nil); status != 200 || out["id"] != pass {
		t.Fatalf("the new key reads its project's pass: %d %v", status, out)
	}
}

func TestTenancyOverHTTP_ScopesAndMissingPasses(t *testing.T) {
	_, base := serve(t)
	alice := signUp(t, base, "Alice")
	_, project := workspace(t, base, alice)
	projectPass(t, base, alice, project, "sandbox")

	readOnly, _ := issueKey(t, base, alice, project, map[string]any{"name": "dashboard", "environment": "sandbox", "scopes": []string{"read"}})
	status, _, _ := call(t, "POST", base+"/api/v1/execute", readOnly, priceRequest("e2e-ro-"+uuid.NewString()))
	if status != 403 {
		t.Fatalf("a read-only key executing: %d", status)
	}
	if status, _, _ := call(t, "GET", base+"/api/v1/pass", readOnly, nil); status != 200 {
		t.Fatalf("a read-only key reading its pass: %d", status)
	}

	// A devnet key in a project that has only a sandbox pass has nothing to spend with.
	devnet, _ := issueKey(t, base, alice, project, map[string]any{"name": "devnet", "environment": "devnet"})
	status, _, out := call(t, "POST", base+"/api/v1/execute", devnet, priceRequest("e2e-dev-"+uuid.NewString()))
	if status != 409 {
		t.Fatalf("devnet key without a devnet pass: %d %v", status, out)
	}
}

func TestTenancyOverHTTP_NothingCrossesBetweenPeople(t *testing.T) {
	_, base := serve(t)
	alice, bob := signUp(t, base, "Alice"), signUp(t, base, "Bob")
	aliceOrg, aliceProject := workspace(t, base, alice)
	_, bobProject := workspace(t, base, bob)
	alicePass := projectPass(t, base, alice, aliceProject, "sandbox")
	projectPass(t, base, bob, bobProject, "sandbox")
	_, aliceKeyID := issueKey(t, base, alice, aliceProject, map[string]any{"name": "ci", "environment": "sandbox"})
	bobKey, _ := issueKey(t, base, bob, bobProject, map[string]any{"name": "ci", "environment": "sandbox"})

	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/orgs/" + aliceOrg + "/projects"},
		{"GET", "/api/v1/projects/" + aliceProject + "/keys"},
		{"GET", "/api/v1/projects/" + aliceProject + "/passes"},
		{"GET", "/api/v1/projects/" + aliceProject + "/events"},
		{"POST", "/api/v1/keys/" + aliceKeyID + "/revoke"},
		{"POST", "/api/v1/keys/" + aliceKeyID + "/rotate"},
	} {
		if status, _, _ := browserCall(t, c.method, base+c.path, bob, nil); status != 404 {
			t.Errorf("Bob %s %s: %d, want 404", c.method, c.path, status)
		}
	}
	if status, _, _ := browserCall(t, "POST", base+"/api/v1/orgs/"+aliceOrg+"/projects", bob, map[string]any{"name": "mine now"}); status != 404 {
		t.Errorf("Bob adding a project to Alice's org: %d", status)
	}
	// A pass for someone else's project can't be made.
	if status, _, _ := browserCall(t, "POST", base+"/api/v1/me/passes", bob, map[string]any{
		"label": "x", "currency": "USDC", "budget_minor_units": 1000, "budget_period": "total", "project_id": aliceProject, "environment": "sandbox",
	}); status != 404 {
		t.Errorf("Bob making a pass in Alice's project: %d", status)
	}
	// Bob's key can't spend through Alice's pass.
	req := priceRequest("e2e-cross-" + uuid.NewString())
	req["spend_pass_id"] = alicePass
	if status, _, out := call(t, "POST", base+"/api/v1/execute", bobKey, req); status != 404 {
		t.Errorf("Bob's key naming Alice's pass: %d %v", status, out)
	}
}

func stringify(v any) string {
	switch x := v.(type) {
	case string:
		return x
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

// A project's default strategy ranks its keys' requests; a strategy named in a
// request wins; "balanced" is the router's AUTO.
func TestTenancyOverHTTP_AProjectDefaultsTheStrategy(t *testing.T) {
	_, base := serve(t)
	alice := signUp(t, base, "Alice")
	_, project := workspace(t, base, alice)
	projectPass(t, base, alice, project, "sandbox")
	secret, _ := issueKey(t, base, alice, project, map[string]any{"name": "ci", "environment": "sandbox"})

	mode := func(strategy string) string {
		req := freshPriceRequest("e2e-strat-" + uuid.NewString())
		if strategy != "" {
			req["provider_policy"] = map[string]any{"strategy": strategy}
		}
		status, _, out := call(t, "POST", base+"/api/v1/execute", secret, req)
		if status != 200 {
			t.Fatalf("execute (%q): %d %v", strategy, status, out)
		}
		routing, _ := out["routing"].(map[string]any)
		m, _ := routing["mode"].(string)
		return m
	}
	if got := mode(""); got != "AUTO" {
		t.Fatalf("a project with no default: %q, want AUTO", got)
	}
	if status, _, out := browserCall(t, "PATCH", base+"/api/v1/projects/"+project, alice, map[string]any{"default_strategy": "cheapest"}); status != 200 {
		t.Fatalf("set default: %d %v", status, out)
	}
	if got := mode(""); got != "CHEAPEST" {
		t.Errorf("the project default: %q, want CHEAPEST", got)
	}
	if got := mode("fastest"); got != "FASTEST" {
		t.Errorf("a named strategy wins: %q, want FASTEST", got)
	}
	if got := mode("balanced"); got != "AUTO" {
		t.Errorf("balanced: %q, want AUTO", got)
	}
}
