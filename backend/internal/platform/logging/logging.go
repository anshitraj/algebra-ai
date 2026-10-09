// Package logging provides Algebra's structured logger with automatic
// redaction of sensitive field values (mandate §50). Redaction happens at
// the slog.Handler level, so it applies uniformly no matter which package
// logs — a call site cannot opt out by forgetting to redact manually.
//
// This only redacts structured attributes (slog.String("token", ...) and
// similar) — it cannot redact a secret that was string-interpolated
// directly into a log message. The convention everywhere in this codebase
// is: secrets are passed as attrs (so they can be redacted), never
// interpolated into the message string.
package logging

import (
	"io"
	"log/slog"
	"strings"
)

// sensitiveKeySubstrings is matched case-insensitively against attribute
// keys at every nesting level. Deliberately broad (substring, not exact
// match) so "provider_token_ref", "access_token", and "card_number" are all
// caught by "token"/"card" without enumerating every variant.
var sensitiveKeySubstrings = []string{
	"authorization", "cookie", "token", "password", "secret",
	"pan", "card_number", "cardnumber", "cvv", "cvc", "pin",
	"otp", "private_key", "privatekey", "seed_phrase", "seedphrase",
	"phone", "email", "address",
}

const redacted = "[REDACTED]"

func isSensitiveKey(key string) bool {
	lower := strings.ToLower(key)
	for _, s := range sensitiveKeySubstrings {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

func redactingReplaceAttr(_ []string, a slog.Attr) slog.Attr {
	if isSensitiveKey(a.Key) {
		return slog.String(a.Key, redacted)
	}
	return a
}

// New builds a JSON structured logger writing to w at the given level, with
// redaction applied to every attribute before it is ever serialized.
func New(w io.Writer, level slog.Level) *slog.Logger {
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: redactingReplaceAttr,
	})
	return slog.New(handler)
}
