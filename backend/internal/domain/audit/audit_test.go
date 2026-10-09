package audit

import (
	"testing"
	"time"
)

func TestValidate_RejectsSecretLookingKeys(t *testing.T) {
	cases := []string{"pan", "card_number", "cvv", "CVV", "otp", "private_key", "session_cookie", "Authorization", "access_token"}
	for _, key := range cases {
		e := Event{Metadata: map[string]any{key: "x"}}
		if err := e.Validate(); err == nil {
			t.Errorf("expected Validate to reject metadata key %q", key)
		}
	}
}

func TestValidate_AllowsSafeKeys(t *testing.T) {
	e := Event{Metadata: map[string]any{"merchant": "zepto", "item_count": 3}}
	if err := e.Validate(); err != nil {
		t.Errorf("unexpected error for safe metadata: %v", err)
	}
}

func TestNewEvent_UniqueIDs(t *testing.T) {
	seen := map[string]bool{}
	now := time.Now()
	for i := 0; i < 50; i++ {
		e := NewEvent("IntentCreated", now)
		if seen[e.EventID] {
			t.Fatal("duplicate event ID generated")
		}
		seen[e.EventID] = true
		if e.Action != "IntentCreated" {
			t.Errorf("expected action to be set, got %q", e.Action)
		}
	}
}
