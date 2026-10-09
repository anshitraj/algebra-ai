package x402client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/platform/safehttp"
	"github.com/project-algebra/algebra/providers/x402"
)

const (
	solUSDC  = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
	fakeUSDC = "FakeUSDCMint1111111111111111111111111111111"
	secret   = "SECRET-PAYMENT-VALUE-DO-NOT-LOG"
)

type recorded struct {
	method, uri string
	header      http.Header
	body        string
}

// provider is a scriptable x402 server. challenge answers a request with no
// payment header; paid answers one that has it.
type provider struct {
	mu        sync.Mutex
	reqs      []recorded
	challenge func(w http.ResponseWriter, r *http.Request)
	paid      func(w http.ResponseWriter, r *http.Request)
	srv       *httptest.Server
}

func newProvider(t *testing.T) *provider {
	t.Helper()
	p := &provider{}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		p.mu.Lock()
		p.reqs = append(p.reqs, recorded{r.Method, r.URL.RequestURI(), r.Header.Clone(), string(b)})
		p.mu.Unlock()
		if r.Header.Get(x402.HeaderPayment) != "" || r.Header.Get(x402.HeaderPaymentV2) != "" {
			p.paid(w, r)
			return
		}
		p.challenge(w, r)
	}))
	t.Cleanup(p.srv.Close)
	p.challenge = func(w http.ResponseWriter, r *http.Request) {
		writeChallenge(w, solOption(5_000, "PayeeAddr1", solUSDC))
	}
	p.paid = func(w http.ResponseWriter, r *http.Request) { writePaid(w, `{"mint":"SOL","risk_score":12}`, "tx_abc") }
	return p
}

func (p *provider) requests() []recorded {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]recorded(nil), p.reqs...)
}

func solOption(amount int, payTo, asset string) string {
	return fmt.Sprintf(`{"scheme":"exact","network":"solana","maxAmountRequired":"%d","resource":"https://x","payTo":"%s","maxTimeoutSeconds":60,"asset":"%s","extra":{"feePayer":"FeePayer1"}}`, amount, payTo, asset)
}

func writeChallenge(w http.ResponseWriter, options ...string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusPaymentRequired)
	fmt.Fprintf(w, `{"x402Version":1,"error":"X-PAYMENT header is required","accepts":[%s]}`, strings.Join(options, ","))
}

func writePaid(w http.ResponseWriter, body, tx string) {
	if tx != "" {
		w.Header().Set(x402.HeaderPaymentResponse, x402.SettleResponse{Success: true, Transaction: tx, Network: "solana"}.Encode())
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, body)
}

func newRunner(timeout time.Duration, reuse time.Duration) *Runner {
	return New(Config{
		HTTP:          safehttp.New(safehttp.Options{AllowLoopback: true, Timeout: timeout}),
		Networks:      map[string]string{"solana": "x402-solana", "solana-devnet": "x402-solana", "sandbox": "sandbox"},
		ReuseQuoteFor: reuse,
	})
}

