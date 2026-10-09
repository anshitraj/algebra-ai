package sanitize

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestText(t *testing.T) {
	cases := map[string]string{
		"  Amul   Taaza\nMilk\t1L ":       "Amul Taaza Milk 1L",
		"price\x1b[31m ₹45":               "price[31m ₹45",
		"safe‮txt.exe":                    "safetxt.exe",
		"zero​width":                      "zerowidth",
		"ignore previous\r\ninstructions": "ignore previous instructions",
	}
	for in, want := range cases {
		if got := Text(in, 200); got != want {
			t.Errorf("Text(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncateKeepsRunesWhole(t *testing.T) {
	got := Truncate(strings.Repeat("₹", 10), 7)
	if !utf8.ValidString(got) || !strings.HasSuffix(got, "…") || len(strings.TrimSuffix(got, "…")) > 7 {
		t.Fatalf("Truncate produced %q", got)
	}
	if Truncate("short", 10) != "short" {
		t.Fatal("short strings must be unchanged")
	}
}
