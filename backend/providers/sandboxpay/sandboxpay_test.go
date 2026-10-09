package sandboxpay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/providers/x402"
)

func serve(p *Provider) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /x", p.Serve)
	mux.HandleFunc("GET /x/operations/{key}", p.ServeOperation)
	return httptest.NewServer(mux)
}

func call(t *testing.T, url string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(`{"mint":"SOL"}`))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, b
}

func TestSandbox_PayOnceAndProveIt(t *testing.T) {
	ctx := context.Background()
	rail := NewRail(time.Minute)
	p := NewProvider(rail, "http://sandbox.test/x", 3_000)
	srv := serve(p)
	defer srv.Close()

	res, body := call(t, srv.URL+"/x", nil)
	if res.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("unpaid call should get 402, got %d", res.StatusCode)
	}
	rv := &econ.Reservation{ID: "rsv_1", HoldMinor: 5_000, IdempotencyKey: "eint_1-a1"}
	auth, err := rail.Authorize(ctx, rv, app.PaymentRequest{Requirements: body})
	if err != nil {
		t.Fatal(err)
	}
	if auth.AmountMinor != 3_000 || !auth.Evidence.Test || auth.Evidence.Network != Network {
		t.Fatalf("authority must be the provider's price and marked test: %+v", auth)
	}
	if st, _ := rail.Settlement(ctx, auth.Evidence); st.Status != app.SettlementPending {
		t.Fatalf("authorized but uncaptured is pending, got %s", st.Status)
	}
	res, result := call(t, srv.URL+"/x", map[string]string{auth.Header: auth.Value, "Idempotency-Key": rv.IdempotencyKey})
	if res.StatusCode != http.StatusOK || !strings.Contains(string(result), `"sandbox":true`) {
		t.Fatalf("paid call: %d %s", res.StatusCode, result)
	}
	settle, err := x402.DecodeSettle(res.Header.Get(x402.HeaderPaymentResponse))
	if err != nil || !settle.Success {
		t.Fatalf("settle header: %v %+v", err, settle)
	}
	st, _ := rail.Settlement(ctx, auth.Evidence)
	if st.Status != app.SettlementSettled || st.Transaction != settle.Transaction || st.AmountMinor != 3_000 || !st.Test {
		t.Fatalf("rail must prove the capture: %+v", st)
	}
	// A replay with the same key returns the result without charging again.
	res, again := call(t, srv.URL+"/x", map[string]string{auth.Header: auth.Value, "Idempotency-Key": rv.IdempotencyKey})
	if res.StatusCode != http.StatusOK || string(again) != string(result) {
		t.Fatalf("replay should return the stored result: %d", res.StatusCode)
	}
	// The same authority can't be captured twice under a new key.
	if res, _ := call(t, srv.URL+"/x", map[string]string{auth.Header: auth.Value, "Idempotency-Key": "other"}); res.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("double capture must be refused, got %d", res.StatusCode)
	}
}

func TestSandbox_DroppedResponseIsRecoverable(t *testing.T) {
	ctx := context.Background()
	rail := NewRail(time.Minute)
	p := NewProvider(rail, "http://sandbox.test/x", 3_000)
	srv := serve(p)
	defer srv.Close()
	_, challenge := call(t, srv.URL+"/x", nil)
	rv := econ.Reservation{ID: "rsv_2", HoldMinor: 3_000, IdempotencyKey: "eint_2-a1"}
	auth, err := rail.Authorize(ctx, &rv, app.PaymentRequest{Requirements: challenge})
	if err != nil {
		t.Fatal(err)
	}
	res, _ := call(t, srv.URL+"/x", map[string]string{auth.Header: auth.Value, "Idempotency-Key": rv.IdempotencyKey, "X-Sandbox-Fault": "drop_response"})
	if res.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("fault should drop the response, got %d", res.StatusCode)
	}
	if st, _ := rail.Settlement(ctx, auth.Evidence); st.Status != app.SettlementSettled {
		t.Fatalf("the money moved even though the caller never heard: %s", st.Status)
	}
	rec, _ := p.Recover(ctx, rv)
	if rec.Status != app.RecoveryFulfilled || !strings.HasPrefix(rec.ResultHash, "sha256:") {
		t.Fatalf("the provider can say what happened: %+v", rec)
	}
	var op map[string]any
	r, _ := http.Get(srv.URL + "/x/operations/" + rv.IdempotencyKey)
	_ = json.NewDecoder(r.Body).Decode(&op)
	r.Body.Close()
	if op["status"] != "fulfilled" {
		t.Errorf("operations endpoint: %v", op)
	}
}

func TestSandbox_ProvesNonSettlementOnlyWhenItCan(t *testing.T) {
	ctx := context.Background()
	rail := NewRail(time.Minute)
	clock := time.Now()
	rail.now = func() time.Time { return clock }
	p := NewProvider(rail, "http://sandbox.test/x", 3_000)
	reqs, _ := json.Marshal(x402.Challenge{Version: 1, Accepts: []x402.Requirements{p.requirements()}})
	auth, err := rail.Authorize(ctx, &econ.Reservation{ID: "rsv_3", HoldMinor: 3_000}, app.PaymentRequest{Requirements: reqs})
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(2 * time.Minute)
	if st, _ := rail.Settlement(ctx, auth.Evidence); st.Status != app.SettlementNotSettled {
		t.Fatalf("an expired, uncaptured authority can never land: %s", st.Status)
	}
	if _, err := rail.Capture(auth.Value, p.payTo, p.price); err == nil {
		t.Fatal("an expired authority must not be capturable")
	}
	if st, _ := rail.Settlement(ctx, econ.Evidence{PaymentID: "sbxpay_never_seen"}); st.Status != app.SettlementUnknown {
		t.Fatalf("no record is UNKNOWN, never assumed unpaid: %s", st.Status)
	}
	// A forged authority is refused.
	forged, _ := x402.PaymentPayload{Version: 1, Scheme: "exact", Network: Network, Payload: json.RawMessage(`{"payment_id":"x","amount":3000,"pay_to":"sandbox-provider-token-risk","mac":"00"}`)}.Encode()
	if _, err := rail.Capture(forged, p.payTo, p.price); err == nil {
		t.Fatal("a forged authority must be refused")
	}
}
