// Package audit defines the append-only AuditEvent that every state
// transition, policy decision, and privacy resolution in Algebra emits.
// Audit events must make this answerable at all times: "which agent
// requested this purchase, what did the user approve, which policy allowed
// it, which merchant was selected, and what amount was actually charged?"
package audit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// forbiddenMetadataKeys is a defense-in-depth deny-list: even though
// logging redaction (internal/platform/logging) should already strip these,
// an AuditEvent must reject them outright at construction time so a bug in
// the caller can never persist a secret into the append-only log.
var forbiddenMetadataKeys = []string{
	"pan", "card_number", "cvv", "cvc", "pin",
	"password", "otp", "private_key", "seed_phrase",
	"session_cookie", "cookie", "authorization", "access_token", "token",
}

// Event is one immutable audit record.
type Event struct {
	EventID   string    `json:"event_id"`
	TraceID   string    `json:"trace_id,omitempty"`
	Timestamp time.Time `json:"timestamp"`

	UserID                 string `json:"user_id,omitempty"`
	AgentID                string `json:"agent_id,omitempty"`
	IntentID               string `json:"intent_id,omitempty"`
	AgenticPaymentIntentID string `json:"agentic_payment_intent_id,omitempty"`
	TenantID               string `json:"tenant_id,omitempty"`

	Action         string `json:"action"`
	PreviousState  string `json:"previous_state,omitempty"`
	NewState       string `json:"new_state,omitempty"`
	PolicyDecision string `json:"policy_decision,omitempty"`

	Merchant           string `json:"merchant,omitempty"`
	PaymentSourceAlias string `json:"payment_source_alias,omitempty"`
	Result             string `json:"result,omitempty"`

	// Metadata carries small, non-sensitive extra context. Validate rejects
	// any key name that looks like a secret before this can be persisted.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Validate rejects an event whose Metadata contains a forbidden key name.
// This is a belt-and-suspenders check in addition to logging redaction —
// audit events are append-only, so a secret written here can never be
// scrubbed after the fact.
func (e Event) Validate() error {
	for k := range e.Metadata {
		lower := strings.ToLower(k)
		for _, forbidden := range forbiddenMetadataKeys {
			if strings.Contains(lower, forbidden) {
				return fmt.Errorf("audit: metadata key %q looks like a secret and is forbidden in audit events", k)
			}
		}
	}
	return nil
}

// NewEvent constructs an Event with a fresh ID and the given timestamp. The
// zero value's remaining fields are set by the caller before Record.
func NewEvent(action string, now time.Time) Event {
	return Event{
		EventID:   generateID(),
		Action:    action,
		Timestamp: now,
	}
}

func generateID() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return "evt_" + hex.EncodeToString(buf)
}

// Logger persists audit events. It is append-only by contract: no Update or
// Delete method exists on this interface.
type Logger interface {
	Record(ctx context.Context, event Event) error
}