func candidateFor(t *testing.T, p *provider, method string) routing.Candidate {
	t.Helper()
	c, err := routing.Candidate{
		Capability: "solana.token-risk", Provider: "acme", ExecutionType: routing.ExecX402, Endpoint: p.srv.URL + "/risk", Method: method,
		Sources: []routing.DiscoverySource{routing.SourceConfigured},
	}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

var input = json.RawMessage(`{"mint":"SOL"}`)

// payCounter is a Pay function that records how often it was asked.
type payCounter struct {
	mu    sync.Mutex
	calls int
	reqs  []app.PaymentRequest
	err   error
}

func (p *payCounter) pay(_ context.Context, req app.PaymentRequest) (*app.PaymentAuthority, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.reqs = append(p.reqs, req)
	if p.err != nil {
		return nil, p.err
	}
	return &app.PaymentAuthority{Header: x402.HeaderPayment, Value: secret, AmountMinor: 5_000}, nil
}

func stepCall(q routing.Quote, pay func(context.Context, app.PaymentRequest) (*app.PaymentAuthority, error)) app.StepCall {
	return app.StepCall{Quote: q, Input: input, Reservation: econ.Reservation{ID: "rsv_1", IdempotencyKey: "eint_1-a1", HoldMinor: 5_000}, Pay: pay}
}

func quoteOf(t *testing.T, r *Runner, c routing.Candidate) routing.Quote {
	t.Helper()
	q, err := r.Quote(context.Background(), c, input)
	if err != nil {
		t.Fatal(err)
	}
	// What app.ExecutionService fills in after the runner returns the terms.
	q.CandidateID, q.Capability, q.Provider, q.ExecutionType, q.Endpoint = c.ID, c.Capability, c.Provider, c.ExecutionType, c.Endpoint
	q.QuotedAt = time.Now()
	return q
}

func TestQuotePicksTheCheapestOptionAlgebraCanPay(t *testing.T) {
	p := newProvider(t)
	p.challenge = func(w http.ResponseWriter, _ *http.Request) {
		writeChallenge(w,
			`{"scheme":"exact","network":"base","maxAmountRequired":"100","payTo":"0xabc","asset":"0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"}`, // no rail for base
			solOption(1_000, "Thief", fakeUSDC), // a look-alike token, cheapest of all
			solOption(5_000, "PayeeAddr1", solUSDC),
			solOption(4_000, "PayeeAddr2", solUSDC), // the cheapest real one
			`{"scheme":"upto","network":"solana","maxAmountRequired":"10","payTo":"p","asset":"`+solUSDC+`"}`,
		)
	}
	r := newRunner(2*time.Second, 0)
	q, err := r.Quote(context.Background(), candidateFor(t, p, "POST"), input)
	if err != nil {
		t.Fatal(err)
	}
	if q.Cost.ProviderMinor != 4_000 || q.PayTo != "PayeeAddr2" || q.Network != "solana" || q.Asset != "USDC" || q.AssetAddress != solUSDC {
		t.Errorf("quote: %+v", q)
	}
	if q.Method != "POST" || q.Semantics != econ.SemanticsPrepaidExact || q.Test {
		t.Errorf("quote: %+v", q)
	}
	var ch x402.Challenge
	if err := json.Unmarshal(q.Requirements, &ch); err != nil || ch.Version != 1 || len(ch.Accepts) != 1 || ch.Accepts[0].PayTo != "PayeeAddr2" {
		t.Errorf("the quote carries exactly the option it priced: %s", q.Requirements)
	}
	if rail, err := r.Rail(q); err != nil || rail != "x402-solana" {
		t.Errorf("rail = %q, %v", rail, err)
	}
	// The probe itself is an ordinary request carrying the intent's input and no payment.
	reqs := p.requests()
	if len(reqs) != 1 || reqs[0].method != "POST" || reqs[0].body != `{"mint":"SOL"}` || reqs[0].header.Get(x402.HeaderPayment) != "" {
		t.Errorf("probe: %+v", reqs)
	}
}

func TestQuoteRefusals(t *testing.T) {
	r := newRunner(2*time.Second, 0)
	cases := map[string]struct {
		handler func(w http.ResponseWriter, _ *http.Request)
		want    string
	}{
		"only look-alike tokens": {func(w http.ResponseWriter, _ *http.Request) { writeChallenge(w, solOption(1, "p", fakeUSDC)) }, "is not USDC"},
		"no rail for the network": {func(w http.ResponseWriter, _ *http.Request) {
			writeChallenge(w, `{"scheme":"exact","network":"polygon","maxAmountRequired":"1","payTo":"p","asset":"0x1"}`)
		}, "no payment rail"},
		"no payee": {func(w http.ResponseWriter, _ *http.Request) { writeChallenge(w, solOption(1, "", solUSDC)) }, "no payee"},
		"bad amount": {func(w http.ResponseWriter, _ *http.Request) {
			writeChallenge(w, strings.Replace(solOption(1, "p", solUSDC), `"1"`, `"abc"`, 1))
		}, "unreadable amount"},
		"free answer":  {func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"ok":true}`) }, "isn't an x402 resource"},
		"server error": {func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }, "answered 500"},
		"402 that isn't x402": {func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(402)
			_, _ = io.WriteString(w, "<html>pay us</html>")
		}, "no payment requirements"},
	}
	for name, tc := range cases {
		p := newProvider(t)
		p.challenge = func(w http.ResponseWriter, req *http.Request) { tc.handler(w, req) }
		_, err := r.Quote(context.Background(), candidateFor(t, p, "POST"), input)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want error containing %q, got %v", name, tc.want, err)
		}
	}
	// An unreachable provider is an error, not a hang.
	p := newProvider(t)
	c := candidateFor(t, p, "POST")
	p.srv.Close()
	if _, err := r.Quote(context.Background(), c, input); err == nil {
		t.Error("an unreachable provider must be a quote error")
	}
}

func TestQuoteReadsAV2HeaderChallengeAndCAIP2Networks(t *testing.T) {
	p := newProvider(t)
	p.challenge = func(w http.ResponseWriter, _ *http.Request) {
		body := fmt.Sprintf(`{"x402Version":2,"accepts":[{"scheme":"exact","network":"solana:EtWTRABZaYq6iMfeYKouRu166VU2xqa1","amount":"2500","asset":"4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU","payTo":"DevPayee","maxTimeoutSeconds":30}]}`)
		w.Header().Set(x402.HeaderPaymentRequiredV2, base64.StdEncoding.EncodeToString([]byte(body)))
		w.WriteHeader(http.StatusPaymentRequired)
	}
	q, err := newRunner(2*time.Second, 0).Quote(context.Background(), candidateFor(t, p, "POST"), input)
	if err != nil {
		t.Fatal(err)
	}
	if q.Network != "solana-devnet" || q.Cost.ProviderMinor != 2_500 || !q.Test || q.AssetAddress != "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU" {
		t.Errorf("a v2 devnet quote is a test quote on the canonical network: %+v", q)
	}
	var ch x402.Challenge
	_ = json.Unmarshal(q.Requirements, &ch)
	if ch.Version != 2 {
		t.Errorf("the protocol version travels with the requirements so the rail picks the right header: %d", ch.Version)
	}
}

func TestQuoteHonoursTheCandidatesNetwork(t *testing.T) {
	p := newProvider(t)
	p.challenge = func(w http.ResponseWriter, _ *http.Request) {
		writeChallenge(w,
			solOption(9_000, "PayeeAddr1", solUSDC),
			`{"scheme":"exact","network":"sandbox","maxAmountRequired":"100","payTo":"sbx","asset":"USDC"}`)
	}
	c := candidateFor(t, p, "POST")
	c.Network = "solana"
	q, err := newRunner(2*time.Second, 0).Quote(context.Background(), c, input)
	if err != nil || q.Network != "solana" || q.Cost.ProviderMinor != 9_000 {
		t.Errorf("a candidate pinned to solana must not be quoted on the cheaper sandbox option: %+v %v", q, err)
	}
}

func TestRunPaysOnceAndReturnsTheResult(t *testing.T) {
	p := newProvider(t)
	r := newRunner(2*time.Second, 0) // always re-check the terms
	q := quoteOf(t, r, candidateFor(t, p, "POST"))
	pay := &payCounter{}

	obs := r.Run(context.Background(), stepCall(q, pay.pay))
	if !obs.Delivered || !obs.AuthorityReleased || obs.HTTPStatus != 200 || string(obs.Body) != `{"mint":"SOL","risk_score":12}` || obs.ContentType != "application/json" {
		t.Fatalf("observation: %+v", obs)
	}
	if obs.SettleTx != "tx_abc" || obs.Class != "" || !strings.HasPrefix(obs.RequestHash, "sha256:") {
		t.Errorf("observation: %+v", obs)
	}
	if pay.calls != 1 {
		t.Fatalf("payment authority is released exactly once, got %d", pay.calls)
	}
	// What the rail is asked to pay is the provider's own requirements, wrapped with the version.
	var ch x402.Challenge
	if err := json.Unmarshal(pay.reqs[0].Requirements, &ch); err != nil || len(ch.Accepts) != 1 || ch.Accepts[0].PayTo != "PayeeAddr1" || pay.reqs[0].Resource != q.Endpoint {
		t.Errorf("payment request: %s %+v", pay.reqs[0].Requirements, pay.reqs[0])
	}
	reqs := p.requests()
	if len(reqs) != 3 { // quote probe, terms re-check, paid call
		t.Fatalf("want 3 requests, got %d", len(reqs))
	}
	paid := reqs[2]
	if paid.header.Get(x402.HeaderPayment) != secret || paid.header.Get("Idempotency-Key") != "eint_1-a1" || paid.body != `{"mint":"SOL"}` || paid.method != "POST" {
		t.Errorf("paid request: %+v", paid)
	}
	if paid.header.Get("Authorization") != "" || paid.header.Get("Cookie") != "" {
		t.Error("no ambient credentials go to a provider")
	}
}

func TestRunReusesAFreshQuoteWithoutReCheckingTerms(t *testing.T) {
	p := newProvider(t)
	r := newRunner(2*time.Second, 30*time.Second)
	q := quoteOf(t, r, candidateFor(t, p, "POST"))
	obs := r.Run(context.Background(), stepCall(q, (&payCounter{}).pay))
	if !obs.Delivered {
		t.Fatalf("%+v", obs)
	}
	if n := len(p.requests()); n != 2 { // the quote probe and the paid call, no re-check
		t.Errorf("a fresh quote saves the re-check: %d requests", n)
	}
	// A stale quote is re-checked even when reuse is on.
	q.QuotedAt = time.Now().Add(-time.Minute)
	before := len(p.requests())
	_ = r.Run(context.Background(), stepCall(q, (&payCounter{}).pay))
	if n := len(p.requests()) - before; n != 2 {
		t.Errorf("a stale quote is re-checked first: %d requests", n)
	}
}

func TestRunRefusesToPayWhenTheTermsChanged(t *testing.T) {
	changes := map[string]func(w http.ResponseWriter){
		"price went up": func(w http.ResponseWriter) { writeChallenge(w, solOption(5_001, "PayeeAddr1", solUSDC)) },
		"payee changed": func(w http.ResponseWriter) { writeChallenge(w, solOption(5_000, "Attacker", solUSDC)) },
		"asset swapped": func(w http.ResponseWriter) { writeChallenge(w, solOption(5_000, "PayeeAddr1", fakeUSDC)) },
		"network moved": func(w http.ResponseWriter) {
			writeChallenge(w, `{"scheme":"exact","network":"sandbox","maxAmountRequired":"5000","payTo":"PayeeAddr1","asset":"USDC"}`)
		},
		"stopped asking": func(w http.ResponseWriter) { w.WriteHeader(200) },
		"now an error":   func(w http.ResponseWriter) { w.WriteHeader(500) },
		"no longer x402": func(w http.ResponseWriter) { w.WriteHeader(402); _, _ = io.WriteString(w, "nope") },
	}
	for name, change := range changes {
		p := newProvider(t)
		r := newRunner(2*time.Second, 0)
		q := quoteOf(t, r, candidateFor(t, p, "POST"))
		p.challenge = func(w http.ResponseWriter, _ *http.Request) { change(w) }
		pay := &payCounter{}
		obs := r.Run(context.Background(), stepCall(q, pay.pay))
		if obs.Delivered || obs.AuthorityReleased || obs.Class != routing.FailQuote || pay.calls != 0 {
			t.Errorf("%s: must not pay (class %q, pay calls %d): %+v", name, obs.Class, pay.calls, obs)
		}
	}
}

func TestRunAcceptsALowerPrice(t *testing.T) {
	p := newProvider(t)
	r := newRunner(2*time.Second, 0)
	q := quoteOf(t, r, candidateFor(t, p, "POST"))
	p.challenge = func(w http.ResponseWriter, _ *http.Request) {
		writeChallenge(w, solOption(3_000, "PayeeAddr1", solUSDC))
	}
	pay := &payCounter{}
	obs := r.Run(context.Background(), stepCall(q, pay.pay))
	if !obs.Delivered || pay.calls != 1 {
		t.Fatalf("a cheaper price is fine: %+v", obs)
	}
	var ch x402.Challenge
	_ = json.Unmarshal(pay.reqs[0].Requirements, &ch)
	if ch.Accepts[0].MaxAmountRequired != "3000" {
		t.Errorf("the rail pays the new, lower price: %+v", ch.Accepts[0])
	}
}

func TestRunStopsWhenPaymentAuthorityIsRefused(t *testing.T) {
	p := newProvider(t)
	r := newRunner(2*time.Second, 0)
	q := quoteOf(t, r, candidateFor(t, p, "POST"))
	pay := &payCounter{err: errors.New("the provider asked for 9000, more than this attempt's hold of 5000")}
	obs := r.Run(context.Background(), stepCall(q, pay.pay))
	if obs.Delivered || obs.AuthorityReleased || obs.Class != routing.FailPayment || !strings.Contains(obs.Message, "more than this attempt's hold") {
		t.Errorf("observation: %+v", obs)
	}
	for _, rq := range p.requests() {
		if rq.header.Get(x402.HeaderPayment) != "" {
			t.Error("nothing is sent with a payment header when none was released")
		}
	}
}

func TestRunProviderRejectsThePayment(t *testing.T) {
	p := newProvider(t)
	p.paid = func(w http.ResponseWriter, _ *http.Request) {
		writeChallenge(w, solOption(5_000, "PayeeAddr1", solUSDC))
	}
	r := newRunner(2*time.Second, 0)
	q := quoteOf(t, r, candidateFor(t, p, "POST"))
	obs := r.Run(context.Background(), stepCall(q, (&payCounter{}).pay))
	if obs.Delivered || !obs.AuthorityReleased || obs.Class != routing.FailPayment || obs.HTTPStatus != 402 {
		t.Errorf("observation: %+v", obs)
	}
	if !strings.Contains(obs.Message, "rejected the payment") || !strings.Contains(obs.Message, "X-PAYMENT header is required") {
		t.Errorf("message: %q", obs.Message)
	}
	if len(obs.Body) != 0 {
		t.Error("a failure keeps no body")
	}
}

func TestRunProviderErrorAfterPaymentTriesAFreeReplay(t *testing.T) {
	p := newProvider(t)
	p.paid = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }
	r := newRunner(2*time.Second, 0)
	q := quoteOf(t, r, candidateFor(t, p, "POST"))
	before := len(p.requests())
	obs := r.Run(context.Background(), stepCall(q, (&payCounter{}).pay))
	if obs.Delivered || !obs.AuthorityReleased || obs.Class != routing.FailProvider || obs.HTTPStatus != 503 {
		t.Errorf("observation: %+v", obs)
	}
	reqs := p.requests()[before:]
	// re-check, paid call, replay
	if len(reqs) != 3 {
		t.Fatalf("want re-check, paid call and one replay, got %d", len(reqs))
	}
	replay := reqs[2]
	if replay.header.Get("Idempotency-Key") != "eint_1-a1" || replay.header.Get(x402.HeaderPayment) != "" {
		t.Errorf("the replay repeats the key and carries no payment: %+v", replay.header)
	}
}

func TestRunRecoversTheResultByIdempotentReplay(t *testing.T) {
	p := newProvider(t)
	var mu sync.Mutex
	stored := false
	p.paid = func(w http.ResponseWriter, _ *http.Request) { // charged, stored, then the response "got lost"
		mu.Lock()
		stored = true
		mu.Unlock()
		w.WriteHeader(http.StatusGatewayTimeout)
	}
	p.challenge = func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		have := stored
		mu.Unlock()
		if have && r.Header.Get("Idempotency-Key") == "eint_1-a1" {
			writePaid(w, `{"mint":"SOL","risk_score":12}`, "tx_replayed")
			return
		}
		writeChallenge(w, solOption(5_000, "PayeeAddr1", solUSDC))
	}
	r := newRunner(2*time.Second, 0)
	q := quoteOf(t, r, candidateFor(t, p, "POST"))
	obs := r.Run(context.Background(), stepCall(q, (&payCounter{}).pay))
	if !obs.Delivered || !obs.AuthorityReleased || string(obs.Body) != `{"mint":"SOL","risk_score":12}` || obs.SettleTx != "tx_replayed" {
		t.Fatalf("the lost result is recovered for free: %+v", obs)
	}
	if !strings.Contains(obs.Message, "recovered") || obs.Class != "" {
		t.Errorf("message/class: %q %q", obs.Message, obs.Class)
	}
}

func TestRunTimeoutAfterPayment(t *testing.T) {
	p := newProvider(t)
	p.paid = func(w http.ResponseWriter, r *http.Request) { time.Sleep(400 * time.Millisecond) }
	r := newRunner(100*time.Millisecond, 0)
	q := quoteOf(t, r, candidateFor(t, p, "POST"))
	obs := r.Run(context.Background(), stepCall(q, (&payCounter{}).pay))
	if obs.Delivered || !obs.AuthorityReleased || obs.Class != routing.FailTimeout {
		t.Errorf("observation: %+v", obs)
	}
}

// The payment header must never be sent anywhere but the endpoint that was
// priced: a redirect is returned to the caller, not followed.
func TestRunNeverFollowsARedirectWithThePayment(t *testing.T) {
	var hitOther bool
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hitOther = true }))
	defer other.Close()
	p := newProvider(t)
	p.paid = func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}
	r := newRunner(2*time.Second, 0)
	q := quoteOf(t, r, candidateFor(t, p, "POST"))
	obs := r.Run(context.Background(), stepCall(q, (&payCounter{}).pay))
	if hitOther {
		t.Fatal("the redirect target was contacted: the payment header would have gone with it")
	}
	if obs.Delivered || obs.HTTPStatus != http.StatusTemporaryRedirect || obs.Class != routing.FailProvider {
		t.Errorf("observation: %+v", obs)
	}
}

func TestPaymentValueNeverAppearsInAnObservation(t *testing.T) {
	for name, paid := range map[string]func(w http.ResponseWriter, r *http.Request){
		"echoes the header": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(500)
			_, _ = io.WriteString(w, "bad header "+r.Header.Get(x402.HeaderPayment))
		},
		"402 echoing it": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(402)
			_, _ = io.WriteString(w, `{"x402Version":1,"error":"invalid payment `+r.Header.Get(x402.HeaderPayment)+`","accepts":[`+solOption(5_000, "PayeeAddr1", solUSDC)+`]}`)
		},
	} {
		p := newProvider(t)
		p.paid = paid
		r := newRunner(2*time.Second, 0)
		q := quoteOf(t, r, candidateFor(t, p, "POST"))
		obs := r.Run(context.Background(), stepCall(q, (&payCounter{}).pay))
		// A provider echoing the credential back into an error message is the
		// provider's doing, but the observation is persisted and shown, so it
		// is a record we are responsible for.
		if strings.Contains(fmt.Sprintf("%+v", obs), secret) {
			t.Errorf("%s: the payment value leaked into the observation: %+v", name, obs)
		}
	}
}

func TestBuildSpec(t *testing.T) {
	// GET: flat input becomes a sorted query, merged with the endpoint's own.
	s, err := buildSpec("https://api.example.com/price?fixed=1", "GET", json.RawMessage(`{"symbol":"SOL","limit":5,"verbose":true}`))
	if err != nil || s.method != "GET" || len(s.body) != 0 || s.url != "https://api.example.com/price?fixed=1&limit=5&symbol=SOL&verbose=true" {
		t.Errorf("GET: %+v %v", s, err)
	}
	if _, err := buildSpec("https://api.example.com/p", "GET", json.RawMessage(`{"nested":{"a":1}}`)); err == nil {
		t.Error("a nested input can't go in a query string")
	}
	if _, err := buildSpec("https://api.example.com/p", "GET", json.RawMessage(`{"list":[1,2]}`)); err == nil {
		t.Error("an array can't go in a query string")
	}
	// POST: the input is the canonical JSON body, key order independent.
	a, _ := buildSpec("https://api.example.com/p", "POST", json.RawMessage(`{"b":2,"a":1}`))
	b, _ := buildSpec("https://api.example.com/p", "post", json.RawMessage(`{ "a": 1.0, "b": 2 }`))
	if string(a.body) != `{"a":1,"b":2}` || a.hash() != b.hash() {
		t.Errorf("POST bodies are canonical so the request hash is stable: %q %q", a.body, b.body)
	}
	// With no method: an empty input is a GET, anything else a POST.
	if s, _ := buildSpec("https://api.example.com/p", "", nil); s.method != "GET" || len(s.body) != 0 {
		t.Errorf("default for no input: %+v", s)
	}
	if s, _ := buildSpec("https://api.example.com/p", "", json.RawMessage(`{"x":1}`)); s.method != "POST" {
		t.Errorf("default with input: %+v", s)
	}
	if _, err := buildSpec("https://api.example.com/p", "TRACE", nil); err == nil {
		t.Error("unsupported methods are refused")
	}
	if _, err := buildSpec("https://api.example.com/p", "POST", json.RawMessage(`{nope`)); err == nil {
		t.Error("bad input is refused")
	}
	// Different input, different request hash.
	c, _ := buildSpec("https://api.example.com/p", "POST", json.RawMessage(`{"a":2,"b":2}`))
	if c.hash() == a.hash() {
		t.Error("the request hash must depend on the input")
	}
}

func TestRailMapping(t *testing.T) {
	r := newRunner(time.Second, 0)
	if rail, err := r.Rail(routing.Quote{Network: "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp"}); err != nil || rail != "x402-solana" {
		t.Errorf("CAIP-2 resolves to the canonical network's rail: %q %v", rail, err)
	}
	if _, err := r.Rail(routing.Quote{Network: "base"}); err == nil {
		t.Error("no rail for base: an error, not a guess")
	}
	if r.Type() != routing.ExecX402 {
		t.Error("type")
	}
}
