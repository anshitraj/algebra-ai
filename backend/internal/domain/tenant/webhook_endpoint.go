package tenant

import "time"

// WebhookEndpoint is a tenant's registered outbound-webhook destination —
// the sending half of what internal/app/webhook_service.go only ever
// received. Secret is an HMAC-SHA256 shared secret Algebra generates and
// shows the tenant once, same posture as a bearer token. An empty
// EventTypes means "send every event type."
type WebhookEndpoint struct {
	ID         string
	TenantID   string
	URL        string
	Secret     string
	EventTypes []string

	CreatedAt time.Time
	RevokedAt *time.Time
}

func (w *WebhookEndpoint) IsRevoked() bool { return w.RevokedAt != nil }

// Wants reports whether this endpoint should receive eventType — every
// event type when EventTypes is empty, an exact match otherwise.
func (w *WebhookEndpoint) Wants(eventType string) bool {
	if len(w.EventTypes) == 0 {
		return true
	}
	for _, t := range w.EventTypes {
		if t == eventType {
			return true
		}
	}
	return false
}
