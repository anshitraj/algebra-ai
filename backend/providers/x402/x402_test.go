package x402

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

const v1Body = `{"x402Version":1,"error":"X-PAYMENT header is required","accepts":[
  {"scheme":"exact","network":"solana","maxAmountRequired":"3000","resource":"https://api.example.com/risk","description":"Token risk","mimeType":"application/json","payTo":"PayeeAddr111","maxTimeoutSeconds":60,"asset":"EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v","extra":{"feePayer":"FeePayer111"},"outputSchema":{"input":{"type":"http"}}},
  {"scheme":"exact","network":"base","maxAmountRequired":"3000","payTo":"0xabc","asset":"0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"}]}`

const v2Body = `{"x402Version":2,"accepts":[{"scheme":"exact","network":"solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp","amount":"5000","asset":"EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v","payTo":"PayeeAddr222","maxTimeoutSeconds":30}]}`

func TestAmountMinor(t *testing.T) {
	ok := map[string]Requirements{
		"3000": {MaxAmountRequired: "3000"},
		"5000": {Amount: "5000"},
		"7":    {MaxAmountRequired: " 7 ", Amount: "999"}, // v1 field wins
	}
	for want, r := range ok {
		got, err := r.AmountMinor()
		if err != nil || got != mustInt(want) {
			t.Errorf("AmountMinor(%+v) = %d, %v; want %s", r, got, err, want)
		}
	}
	for _, r := range []Requirements{{}, {Amount: "0"}, {Amount: "-5"}, {Amount: "abc"}, {Amount: "1.5"}, {Amount: "99999999999999999999"}} {
		if _, err := r.AmountMinor(); err == nil {
			t.Errorf("amount %+v must be refused", r)
		}
	}
}

func mustInt(s string) int64 {
	var n int64
	for _, c := range s {
		n = n*10 + int64(c-'0')
	}
	return n
}

func TestSelect(t *testing.T) {
	r, err := Select(json.RawMessage(v1Body), "EXACT", "Base", "solana")
	if err != nil || r.Network != "solana" {
		t.Fatalf("first matching option in the provider's order: %+v %v", r, err)
	}
	if r, err = Select(json.RawMessage(v1Body), "exact", "base"); err != nil || r.PayTo != "0xabc" {
		t.Fatalf("a different network picks a different option: %+v %v", r, err)
	}
	if _, err = Select(json.RawMessage(v1Body), "exact", "polygon"); !errors.Is(err, ErrNoMatch) {
		t.Errorf("no option on the networks we pay on: %v", err)
	}
	if _, err = Select(json.RawMessage(v1Body), "upto", "solana"); !errors.Is(err, ErrNoMatch) {
		t.Errorf("only the requested scheme: %v", err)
	}
	single := `{"scheme":"exact","network":"solana","maxAmountRequired":"1","payTo":"p","asset":"a"}`
	if r, err = Select(json.RawMessage(single), "exact", "solana"); err != nil || r.PayTo != "p" {
		t.Errorf("a single requirements object is accepted too: %+v %v", r, err)
	}
	if _, err = Select(json.RawMessage(`{"hello":"world"}`), "exact", "solana"); err == nil {
		t.Error("something that isn't requirements must be refused")
	}
}

func TestParseChallengeV1Body(t *testing.T) {
	c, err := ParseChallenge(http.Header{}, []byte(v1Body))
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != 1 || len(c.Accepts) != 2 || len(c.Raw) != 2 {
		t.Fatalf("challenge: %+v", c)
	}
	if !strings.Contains(string(c.Raw[0]), `"outputSchema"`) {
		t.Error("the raw option keeps fields this package doesn't model")
	}
}

