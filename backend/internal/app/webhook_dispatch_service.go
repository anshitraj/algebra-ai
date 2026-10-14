package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/domain/tenant"
	"github.com/project-algebra/algebra/internal/platform/safehttp"
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
	client    WebhookHTTP
	now       func() time.Time
}

// WebhookHTTP is the client deliveries go out through. *safehttp.Client
// satisfies it: it dials only public addresses, speaks only https and never
// follows a redirect, which matters because the destination is a URL a tenant
// chose and the request is made from inside Algebra's own network.
type WebhookHTTP interface {
	Do(req *http.Request) (*safehttp.Response, error)
}

func NewWebhookDispatchService(endpoints WebhookEndpointStore) *WebhookDispatchService {
	return &WebhookDispatchService{
		endpoints: endpoints,
		client:    safehttp.New(safehttp.Options{Timeout: 10 * time.Second}),
		now:       time.Now,
	}
}

// SetHTTPClient replaces the delivery client (tests).
func (s *WebhookDispatchService) SetHTTPClient(c WebhookHTTP) { s.client = c }

// ErrInvalidWebhookURL: the URL can't be a webhook destination.
var ErrInvalidWebhookURL = errors.New("app: invalid webhook URL")

// maxWebhookURL bounds a destination's length.
const maxWebhookURL = 2048

// ValidateWebhookURL accepts only a public https destination: it refuses plain
// http, credentials in the URL, names that only resolve inside a network
// (localhost, .local, .internal, a bare service name) and IP literals in
// private, loopback, link-local or otherwise non-public ranges.
//
// This is the early, readable refusal. It can't see what a public-looking name
// resolves to, so the delivery client re-checks the address it actually dials,
// after DNS, and refuses a non-public one whatever the registered URL says.
func ValidateWebhookURL(raw string) error {
	reject := func(why string) error { return fmt.Errorf("%w: %s", ErrInvalidWebhookURL, why) }
	if len(raw) > maxWebhookURL {
		return reject("it is longer than 2048 characters")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return reject("it isn't a valid URL")
	}
	if u.Scheme != "https" {
		return reject("it must be an https:// URL")
	}
	if u.User != nil {
		return reject("it must not carry a username or password")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return reject("it has no host")
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return reject("its port isn't valid")
		}
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if safehttp.BlockedIP(addr, false) {
			return reject("it points at a private or reserved address")
		}
		return nil
	}
	switch {
	case host == "localhost", strings.HasSuffix(host, ".localhost"),
		strings.HasSuffix(host, ".local"), strings.HasSuffix(host, ".internal"), strings.HasSuffix(host, ".localdomain"):
		return reject("it points at a name that only resolves inside a network")
	case !strings.Contains(strings.TrimSuffix(host, "."), "."):
		return reject("it must be a fully qualified public hostname")
	}
	return nil
}

// CreateEndpoint registers a new webhook destination for tenantID and
// returns its generated shared secret exactly once — the REST/MCP layer
// never sees it again after this call, same posture as a bearer token.
func (s *WebhookDispatchService) CreateEndpoint(ctx context.Context, tenantID, endpointURL string, eventTypes []string) (*tenant.WebhookEndpoint, string, error) {
	if err := ValidateWebhookURL(endpointURL); err != nil {
		return nil, "", err
	}
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
		ID: newID("whep"), TenantID: tenantID, URL: endpointURL, Secret: secret,
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
	return resp.Status >= 200 && resp.Status < 300
}
