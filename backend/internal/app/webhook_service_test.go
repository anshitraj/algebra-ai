package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"testing"
	"time"
)

type fakeWebhookStore struct {
	mu   sync.Mutex
	seen map[string]bool // provider|event_id
}

func newFakeWebhookStore() *fakeWebhookStore { return &fakeWebhookStore{seen: map[string]bool{}} }

func (f *fakeWebhookStore) Insert(_ context.Context, _ string, provider, eventID string, _ []byte, _ bool, _ time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := provider + "|" + eventID
	if f.seen[key] {
		return false, nil
	}
	f.seen[key] = true
	return true, nil
}

func sign(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyHMACSignature_CorrectSecretPasses(t *testing.T) {
	payload := []byte(`{"event":"order.paid"}`)
	sig := sign(payload, "shhh")
	if !VerifyHMACSignature(payload, sig, "shhh") {
		t.Error("expected a correctly signed payload to verify")
	}
}

func TestVerifyHMACSignature_WrongSecretFails(t *testing.T) {
	payload := []byte(`{"event":"order.paid"}`)
	sig := sign(payload, "shhh")
	if VerifyHMACSignature(payload, sig, "different-secret") {
		t.Error("expected verification to fail with the wrong secret")
	}
}

func TestVerifyHMACSignature_TamperedPayloadFails(t *testing.T) {
	payload := []byte(`{"event":"order.paid","amount":100}`)
	sig := sign(payload, "shhh")
	tampered := []byte(`{"event":"order.paid","amount":999999}`)
	if VerifyHMACSignature(tampered, sig, "shhh") {
		t.Error("expected verification to fail for a tampered payload")
	}
}

func TestVerifyHMACSignature_MissingPrefixFails(t *testing.T) {
	payload := []byte("body")
	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write(payload)
	rawHex := hex.EncodeToString(mac.Sum(nil))
	if VerifyHMACSignature(payload, rawHex, "secret") {
		t.Error("expected a signature header without the sha256= prefix to be rejected")
	}
}

func TestWebhookService_RejectsWhenNoSecretConfigured(t *testing.T) {
	store := newFakeWebhookStore()
	audit := newFakeAuditLogger()
	svc := NewWebhookService(store, audit, func(string) string { return "" })

	_, err := svc.Receive(context.Background(), "unconfigured-provider", "sha256=whatever", []byte("body"), "evt-1")
	if err == nil {
		t.Fatal("expected an error when no secret is configured for the provider")
	}
	found := false
	for _, e := range audit.all() {
		if e.Action == "WebhookRejected" {
			found = true
		}
	}
	if !found {
		t.Error("expected a WebhookRejected audit event")
	}
}

func TestWebhookService_RejectsBadSignature(t *testing.T) {
	store := newFakeWebhookStore()
	svc := NewWebhookService(store, newFakeAuditLogger(), func(string) string { return "real-secret" })

	_, err := svc.Receive(context.Background(), "stripe", "sha256=deadbeef", []byte("body"), "evt-1")
	if err == nil {
		t.Fatal("expected an error for a bad signature")
	}
}

func TestWebhookService_AcceptsValidSignatureAndDedupes(t *testing.T) {
	store := newFakeWebhookStore()
	audit := newFakeAuditLogger()
	svc := NewWebhookService(store, audit, func(string) string { return "real-secret" })

	payload := []byte(`{"order_id":"ord_1","status":"paid"}`)
	sig := sign(payload, "real-secret")

	first, err := svc.Receive(context.Background(), "stripe", sig, payload, "evt-1")
	if err != nil {
		t.Fatalf("expected the first delivery to succeed: %v", err)
	}
	if first.Duplicate {
		t.Error("expected the first delivery to NOT be flagged duplicate")
	}

	second, err := svc.Receive(context.Background(), "stripe", sig, payload, "evt-1")
	if err != nil {
		t.Fatalf("expected a replayed delivery to still succeed (idempotent), got error: %v", err)
	}
	if !second.Duplicate {
		t.Error("expected the second identical delivery to be flagged duplicate")
	}
}

func TestWebhookService_FallsBackToPayloadFingerprintWhenNoEventID(t *testing.T) {
	store := newFakeWebhookStore()
	svc := NewWebhookService(store, newFakeAuditLogger(), func(string) string { return "real-secret" })

	payload := []byte(`{"order_id":"ord_2"}`)
	sig := sign(payload, "real-secret")

	first, err := svc.Receive(context.Background(), "generic", sig, payload, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if first.Duplicate {
		t.Error("expected first delivery to not be a duplicate")
	}

	// Same payload again, still no event ID header — must still dedupe via
	// the payload's own fingerprint.
	second, err := svc.Receive(context.Background(), "generic", sig, payload, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !second.Duplicate {
		t.Error("expected the identical payload (no event ID) to be recognized as a duplicate via fingerprint")
	}
}
