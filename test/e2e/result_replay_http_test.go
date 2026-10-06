package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	v1 "github.com/project-algebra/algebra/internal/api/v1"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
	"github.com/project-algebra/algebra/internal/platform/config"
	"github.com/project-algebra/algebra/internal/platform/wiring"
)

// serve runs the real API on a free loopback port, with the sandbox provider
// the API itself hosts reachable on that same port, as in `go run ./cmd/api`.
func serve(t *testing.T) (*wiring.Bundle, string) {
	t.Helper()
	if os.Getenv("DATABASE_URL") == "" || os.Getenv("ALGEBRA_MASTER_KEY") == "" {
		t.Skip("DATABASE_URL / ALGEBRA_MASTER_KEY not set; skipping E2E test (see docs/LOCAL_DEVELOPMENT.md)")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HTTP_ADDR", ln.Addr().String())
	t.Setenv("ECONOMIC_SANDBOX", "on")
	t.Setenv("SOLANA_CLUSTER", "")
	t.Setenv("SOLANA_DEVNET_KEYPAIR_FILE", "")
	t.Setenv("SOLANA_DEVNET_KEYPAIR", "")
	t.Setenv("SOLANA_MAINNET_KEYPAIR_FILE", "")
	t.Setenv("SOLANA_MAINNET_KEYPAIR", "")
	t.Setenv("RESULT_RETENTION", "")
	// Nothing but the sandbox provider is asked for a price: no catalog, so no
	// request leaves this machine.
	t.Setenv("PAYSH_ENABLED", "off")
	t.Setenv("CIRCLE_AGENTS_ENABLED", "off")
	t.Setenv("PAYAI_ENABLED", "off")
	cfg, err := config.FromEnv()
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	b, err := wiring.Build(context.Background(), cfg, "../../migrations")
	if err != nil {
		t.Fatalf("building application: %v", err)
	}
	t.Cleanup(b.DB.Close)
	srv := &http.Server{Handler: v1.NewRouter(b, b.Limiter, nil), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return b, "http://" + ln.Addr().String()
}

// issuePass makes a person with a USDC pass and returns the pass's token.
func issuePass(t *testing.T, b *wiring.Bundle) string {
	t.Helper()
	user, err := b.Users.Create(context.Background(), "replay-"+uuid.NewString()+"@example.test", "")
	if err != nil {
		t.Fatal(err)
	}
	issued, err := b.SpendPasses.Create(context.Background(), user.ID, spendpass.Pass{
		Label: "e2e", AgentKind: spendpass.AgentCustom, Currency: "USDC", BudgetMinorUnits: 1_000_000, BudgetPeriod: spendpass.PeriodTotal,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	return issued.Token
}

func call(t *testing.T, method, url, token string, body any) (int, http.Header, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, url, rd)
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	b, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, resp.Header, out
}

// Asking again for what was already paid for returns the answer that was kept,
// pays nothing, and the answer can be read back: until the request that
// declined keeping it, which gets "already committed" as it always did.
func TestExecuteOverHTTP_ARepeatRequestIsAnsweredFromTheKeptResult(t *testing.T) {
	b, base := serve(t)
	token := issuePass(t, b)
	window := "e2e-" + uuid.NewString()
	req := map[string]any{
		"capability": "solana.token-risk", "input": map[string]any{"mint": "So11111111111111111111111111111111111111112"},
		"budget_max_minor": 50_000, "window": window,
	}

	status, _, first := call(t, "POST", base+"/api/v1/execute", token, req)
	if status != 200 || first["delivered"] != true || first["replayed"] != nil || first["response"] == nil {
		t.Fatalf("first request: %d %v", status, first)
	}
	intent := first["intent"].(map[string]any)
	id, committed := intent["id"].(string), intent["committed_minor"]
	if committed == nil || committed.(float64) <= 0 {
		t.Fatalf("it was paid: %v", intent)
	}

	// The same outcome again.
	status, _, again := call(t, "POST", base+"/api/v1/execute", token, req)
	if status != 200 || again["replayed"] != true || again["delivered"] != true || again["stopped"] != "replayed" {
		t.Fatalf("a repeat request is answered: %d %v", status, again)
	}
	if fmt.Sprint(again["response"]) != fmt.Sprint(first["response"]) || again["receipt"] != first["receipt"] || again["receipt"] == "" {
		t.Errorf("the same answer and the same receipt:\n first %v\n again %v", first["response"], again["response"])
	}
	if again["intent"].(map[string]any)["committed_minor"] != committed || again["intent"].(map[string]any)["attempts"] != intent["attempts"] {
		t.Errorf("nothing was paid again: %v", again["intent"])
	}

	// And it can be read back, for the agent and nobody else.
	status, hdr, got := call(t, "GET", base+"/api/v1/economic-intents/"+id+"/result", token, nil)
	if status != 200 || got["result_is_untrusted"] != nil || got["response_is_untrusted_provider_data"] != true || hdr.Get("Cache-Control") != "no-store" ||
		fmt.Sprint(got["response"]) != fmt.Sprint(first["response"]) || got["expires_at"] == nil {
		t.Fatalf("result: %d %v %v", status, got, hdr)
	}
	other := issuePass(t, b)
	if status, _, _ := call(t, "GET", base+"/api/v1/economic-intents/"+id+"/result", other, nil); status != 404 {
		t.Errorf("another person's agent can't read it: %d", status)
	}
	if status, _, _ := call(t, "GET", base+"/api/v1/economic-intents/"+id+"/result", "not-a-token", nil); status != 401 {
		t.Errorf("no token, no result: %d", status)
	}

	// A request that declines keeping its answer: delivered, then refused on repeat.
	req2 := map[string]any{
		"capability": "solana.token-risk", "input": map[string]any{"mint": "So11111111111111111111111111111111111111112"},
		"budget_max_minor": 50_000, "window": window + "-private", "store_result": false,
	}
	status, _, priv := call(t, "POST", base+"/api/v1/execute", token, req2)
	if status != 200 || priv["delivered"] != true || priv["response"] == nil {
		t.Fatalf("the caller still gets its answer: %d %v", status, priv)
	}
	privID := priv["intent"].(map[string]any)["id"].(string)
	if status, _, again := call(t, "POST", base+"/api/v1/execute", token, req2); status != 409 {
		t.Errorf("nothing was kept, so the repeat is refused as already committed: %d %v", status, again)
	}
	if status, _, _ := call(t, "GET", base+"/api/v1/economic-intents/"+privID+"/result", token, nil); status != 404 {
		t.Errorf("no result for a declined answer: %d", status)
	}
}
