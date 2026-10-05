package routing

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

func TestParseMode(t *testing.T) {
	cases := map[string]Mode{
		"": ModeAuto, "auto": ModeAuto, "best_execution": ModeAuto, "FIXED": ModeAuto,
		"cheapest": ModeCheapest, " Fastest ": ModeFastest,
	}
	for in, want := range cases {
		got, err := ParseMode(in)
		if err != nil || got != want {
			t.Errorf("ParseMode(%q) = %q, %v; want %q", in, got, err, want)
		}
		if !got.Valid() {
			t.Errorf("%q should be a valid mode", got)
		}
	}
	if _, err := ParseMode("bogus"); err == nil {
		t.Error("an unknown strategy must be refused")
	}
	if Mode("").Valid() || Mode("auto").Valid() {
		t.Error("modes are the upper-case constants only")
	}
}

func TestCapabilityNormalize(t *testing.T) {
	c, err := Capability{ID: " Solana.Token-Risk ", Title: " Token risk ", Evaluator: " Generic "}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if c.ID != "solana.token-risk" || c.Kind != KindData || c.Title != "Token risk" || c.Evaluator != "generic" {
		t.Errorf("canonical form wrong: %+v", c)
	}
	bad := map[string]Capability{
		"bad id":          {ID: "x"},
		"bad kind":        {ID: "ok.cap", Kind: "magic"},
		"long title":      {ID: "ok.cap", Title: strings.Repeat("t", 121)},
		"long desc":       {ID: "ok.cap", Description: strings.Repeat("d", 1001)},
		"bad evaluator":   {ID: "ok.cap", Evaluator: "Has Spaces"},
		"schema not json": {ID: "ok.cap", OutputSchema: json.RawMessage(`{nope`)},
		"schema too big":  {ID: "ok.cap", OutputSchema: json.RawMessage(`"` + strings.Repeat("a", maxSchemaBytes) + `"`)},
	}
	for name, c := range bad {
		if _, err := c.Normalize(); err == nil {
			t.Errorf("%s should be refused", name)
		}
	}
	if _, err := (Capability{ID: "ok.cap", Kind: KindTrade, OutputSchema: json.RawMessage(`{"type":"object"}`)}).Normalize(); err != nil {
		t.Errorf("a trade capability with a schema should pass: %v", err)
	}
}