func TestParseChallengeV2Header(t *testing.T) {
	for name, enc := range map[string]*base64.Encoding{
		"std": base64.StdEncoding, "raw-std": base64.RawStdEncoding, "url": base64.URLEncoding, "raw-url": base64.RawURLEncoding,
	} {
		h := http.Header{}
		h.Set(HeaderPaymentRequiredV2, enc.EncodeToString([]byte(v2Body)))
		c, err := ParseChallenge(h, []byte("<html>Payment required</html>"))
		if err != nil || c.Version != 2 || len(c.Accepts) != 1 || c.Accepts[0].PayTo != "PayeeAddr222" {
			t.Errorf("%s: %+v %v", name, c, err)
		}
	}
}

func TestParseChallengeFallsBackToTheBody(t *testing.T) {
	h := http.Header{}
	h.Set(HeaderPaymentRequiredV2, "!!!not base64!!!")
	if c, err := ParseChallenge(h, []byte(v1Body)); err != nil || len(c.Accepts) != 2 {
		t.Fatalf("a garbage header is ignored when the body is a challenge: %+v %v", c, err)
	}
	h.Set(HeaderPaymentRequiredV2, base64.StdEncoding.EncodeToString([]byte(`{"x402Version":2,"accepts":[]}`)))
	if c, err := ParseChallenge(h, []byte(v1Body)); err != nil || len(c.Accepts) != 2 {
		t.Fatalf("a header with no options defers to the body: %+v %v", c, err)
	}
	if _, err := ParseChallenge(http.Header{}, []byte(`<html>pay up</html>`)); err == nil {
		t.Error("a 402 with no requirements is not x402")
	}
	if _, err := ParseChallenge(http.Header{}, nil); err == nil {
		t.Error("an empty 402 is not x402")
	}
}

func TestSingleKeepsVersionAndTheProvidersBytes(t *testing.T) {
	c, _ := ParseChallenge(http.Header{}, []byte(v1Body))
	b, err := c.Single(1)
	if err != nil {
		t.Fatal(err)
	}
	var got Challenge
	if err := json.Unmarshal(b, &got); err != nil || got.Version != 1 || len(got.Accepts) != 1 || got.Accepts[0].Network != "base" {
		t.Fatalf("single: %s %v", b, err)
	}
	// And Select can read it back, so the rail needs no special case.
	if r, err := Select(b, "exact", "base"); err != nil || r.PayTo != "0xabc" {
		t.Errorf("select on a single: %+v %v", r, err)
	}
	first, _ := c.Single(0)
	if !strings.Contains(string(first), `"outputSchema"`) {
		t.Error("the provider's own bytes are kept")
	}
	if _, err := c.Single(2); err == nil {
		t.Error("an out-of-range option must be refused")
	}
	// Without raw bytes (a hand-built challenge) it still works.
	hand := Challenge{Version: 2, Accepts: []Requirements{{Scheme: "exact", Network: "solana", Amount: "1", PayTo: "p", Asset: "a"}}}
	if b, err := hand.Single(0); err != nil || !strings.Contains(string(b), `"x402Version":2`) {
		t.Errorf("hand-built single: %s %v", b, err)
	}
}

