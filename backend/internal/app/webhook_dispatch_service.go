package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/project-algebra/algebra/internal/domain/tenant"
)

// WebhookDispatchService is the outbound-sending half of the webhook
// system — internal/app/webhook_service.go only ever received. Dispatch
// does the fast part (list endpoints, marshal the payload) on the caller's
// context, then hands each delivery off to its own background goroutine on
// a detached context — so a slow or unreachable tenant endpoint never adds
// latency to the request that triggered the event, and callers never need
// to remember to run this asynchronously themselves.
//
// There is no durable retry queue in this build: an attempt that exhausts
// its retries while the process is up is simply lost. This is a documented
// Phase 1 gap, the same kind idempotency_repo.go's own doc comment already
// carries for a crashed IN_PROGRESS reservation — a production deployment
// needs a real queue (SQS, Cloud Tasks, ...) behind this.
type WebhookDispatchService struct {
	endpoints WebhookEndpointStore
	client    *http.Client
	now       func() time.Time
}

func NewWebhookDispatchService(endpoints WebhookEndpointStore) *WebhookDispatchService {
	return &WebhookDispatchService{
		endpoints: endpoints,
		client:    &http.Client{Timeout: 10 * time.Second},
		now:       time.Now,
	}
}

// CreateEndpoint registers a new webhook destination for tenantID and
// returns its generated shared secret exactly once — the REST/MCP layer
// never sees it again after this call, same posture as a bearer token.
func (s *WebhookDispatchService) CreateEndpoint(ctx context.Context, tenantID, url string, eventTypes []string) (*tenant.WebhookEndpoint, string, error) {
	if eventTypes == nil {
		// tenant_webhook_endpoints.event_types is TEXT[] NOT NULL — a Go nil
		// slice binds as SQL NULL (the column's DEFAULT '{}' only applies
		// when the column is omitted from the INSERT, not when NULL is
		// passed explicitly), so this must be a real empty slice, not nil.
		eventTypes = []string{}
	}
	secret, err := generateWebhookSecret()
	if err != nil {
		return nil, "", fmt.Errorf("app: generating webhook secret: %w", err)
	}
	ep := &tenant.WebhookEndpoint{
		ID: newID("whep"), TenantID: tenantID, URL: url, Secret: secret,
		EventTypes: eventTypes, CreatedAt: s.now(),
	}
	if err := s.endpoints.Create(ctx, ep); err != nil {
		return nil, "", fmt.Errorf("app: persisting webhook endpoint: %w", err)
	}
	return ep, secret, nil
}

func generateWebhookSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "whsec_" + hex.EncodeToString(buf), nil
}

// Dispatch signs and POSTs payload to every one of tenantID's registered
// endpoints that wants eventType. Individual delivery failures are never
// returned to the caller — the event that triggered dispatch has already
// happened and must not be unwound by a delivery failure, the same
// principle transition.go's recordAudit already applies to audit-write
// failures.
func (s *WebhookDispatchService) Dispatch(ctx context.Context, tenantID, eventType string, payload any) error {
	endpoints, err := s.endpoints.ListActiveByTenant(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("app: listing webhook endpoints: %w", err)
	}
	body, err := json.Marshal(struct {
		EventType string `json:"event_type"`
		Data      any    `json:"data"`
	}{EventType: eventType, Data: payload})
	if err != nil {
		return fmt.Errorf("app: marshaling webhook payload: %w", err)
	}

	eventID := newID("evt")
	for _, ep := range endpoints {
		if !ep.Wants(eventType) {
			continue
		}
		go s.deliverWithRetry(context.Background(), ep, eventID, eventType, body)
	}
	return nil
}

func (s *WebhookDispatchService) deliverWithRetry(ctx context.Context, ep tenant.WebhookEndpoint, eventID, eventType string, body []byte) {
	backoff := 500 * time.Millisecond
	for attempt := 1; attempt <= 3; attempt++ {
		if s.deliverOnce(ctx, ep, eventID, eventType, body) {
			return
		}
		if attempt < 3 {
			time.Sleep(backoff)
			backoff *= 2
		}
	}
}

func (s *WebhookDispatchService) deliverOnce(ctx context.Context, ep tenant.WebhookEndpoint, eventID, eventType string, body []byte) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.URL, bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Algebra-Signature", "sha256="+SignHMAC(body, ep.Secret))
	req.Header.Set("X-Algebra-Event-ID", eventID)
	req.Header.Set("X-Algebra-Event-Type", eventType)

	resp, err := s.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}
