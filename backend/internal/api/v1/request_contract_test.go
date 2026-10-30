package v1

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

func decodeSpec(t *testing.T, body string) (economicSpecRequest, error) {
	t.Helper()
	var req economicSpecRequest
	r := httptest.NewRequest("POST", "/", strings.NewReader(body))
	return req, decodeJSON(r, &req)
}

func TestMaxUSDIsADecimalStringOfDollars(t *testing.T) {
	for body, want := range map[string]int64{
		`{"max_usd":"0.05"}`:                                         50_000,
		`{"max_usd":"1"}`:                                            1_000_000,
		`{"max_usd":"0.000001"}`:                                     1,
		`{"max_usd":" 2.5 "}`:                                        2_500_000,
		`{"max_usd":"0.05","budget_max_minor":50000}`:                50_000,
		`{"max_usd":"0.05","currency":"usdc"}`:                       50_000,
		`{"budget_max_minor":7000}`:                                  7_000,
		`{"max_usd":"","budget_max_minor":7000}`:                     7_000,
		`{"max_usd":"0.05","budget_max_minor":0,"capability":"x.y"}`: 50_000,
	} {
		req, err := decodeSpec(t, body)
		if err != nil || req.BudgetMaxMinor != want {
			t.Errorf("%s: %d %v, want %d", body, req.BudgetMaxMinor, err, want)
		}
	}
}

func TestMaxUSDRefusesWhatIsNotAnAmountOfDollars(t *testing.T) {
	for body, want := range map[string]string{
		`{"max_usd":0.05}`:                            "not a number",
		`{"max_usd":5}`:                               "not a number",
		`{"max_usd":"0"}`:                             "positive amount",
		`{"max_usd":"-1"}`:                            "positive amount",
		`{"max_usd":"0.0000001"}`:                     "at most 6 decimals",
		`{"max_usd":"five"}`:                          "decimal string",
		`{"max_usd":"0.05","budget_max_minor":60000}`: "different amounts",
		`{"max_usd":"0.05","currency":"INR"}`:         "USDC",
		`{"max_usd":"1e3"}`:                           "decimal string",
		`{"max_usd":"0,05"}`:                          "decimal string",
	} {
		_, err := decodeSpec(t, body)
		var bad badRequestError
		if !errors.As(err, &bad) || !strings.Contains(bad.msg, want) {
			t.Errorf("%s: %v, want a bad request that says %q", body, err, want)
		}
	}
}

func executeBody(t *testing.T, body string) executeRequest {
	t.Helper()
	var req executeRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	return req
}

// What a request asks for, not how it is spelled: the key only refuses a different request.
func TestAFingerprintIgnoresSpellingAndTheWindow(t *testing.T) {
	base := requestFingerprint(executeBody(t, `{"capability":"token.price","input":{"mint":"So1"},"budget_max_minor":50000,"window":"a"}`))
	for name, body := range map[string]string{
		"another window":        `{"capability":"token.price","input":{"mint":"So1"},"budget_max_minor":50000,"window":"b"}`,
		"no window":             `{"capability":"token.price","input":{"mint":"So1"},"budget_max_minor":50000}`,
		"whitespace in input":   `{"capability":"token.price","input":{ "mint" : "So1" },"budget_max_minor":50000}`,
		"the amount in dollars": `{"capability":"token.price","input":{"mint":"So1"},"budget_max_minor":50000,"max_usd":"0.05"}`,
	} {
		if got := requestFingerprint(executeBody(t, body)); got != base {
			t.Errorf("%s changes the fingerprint", name)
		}
	}
	for name, body := range map[string]string{
		"another input":      `{"capability":"token.price","input":{"mint":"So2"},"budget_max_minor":50000}`,
		"another capability": `{"capability":"token.risk","input":{"mint":"So1"},"budget_max_minor":50000}`,
		"another amount":     `{"capability":"token.price","input":{"mint":"So1"},"budget_max_minor":60000}`,
		"fresh":              `{"capability":"token.price","input":{"mint":"So1"},"budget_max_minor":50000,"fresh":true}`,
	} {
		if got := requestFingerprint(executeBody(t, body)); got == base {
			t.Errorf("%s does not change the fingerprint", name)
		}
	}
}

func TestAKeyIsAPrintableTokenNotAFreeText(t *testing.T) {
	for _, ok := range []string{"3f2b6c1e-8d41-4a0b-9c75-1e2d3c4b5a69", "01J9ZK3Q8W5X7Y2V4N6M8P0R1S", "order 1234 retry 2", "a", strings.Repeat("k", 255)} {
		if !idempotencyKeyRE.MatchString(ok) || len(ok) > maxIdempotencyKey {
			t.Errorf("%q should be accepted", ok)
		}
	}
	for _, bad := range []string{"", " ", "two  spaces", "tab\tin", "line\nbreak", "café", "emoji \U0001F4B8", "\x00"} {
		if idempotencyKeyRE.MatchString(bad) {
			t.Errorf("%q should be refused", bad)
		}
	}
}
