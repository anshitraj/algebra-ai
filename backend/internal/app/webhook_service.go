package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/domain/audit"
)

// WebhookStore persists received webhooks, deduplicated on (provider,
// event_id) — see migrations/0001_init.sql's UNIQUE constraint and
// postgres.WebhookRepo.
type WebhookStore interface {
	Insert(ctx context.Context, id, provider, eventID string, payload []byte, verified bool, receivedAt time.Time) (isNew bool, err error)
}

// WebhookResult tells the REST handler what happened, so it can pick the
// right response: reject (signature invalid), or accept as new-or-replay.
type WebhookResult struct {
	Duplicate bool
}

// SecretLookup returns the shared secret for a provider, or "" if none is
// configured — no real webhook-sending provider (a payment gateway, a
// merchant) is integrated in this build, so every lookup returns "" until
// one is (mandate §47: webhook spoofing must be rejected, not trusted by
// default — an unconfigured secret means every webhook for that provider is
// rejected, never silently accepted).
type SecretLookup func(provider string) string

type WebhookService struct {
	store  WebhookStore
	audit  audit.Logger
	secret SecretLookup
	now    func() time.Time
}

func NewWebhookService(store WebhookStore, auditLogger audit.Logger, secret SecretLookup) *WebhookService {
	return &WebhookService{store: store, audit: auditLogger, secret: secret, now: time.Now}
}

// Receive verifies, deduplicates, and persists an inbound webhook. It never
// returns success for an unverified signature — the REST handler maps a
// non-nil error here straight to 401, and the rejection itself is audited
// (mandate §47/§50).
func (s *WebhookService) Receive(ctx context.Context, provider, signatureHeader string, payload []byte, eventIDHeader string) (*WebhookResult, error) {
	secret := s.secret(provider)
	if secret == "" || !VerifyHMACSignature(payload, signatureHeader, secret) {
		evt := audit.NewEvent("WebhookRejected", s.now())
		evt.Result = "signature verification failed or no secret configured for provider " + provider
		evt.Metadata = map[string]any{"provider": provider}
		_ = s.audit.Record(ctx, evt)
		return nil, fmt.Errorf("webhook: signature verification failed for provider %q", provider)
	}

	eventID := eventIDHeader
	if eventID == "" {
		eventID = payloadFingerprint(payload)
	}

	isNew, err := s.store.Insert(ctx, newID("webhook"), provider, eventID, payload, true, s.now())
	if err != nil {
		return nil, fmt.Errorf("webhook: persisting: %w", err)
	}

	evt := audit.NewEvent("WebhookReceived", s.now())
	evt.Result = provider
	evt.Metadata = map[string]any{"provider": provider, "event_id": eventID, "duplicate": !isNew}
	_ = s.audit.Record(ctx, evt)

	return &WebhookResult{Duplicate: !isNew}, nil
}

// VerifyHMACSignature checks payload against a "sha256=<hex>" HMAC-SHA256
// signature header — a generic, documented reference scheme (matching the
// common Stripe/GitHub-style convention) for this build, since no real
// webhook-sending provider is integrated yet to dictate its own scheme.
// Wiring up a real provider (Stripe, Circle, a merchant) means adapting
// this to whatever header format and algorithm THAT provider actually
// uses — never assume this generic scheme matches a real one without
// checking that provider's docs.
func VerifyHMACSignature(payload []byte, signatureHeader, secret string) bool {
	const prefix = "sha256="
	if !strings.HasPrefix(signatureHeader, prefix) {
		return false
	}
	got := strings.TrimPrefix(signatureHeader, prefix)
	if got == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(got), []byte(expected))
}

// SignHMAC is VerifyHMACSignature's sending-side counterpart, used by
// WebhookDispatchService to sign outbound payloads to a tenant's registered
// endpoint with the same generic HMAC-SHA256 reference scheme. Returns the
// raw hex digest — callers prefix it with "sha256=" for the header value,
// matching what VerifyHMACSignature expects to receive.
func SignHMAC(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func payloadFingerprint(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
