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

// callWithHeader is call with more request headers.
func callWithHeader(t *testing.T, method, url, token string, body any, headers map[string]string) (int, http.Header, map[string]any) {
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
	for k, v := range headers {
		req.Header.Set(k, v)
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

// executeWithKey is POST /execute with an Idempotency-Key.
func executeWithKey(t *testing.T, base, token, key string, body map[string]any) (int, map[string]any) {
	t.Helper()
	status, _, out := callWithHeader(t, "POST", base+"/api/v1/execute", token, body, map[string]string{"Idempotency-Key": key})
	return status, out
}

func intentOf(out map[string]any) map[string]any {
	m, _ := out["intent"].(map[string]any)
	return m
}

// A client that retries with the same Idempotency-Key and, by accident, another `window` pays once: the key is the
// request's identity, and the window can't make it a second request.
func TestIdempotencyKeyOverHTTP_ARetryThatChangesTheWindowPaysOnce(t *testing.T) {
	b, base := serve(t)
	token := issuePass(t, b)
	key := "retry-" + uuid.NewString()

	first := priceRequest("first-try")
	status, one := executeWithKey(t, base, token, key, first)
	if status != 200 || one["delivered"] != true || one["replayed"] != nil {
		t.Fatalf("the first call pays: %d %v", status, one)
	}
	paid := intentOf(one)["committed_minor"]
	if paid == nil || paid == float64(0) {
		t.Fatalf("it paid something: %v", one)
	}

	second := priceRequest("second-try-a-different-window")
	status, two := executeWithKey(t, base, token, key, second)
	if status != 200 || two["replayed"] != true || intentOf(two)["id"] != intentOf(one)["id"] || two["delivered"] != true {
		t.Fatalf("the retry gets the first answer, not a second payment: %d %v", status, two)
	}
	if intentOf(two)["committed_minor"] != paid {
		t.Errorf("one payment: %v then %v", paid, intentOf(two)["committed_minor"])
	}

	// Without the key the same two windows are two requests: that is what the key prevents.
	other := issuePass(t, b)
	_, _, a := call(t, "POST", base+"/api/v1/execute", other, freshPriceRequest("w1"))
	_, _, bb := call(t, "POST", base+"/api/v1/execute", other, freshPriceRequest("w2"))
	if intentOf(a)["id"] == nil || intentOf(a)["id"] == intentOf(bb)["id"] {
		t.Errorf("two windows without a key are two purchases: %v %v", intentOf(a)["id"], intentOf(bb)["id"])
	}
}

// One key is one request: asking for something else under it is refused, and so is any other person's use of it
// kept apart.
func TestIdempotencyKeyOverHTTP_ADifferentRequestUnderTheSameKeyIsRefused(t *testing.T) {
	b, base := serve(t)
	token, stranger := issuePass(t, b), issuePass(t, b)
	key := "one-request-" + uuid.NewString()

	if status, out := executeWithKey(t, base, token, key, priceRequest("a")); status != 200 {
		t.Fatalf("first: %d %v", status, out)
	}
	different := priceRequest("a")
	different["input"] = map[string]any{"mint": "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"}
	status, out := executeWithKey(t, base, token, key, different)
	if status != 409 || out["reason"] != "idempotency_key_reused" || !strings.Contains(out["error"].(string), "different request") {
		t.Fatalf("another input under the same key: %d %v", status, out)
	}
	// The same request in other words (the amount in dollars, spaces in the input) is the same request.
	same := map[string]any{
		"capability": "token.price", "input": map[string]any{"mint": "So11111111111111111111111111111111111111112"},
		"max_usd": "0.05", "window": "whatever",
	}
	if status, out := executeWithKey(t, base, token, key, same); status != 200 || out["replayed"] != true {
		t.Errorf("the same request spelled another way is a replay: %d %v", status, out)
	}
	// Someone else's request with the same key is their own.
	if status, out := executeWithKey(t, base, stranger, key, priceRequest("a")); status != 200 || out["delivered"] != true || out["replayed"] != nil {
		t.Errorf("another person's call under the same key pays for itself: %d %v", status, out)
	}
	// A key that isn't a printable token is refused before anything happens.
	for _, bad := range []string{strings.Repeat("k", 256), "two  spaces"} {
		if status, out := executeWithKey(t, base, token, bad, priceRequest("a")); status != 400 {
			t.Errorf("key %q: %d %v", bad, status, out)
		}
	}
}

// A request refused as malformed doesn't use up its key: correcting it and sending it again works.
func TestIdempotencyKeyOverHTTP_ARefusedMalformedRequestDoesNotSpendTheKey(t *testing.T) {
	b, base := serve(t)
	token := issuePass(t, b)
	key := "typo-" + uuid.NewString()

	bad := priceRequest("w")
	bad["budget_max_minor"] = 0
	if status, out := executeWithKey(t, base, token, key, bad); status != 400 {
		t.Fatalf("a zero budget is a bad request: %d %v", status, out)
	}
	if status, out := executeWithKey(t, base, token, key, priceRequest("w")); status != 200 || out["delivered"] != true {
		t.Errorf("the corrected request under the same key: %d %v", status, out)
	}
}

// Dollars at the edge: max_usd where budget_max_minor goes, and the answer says both.
func TestMaxUSDOverHTTP_AFirstCallNeedsNoMicroUSDCArithmetic(t *testing.T) {
	b, base := serve(t)
	token := issuePass(t, b)

	req := map[string]any{
		"capability": "token.price", "input": map[string]any{"mint": "So11111111111111111111111111111111111111112"},
		"max_usd": "0.05", "window": "usd-" + uuid.NewString(),
	}
	status, _, out := call(t, "POST", base+"/api/v1/execute", token, req)
	in := intentOf(out)
	if status != 200 || out["delivered"] != true || out["budget_max_usd"] != "0.05" || in["budget_max_minor"] != float64(50_000) {
		t.Fatalf("max_usd: %d %v", status, out)
	}
	if out["paid_usd"] == nil || out["paid_usd"] == "0" {
		t.Errorf("what was paid, in dollars: %v", out["paid_usd"])
	}
	// The dry run takes it too.
	sim := map[string]any{"capability": "token.price", "input": req["input"], "max_usd": "0.05", "window": "sim"}
	if status, _, out := call(t, "POST", base+"/api/v1/policy/simulate", token, sim); status != 200 || out["verdict"] == nil {
		t.Errorf("simulate with max_usd: %d %v", status, out)
	}

	for name, tc := range map[string]struct {
		body map[string]any
		want string
	}{
		"a number":     {map[string]any{"max_usd": 0.05}, "not a number"},
		"seven places": {map[string]any{"max_usd": "0.0000001"}, "at most 6 decimals"},
		"two amounts":  {map[string]any{"max_usd": "0.05", "budget_max_minor": 60_000}, "different amounts"},
		"zero":         {map[string]any{"max_usd": "0"}, "positive amount"},
	} {
		body := map[string]any{"capability": "token.price", "input": req["input"], "window": "bad-" + uuid.NewString()}
		for k, v := range tc.body {
			body[k] = v
		}
		status, _, out := call(t, "POST", base+"/api/v1/execute", token, body)
		if status != 400 || !strings.Contains(out["error"].(string), tc.want) {
			t.Errorf("%s: %d %v, want a 400 that says %q", name, status, out, tc.want)
		}
	}
}

// The same refusal reads the same over REST and over MCP.
func TestRefusalsReadTheSameOverRESTAndMCP(t *testing.T) {
	b, base := serve(t)
	// A pass of one cent can't take a 0.05 request: the person's limits say no.
	small, _, _ := issuePassWith(t, b, 10_000)
	for name, tc := range map[string]struct {
		pass       string
		capability string
		status     int
	}{
		"the limits say no":   {small, "token.price", 403},
		"no provider does it": {issuePass(t, b), "nothing.like.this", 422},
	} {
		status, _, rest := call(t, "POST", base+"/api/v1/execute", tc.pass, map[string]any{
			"capability": tc.capability, "input": map[string]any{"mint": "So11111111111111111111111111111111111111112"}, "max_usd": "0.05", "window": "w-" + uuid.NewString(),
		})
		if status != tc.status {
			t.Fatalf("%s: REST answered %d %v", name, status, rest)
		}
		cs := mcpSession(t, base, tc.pass)
		_, text, isErr := callTool(t, cs, "algebra_execute", map[string]any{
			"capability": tc.capability, "input": map[string]any{"mint": "So11111111111111111111111111111111111111112"}, "max_price_usdc": "0.05", "window": "w-" + uuid.NewString(),
		})
		if !isErr || text != rest["error"] {
			t.Errorf("%s:\n REST: %v\n MCP:  %s", name, rest["error"], text)
		}
	}
}
