package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// TestSensitiveValuesNeverAppearInLogOutput is the automated check the
// mandate calls for directly (§50/§64): "Add automated tests specifically
// checking sensitive values cannot appear in logs."
func TestSensitiveValuesNeverAppearInLogOutput(t *testing.T) {
	cases := []struct {
		key   string
		value string
	}{
		{"authorization", "Bearer sk_live_abcdef123456"},
		{"cookie", "session=deadbeef"},
		{"agent_token", "alg_agent_supersecret"},
		{"pan", "4111111111111111"},
		{"card_number", "4111111111111111"},
		{"cvv", "123"},
		{"otp", "482913"},
		{"private_key", "0xabcdef0123456789"},
		{"seed_phrase", "witch collapse practice feed shame open despair creek road again ice least"},
		{"user_password", "hunter2"},
		{"provider_token_ref", "tok_live_9f8e7d"},
		{"phone", "+919999999999"},
		{"email", "user@example.com"},
		{"shipping_address", "221B Baker Street"},
	}

	for _, c := range cases {
		var buf bytes.Buffer
		logger := New(&buf, slog.LevelInfo)
		logger.Info("test event", slog.String(c.key, c.value))

		output := buf.String()
		// The leak check excludes the "time" field: slog.JSONHandler stamps
		// the real wall-clock time on every line, and a short numeric test
		// value (e.g. cvv "123") can coincidentally appear inside its
		// nanosecond digits — a false positive with nothing to do with
		// redaction. Every other field (level, msg, and the attribute under
		// test) is still checked, so an actual leak anywhere else is still
		// caught.
		var parsed map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &parsed); err != nil {
			t.Fatalf("key %q: log output is not valid JSON: %v (%s)", c.key, err, output)
		}
		delete(parsed, "time")
		withoutTime, err := json.Marshal(parsed)
		if err != nil {
			t.Fatalf("key %q: re-encoding log output: %v", c.key, err)
		}
		if strings.Contains(string(withoutTime), c.value) {
			t.Errorf("key %q: sensitive value %q leaked into log output: %s", c.key, c.value, output)
		}
		if !strings.Contains(output, redacted) {
			t.Errorf("key %q: expected redaction marker in output: %s", c.key, output)
		}
	}
}

func TestSafeValuesPassThroughUnredacted(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, slog.LevelInfo)
	logger.Info("intent created", slog.String("merchant", "zepto"), slog.String("intent_id", "pi_123"), slog.Int("item_count", 2))

	output := buf.String()
	for _, want := range []string{"zepto", "pi_123"} {
		if !strings.Contains(output, want) {
			t.Errorf("expected safe value %q to appear in log output: %s", want, output)
		}
	}
	if strings.Contains(output, redacted) {
		t.Errorf("expected no redaction for safe-keyed attributes: %s", output)
	}
}

func TestIsSensitiveKey_CaseInsensitive(t *testing.T) {
	for _, key := range []string{"Authorization", "AUTHORIZATION", "Card_Number", "X-Auth-Token"} {
		if !isSensitiveKey(key) {
			t.Errorf("expected %q to be classified sensitive", key)
		}
	}
	for _, key := range []string{"merchant", "intent_id", "status", "quantity"} {
		if isSensitiveKey(key) {
			t.Errorf("expected %q to NOT be classified sensitive", key)
		}
	}
}
