package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Plans, the trial, project usage and Book a Demo over real HTTP.

const testOperatorToken = "e2e-operator-token-0123456789abcdef0123"

// operatorGet is a GET carrying the operator token.
func operatorGet(t *testing.T, url, token string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if token != "" {
		req.Header.Set("X-Algebra-Operator-Token", token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	b, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(bytes.TrimSpace(b), &out)
	return resp.StatusCode, out
}

func TestPlansOverHTTP_ThePricingPageNeverClaimsLiveBilling(t *testing.T) {
	_, base := serve(t)
	status, _, out := browserCall(t, "GET", base+"/api/v1/plans", nil, nil)
	if status != 200 {
		t.Fatalf("plans: %d %v", status, out)
	}
	if out["billing_live"] != false || out["trial_plan"] != "startup" || out["trial_days"] != float64(7) {
		t.Fatalf("plans must say billing is not live and the trial is seven days of Startup: %v", out)
	}
	want := map[string]float64{"free": 0, "builder": 1900, "startup": 5900, "business": 19900, "enterprise": -1}
	plans, _ := out["plans"].([]any)
	if len(plans) != len(want) {
		t.Fatalf("plans: %v", plans)
	}
	for _, p := range plans {
		plan := p.(map[string]any)
		if price, ok := want[plan["id"].(string)]; !ok || plan["monthly_usd_cents"] != price {
			t.Fatalf("plan %v: want %v cents", plan, price)
		}
	}
}

func TestTrialOverHTTP_SevenDaysOfStartupOncePerOrganization(t *testing.T) {
	_, base := serve(t)
	alice := signUp(t, base, "Alice")
	org, _ := workspace(t, base, alice)

	// Free includes one project: the workspace's first one is it.
	status, _, out := browserCall(t, "POST", base+"/api/v1/orgs/"+org+"/projects", alice, map[string]any{"name": "second"})
	if status != 409 || !strings.Contains(stringify(out), "Free plan includes 1 project") {
		t.Fatalf("a second project on Free must be refused, naming the limit: %d %v", status, out)
	}

	// Someone else can't start Alice's trial, and neither can a key.
	bob := signUp(t, base, "Bob")
	if status, _, _ := browserCall(t, "POST", base+"/api/v1/orgs/"+org+"/trial", bob, nil); status != 404 {
		t.Fatalf("another person's organization must be not found: %d", status)
	}
	_, project := workspace(t, base, alice)
	secret, _ := issueKey(t, base, alice, project, map[string]any{"name": "ci", "environment": "sandbox"})
	if status, _, _ := call(t, "POST", base+"/api/v1/orgs/"+org+"/trial", secret, nil); status != 403 {
		t.Fatalf("an API key must never start a trial: %d", status)
	}

	status, _, out = browserCall(t, "POST", base+"/api/v1/orgs/"+org+"/trial", alice, nil)
	if status != 200 || out["effective_plan"] != "startup" || out["trial_active"] != true || out["plan"] != "free" || out["trial_available"] != false {
		t.Fatalf("trial: %d %v", status, out)
	}
	if out["trial_ends_at"] == nil {
		t.Fatalf("a trial has an end: %v", out)
	}

	// The trial's limits apply at once.
	status, _, out = browserCall(t, "POST", base+"/api/v1/orgs/"+org+"/projects", alice, map[string]any{"name": "second"})
	if status != 201 {
		t.Fatalf("a second project during the trial: %d %v", status, out)
	}

	// One trial each.
	if status, _, out := browserCall(t, "POST", base+"/api/v1/orgs/"+org+"/trial", alice, nil); status != 409 {
		t.Fatalf("a second trial must be refused: %d %v", status, out)
	}
	status, _, out = browserCall(t, "GET", base+"/api/v1/orgs", alice, nil)
	got := out["organizations"].([]any)[0].(map[string]any)
	if status != 200 || got["effective_plan"] != "startup" || got["entitlements"].(map[string]any)["projects"] != float64(10) {
		t.Fatalf("the workspace shows the plan in force: %v", got)
	}
}

func TestUsageOverHTTP_AProjectsSandboxRunsAreCountedButNotBilled(t *testing.T) {
	_, base := serve(t)
	alice := signUp(t, base, "Alice")
	_, project := workspace(t, base, alice)
	projectPass(t, base, alice, project, "sandbox")
	secret, _ := issueKey(t, base, alice, project, map[string]any{"name": "ci", "environment": "sandbox"})

	status, _, out := browserCall(t, "GET", base+"/api/v1/projects/"+project+"/usage", alice, nil)
	if status != 200 || out["requests"] != float64(0) || out["plan"] != "free" || out["included_executions"] != float64(100) {
		t.Fatalf("usage before anything ran: %d %v", status, out)
	}

	window := "e2e-usage-" + uuid.NewString()
	status, _, run := call(t, "POST", base+"/api/v1/execute", secret, priceRequest(window))
	if status != 200 || run["delivered"] != true {
		t.Fatalf("execute: %d %v", status, run)
	}
	paid := run["intent"].(map[string]any)["committed_minor"]
	// The same request again is the same intent: counted once.
	if status, _, _ := call(t, "POST", base+"/api/v1/execute", secret, priceRequest(window)); status != 200 {
		t.Fatalf("repeat: %d", status)
	}

	status, _, out = browserCall(t, "GET", base+"/api/v1/projects/"+project+"/usage", alice, nil)
	if status != 200 || out["requests"] != float64(1) || out["managed_executions"] != float64(0) {
		t.Fatalf("one request, and sandbox runs are not managed executions: %d %v", status, out)
	}
	nets, _ := out["by_network"].([]any)
	if len(nets) != 1 {
		t.Fatalf("by_network: %v", out)
	}
	n := nets[0].(map[string]any)
	if n["environment"] != "sandbox" || n["executions"] != float64(1) || n["spent_minor"] != paid {
		t.Fatalf("sandbox spend must equal what the intent committed (%v): %v", paid, n)
	}

	// Usage is the project's members' only.
	bob := signUp(t, base, "Bob")
	if status, _, _ := browserCall(t, "GET", base+"/api/v1/projects/"+project+"/usage", bob, nil); status != 404 {
		t.Fatalf("another person's project usage must be not found: %d", status)
	}
	if status, _, _ := call(t, "GET", base+"/api/v1/projects/"+project+"/usage", secret, nil); status != 403 {
		t.Fatalf("usage is read in the console, not with a key: %d", status)
	}
}

func TestLeadsOverHTTP_AnEnquiryIsStoredAndOnlyTheOperatorReadsIt(t *testing.T) {
	_, base := serveWith(t, map[string]string{"ALGEBRA_OPERATOR_TOKEN": testOperatorToken})
	business := "Acme Agents " + uuid.NewString()
	lead := map[string]any{
		"business_name": business, "work_email": "dana@acme.example", "website": "acme.example",
		"use_case": "Our support agent buys enrichment data.\n<script>alert(1)</script>", "estimated_usage": "1k-10k", "message": "",
	}
	status, _, out := browserCall(t, "POST", base+"/api/v1/leads", nil, lead)
	if status != 201 || out["received"] != true || out["id"] == nil {
		t.Fatalf("lead: %d %v", status, out)
	}

	// Refused, each with its reason, and nothing stored.
	for name, change := range map[string]map[string]any{
		"no email":      {"work_email": "not-an-email"},
		"no business":   {"business_name": "  "},
		"no use case":   {"use_case": ""},
		"bad usage":     {"estimated_usage": "lots"},
		"bad website":   {"website": "javascript:alert(1)"},
		"long use case": {"use_case": strings.Repeat("x", 2001)},
	} {
		bad := map[string]any{}
		for k, v := range lead {
			bad[k] = v
		}
		for k, v := range change {
			bad[k] = v
		}
		if status, _, out := browserCall(t, "POST", base+"/api/v1/leads", nil, bad); status != 400 {
			t.Fatalf("%s: want 400, got %d %v", name, status, out)
		}
	}

	// A bot that fills the hidden field is answered and dropped.
	bot := map[string]any{"business_name": "Bot " + business, "work_email": "b@bot.example", "use_case": "x", "company": "filled"}
	if status, _, out := browserCall(t, "POST", base+"/api/v1/leads", nil, bot); status != 201 || out["id"] != nil {
		t.Fatalf("honeypot: %d %v", status, out)
	}

	// Reading needs the operator token: no token, a wrong one and a session all fail.
	if status, _ := operatorGet(t, base+"/api/v1/leads", ""); status != 401 {
		t.Fatalf("leads without the operator token: %d", status)
	}
	if status, _ := operatorGet(t, base+"/api/v1/leads", "wrong-"+testOperatorToken); status != 401 {
		t.Fatalf("leads with a wrong token: %d", status)
	}
	alice := signUp(t, base, "Alice")
	if status, _, _ := browserCall(t, "GET", base+"/api/v1/leads", alice, nil); status != 401 {
		t.Fatalf("a signed-in person is not the operator: %d", status)
	}

	status, got := operatorGet(t, base+"/api/v1/leads?limit=500", testOperatorToken)
	if status != 200 {
		t.Fatalf("operator read: %d %v", status, got)
	}
	found := 0
	for _, l := range got["leads"].([]any) {
		row := l.(map[string]any)
		if strings.Contains(row["business_name"].(string), business) {
			found++
			if row["business_name"] != business || row["website"] != "https://acme.example" || row["status"] != "new" ||
				!strings.Contains(row["use_case"].(string), "<script>") || row["source_ip"] != nil {
				t.Fatalf("stored as sent (text, never markup), without the address: %v", row)
			}
		}
	}
	if found != 1 {
		t.Fatalf("exactly the one real enquiry is stored, not the refused ones or the bot's: %d", found)
	}
}