func TestPaymentPayloadRoundTrip(t *testing.T) {
	p := PaymentPayload{Version: 1, Scheme: "exact", Network: "solana", Payload: json.RawMessage(`{"transaction":"AQID"}`)}
	h, err := p.Encode()
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodePayment(h)
	if err != nil || got.Network != "solana" || string(got.Payload) != `{"transaction":"AQID"}` {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	if _, err := DecodePayment("%%%"); err == nil {
		t.Error("non-base64 must be refused")
	}
	if _, err := DecodePayment(base64.StdEncoding.EncodeToString([]byte("not json"))); err == nil {
		t.Error("non-JSON must be refused")
	}
}

func TestSettleResponseRoundTrip(t *testing.T) {
	s := SettleResponse{Success: true, Transaction: "5Gf9sig", Network: "solana", Payer: "Payer111"}
	got, err := DecodeSettle(s.Encode())
	if err != nil || got != s {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	if _, err := DecodeSettle("%%%"); err == nil {
		t.Error("non-base64 must be refused")
	}
	if _, err := DecodeSettle(base64.StdEncoding.EncodeToString([]byte("nope"))); err == nil {
		t.Error("non-JSON must be refused")
	}
}

const v2Full = `{"x402Version":2,"error":"PAYMENT-SIGNATURE header is required","resource":{"url":"https://api.example.com/risk","description":"Token risk","mimeType":"application/json"},
 "accepts":[{"scheme":"exact","network":"solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp","amount":"2500","asset":"EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v","payTo":"PayeeAddr","maxTimeoutSeconds":60,"extra":{"feePayer":"FeePayer1","memo":"order-42"}}]}`

func TestExtraField(t *testing.T) {
	c, _ := ParseChallenge(http.Header{}, []byte(v2Full))
	r := c.Accepts[0]
	if r.ExtraField("feePayer") != "FeePayer1" || r.ExtraField("memo") != "order-42" || r.ExtraField("nope") != "" {
		t.Errorf("extra: %+v", r.Extra)
	}
	if (Requirements{}).ExtraField("feePayer") != "" || (Requirements{Extra: json.RawMessage(`[1]`)}).ExtraField("x") != "" {
		t.Error("missing or non-object extra reads as empty")
	}
}

func TestSelectRawKeepsWhatAV2PayerNeedsToEcho(t *testing.T) {
	sel, err := SelectRaw(json.RawMessage(v2Full), "exact", "solana", "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp")
	if err != nil {
		t.Fatal(err)
	}
	if sel.Version != 2 || sel.Requirements.PayTo != "PayeeAddr" || !strings.Contains(string(sel.Resource), "api.example.com/risk") {
		t.Errorf("selected: %+v", sel)
	}
	if !strings.Contains(string(sel.Raw), `"memo":"order-42"`) || strings.Contains(string(sel.Raw), "x402Version") {
		t.Errorf("Raw is the option itself, exactly as sent: %s", sel.Raw)
	}
	if _, err := SelectRaw(json.RawMessage(v2Full), "exact", "base"); err != ErrNoMatch {
		t.Errorf("no option on base: %v", err)
	}
	// A challenge that went through Single still selects the same way.
	c, _ := ParseChallenge(http.Header{}, []byte(v2Full))
	single, _ := c.Single(0)
	again, err := SelectRaw(single, "exact", "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp")
	if err != nil || again.Version != 2 || string(again.Resource) != string(sel.Resource) || string(again.Raw) != string(sel.Raw) {
		t.Errorf("Single must preserve version, resource and the option: %+v %v", again, err)
	}
	// A bare requirements object is version 1 with its own bytes.
	one := `{"scheme":"exact","network":"solana","maxAmountRequired":"1","payTo":"p","asset":"a"}`
	got, err := SelectRaw(json.RawMessage(one), "exact", "solana")
	if err != nil || got.Version != 1 || string(got.Raw) != one {
		t.Errorf("bare object: %+v %v", got, err)
	}
	if _, err := SelectRaw(json.RawMessage(`{"hello":1}`), "exact", "solana"); err == nil {
		t.Error("not requirements")
	}
}

func TestPaymentPayloadV2RoundTrip(t *testing.T) {
	p := PaymentPayload{
		Version: 2, Resource: json.RawMessage(`{"url":"https://api.example.com/risk"}`),
		Accepted: json.RawMessage(`{"scheme":"exact","network":"solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp"}`),
		Payload:  json.RawMessage(`{"transaction":"AQID"}`),
	}
	h, err := p.Encode()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.StdEncoding.DecodeString(h)
	if strings.Contains(string(raw), `"scheme":""`) || strings.Contains(string(raw), `"network":""`) {
		t.Errorf("v2 carries no empty v1 fields: %s", raw)
	}
	got, err := DecodePayment(h)
	if err != nil || got.Version != 2 || string(got.Accepted) != string(p.Accepted) || string(got.Resource) != string(p.Resource) || string(got.Payload) != `{"transaction":"AQID"}` {
		t.Errorf("round trip: %+v %v", got, err)
	}
}
