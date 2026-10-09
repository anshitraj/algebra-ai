// Package sanitize prepares merchant-supplied text for places an agent or a
// user will read it: product names, merchant error messages, status lines.
package sanitize

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Text drops control and invisible format characters (newlines, ANSI
// escapes, zero-width and bidi-override characters), collapses whitespace,
// and caps the result at max bytes on a rune boundary. The result is still
// untrusted data — this removes formatting tricks and runaway length, it
// does not make merchant text safe to follow as instructions.
func Text(s string, max int) string {
	var b strings.Builder
	pendingSpace := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			pendingSpace = b.Len() > 0
			continue
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r), r == utf8.RuneError:
			continue
		}
		if pendingSpace {
			b.WriteByte(' ')
			pendingSpace = false
		}
		b.WriteRune(r)
	}
	return Truncate(b.String(), max)
}

// Truncate caps s at max bytes without splitting a UTF-8 sequence, marking
// the cut with an ellipsis.
func Truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
