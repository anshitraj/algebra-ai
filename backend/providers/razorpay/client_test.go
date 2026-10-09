package razorpay

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVerifySubscriptionPayment(t *testing.T) {
	c := New(Config{KeyID: "rzp_test_x", KeySecret: "secret"})
	good := hmacHex("secret", "pay_1|sub_1")
	if err := c.VerifySubscriptionPayment("pay_1", "sub_1", good); err != nil {
		t.Errorf("valid signature rejected: %v", err)
	}
	if err := c.VerifySubscriptionPayment("pay_1", "sub_2", good); err == nil {
		t.Error("signature for another subscription accepted")
	}
	if err := c.VerifySubscriptionPayment("pay_1", "sub_1", ""); err == nil {
		t.Error("empty signature accepted")
	}
}

func TestVerifyWebhook(t *testing.T) {
	body := []byte(`{"event":"subscription.charged"}`)
	c := New(Config{WebhookSecret: "whsec"})
	if err := c.VerifyWebhook(body, hmacHex("whsec", string(body))); err != nil {
		t.Errorf("valid webhook rejected: %v", err)
	}
	if err := c.VerifyWebhook([]byte(`{"event":"subscription.cancelled"}`), hmacHex("whsec", string(body))); err == nil {
		t.Error("tampered body accepted")
	}
	if err := New(Config{}).VerifyWebhook(body, "anything"); err == nil {
		t.Error("webhook accepted with no secret configured")
	}
}

func TestCreateSubscription_SendsAuthAndParsesErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "rzp_test_x" || pass != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":{"code":"BAD_REQUEST_ERROR","description":"Authentication failed"}}`)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path != "/v1/subscriptions" || body["plan_id"] != "plan_1" {
			t.Errorf("unexpected request %s %v", r.URL.Path, body)
		}
		_, _ = io.WriteString(w, `{"id":"sub_123","status":"created","plan_id":"plan_1","short_url":"https://rzp.io/i/x"}`)
	}))
	defer srv.Close()

	c := New(Config{KeyID: "rzp_test_x", KeySecret: "secret", APIBase: srv.URL + "/v1"})
	s, err := c.CreateSubscription(context.Background(), "plan_1", 12, map[string]string{"user_id": "u1"})
	if err != nil || s.ID != "sub_123" || s.Status != "created" {
		t.Fatalf("got %+v, %v", s, err)
	}
	if !c.TestMode() {
		t.Error("rzp_test_ key should report test mode")
	}

	bad := New(Config{KeyID: "rzp_test_x", KeySecret: "wrong", APIBase: srv.URL + "/v1"})
	_, err = bad.CreateSubscription(context.Background(), "plan_1", 12, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 401 || apiErr.Description != "Authentication failed" {
		t.Errorf("want parsed 401 APIError, got %v", err)
	}
}
