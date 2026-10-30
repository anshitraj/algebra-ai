package keyed

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
	"github.com/project-algebra/algebra/internal/platform/safehttp"
)

const secret = "sk-test-0123456789abcdef"

type keyStore map[string]string

func (k keyStore) Key(_ context.Context, userID, vendor string) (string, error) {
	if v, ok := k[userID+"|"+vendor]; ok {
		return v, nil
	}
	return "", shared.ErrNotFound
}

type passStore map[string]string

func (p passStore) Get(_ context.Context, id string) (*spendpass.Pass, error) {
	if u, ok := p[id]; ok {
		return &spendpass.Pass{ID: id, UserID: u}, nil
	}
	return nil, shared.ErrNotFound
}

// vendor is a fake vendor server: it records what it was sent and answers
// with whatever the test says.
type vendor struct {
	*httptest.Server
	mu     sync.Mutex
	status int
	reply  string
	got    []sent
}

type sent struct {
	method, path, query, header, body string
}

func newVendor(t *testing.T, header string) *vendor {
	t.Helper()
	v := &vendor{status: 200}
	v.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		v.mu.Lock()
		v.got = append(v.got, sent{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get(header), string(b)})
		status, reply := v.status, v.reply
		v.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(v.Close)
	return v
}

func (v *vendor) last(t *testing.T) sent {
	t.Helper()
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.got) == 0 {
		t.Fatal("the vendor was never called")
	}
	return v.got[len(v.got)-1]
}

func (v *vendor) calls() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.got)
}

func runnerFor(t *testing.T, endpoints map[string]string) (*Runner, *Tools) {
	t.Helper()
	tools := NewTools(endpoints)
	return NewRunner(Config{
		HTTP:   safehttp.New(safehttp.Options{AllowLoopback: true, Timeout: 5 * time.Second}),
		Keys:   keyStore{"user_1|tinyfish": secret, "user_1|exa": secret, "user_1|firecrawl": secret, "user_1|tavily": secret},
		Passes: passStore{"pass_1": "user_1", "pass_2": "user_2"},
		Tools:  tools,
	}), tools
}

// call runs one tool the way the coordinator does: price it, then run it with
// a Pay that records whether authority was asked for.
func call(t *testing.T, r *Runner, tools *Tools, vendorID, class, passID, input string) (app.StepObservation, *int) {
	t.Helper()
	var cand routing.Candidate
	for _, c := range tools.Candidates(vendorID) {
		if c.Capability == class {
			cand = c
		}
	}
	if cand.ID == "" {
		t.Fatalf("no %s tool for %s", class, vendorID)
	}
	q, err := r.Quote(context.Background(), cand, json.RawMessage(input))
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	q.Provider, q.Capability, q.Endpoint, q.Method = cand.Provider, cand.Capability, cand.Endpoint, cand.Method
	paid := new(int)
	obs := r.Run(context.Background(), app.StepCall{
		Quote: q, Input: json.RawMessage(input),
		Reservation: econ.Reservation{ID: "rsv_1", ExecutorPassID: passID, IdempotencyKey: "idem_1"},
		Pay: func(context.Context, app.PaymentRequest) (*app.PaymentAuthority, error) {
			*paid++
			return &app.PaymentAuthority{}, nil
		},
	})
	return obs, paid
}

func TestCandidates_AreKeyedHttpToolsPricedAtListPrice(t *testing.T) {
	tools := NewTools(nil)
	seen := map[string]bool{}
	for _, v := range tools.Vendors() {
		for _, c := range tools.Candidates(v.ID) {
			seen[c.Provider+" "+c.Capability] = true
			if c.ExecutionType != routing.ExecHTTPAPI || c.Network != chain.Keyed || c.Asset != "USDC" || c.PriceMinor < 1 {
				t.Errorf("%s %s: %+v", c.Provider, c.Capability, c)
			}
			if !strings.HasPrefix(c.Endpoint, "https://") || strings.Contains(c.Endpoint, "?") {
				t.Errorf("a vendor's own https address, no query: %s", c.Endpoint)
			}
			if c.Trust() != routing.TrustNative {
				t.Errorf("the person's own tool is trusted: %s", c.Trust())
			}
			if _, ok := routing.ClassByID(c.Capability); !ok {
				t.Errorf("%s is no class", c.Capability)
			}
		}
	}
	for _, want := range []string{
		"keyed:tinyfish web.search", "keyed:tinyfish web.scrape", "keyed:tinyfish web.agent",
		"keyed:exa web.search", "keyed:firecrawl web.scrape", "keyed:tavily web.search",
	} {
		if !seen[want] {
			t.Errorf("missing %s", want)
		}
	}
	if got := tools.Candidates("nobody"); len(got) != 0 {
		t.Errorf("an unknown vendor has no tools: %+v", got)
	}
}

func TestTinyFishSearch_GetsTheKeyInTheHeaderNotTheURL(t *testing.T) {
	v := newVendor(t, "X-API-Key")
	v.reply = `{"query":"solana","results":[{"position":1,"site_name":"Solana","title":"Solana","snippet":"fast chain","url":"https://solana.com"}],"total_results":1}`
	r, tools := runnerFor(t, map[string]string{"tinyfish/web.search": v.URL})
	obs, paid := call(t, r, tools, TinyFish, "web.search", "pass_1", `{"query":"solana payments"}`)
	if !obs.Delivered || !obs.AuthorityReleased || *paid != 1 {
		t.Fatalf("delivered after one payment authority: %+v paid=%d", obs, *paid)
	}
	got := v.last(t)
	if got.method != "GET" || got.header != secret || got.query != "query=solana+payments" {
		t.Errorf("sent %+v", got)
	}
	if strings.Contains(got.query, secret) {
		t.Error("the key never travels in the URL")
	}
	var out struct {
		Query   string `json:"query"`
		Results []hit  `json:"results"`
	}
	if err := json.Unmarshal(obs.Body, &out); err != nil || len(out.Results) != 1 || out.Results[0].Title != "Solana" || out.Results[0].Snippet != "fast chain" {
		t.Errorf("normalized: %s %v", obs.Body, err)
	}
	if obs.ChargedMinor != 0 || obs.SettleTx != "billed:idem_1" || obs.RequestHash == "" {
		t.Errorf("TinyFish search is free within its quota, billed under the attempt's id: %+v", obs)
	}
}

func TestTinyFishFetch_ReturnsThePageAndAFailedFetchIsNotDelivered(t *testing.T) {
	v := newVendor(t, "X-API-Key")
	v.reply = `{"results":[{"url":"https://example.com","final_url":"https://www.example.com/","title":"Example","description":"d","language":"en","format":"markdown","text":"# Hello"}],"errors":[]}`
	r, tools := runnerFor(t, map[string]string{"tinyfish/web.scrape": v.URL})
	obs, _ := call(t, r, tools, TinyFish, "web.scrape", "pass_1", `{"url":"https://example.com"}`)
	var page struct{ URL, Title, Markdown string }
	if !obs.Delivered || json.Unmarshal(obs.Body, &page) != nil || page.Markdown != "# Hello" || page.URL != "https://www.example.com/" {
		t.Fatalf("page: %+v %s", obs, obs.Body)
	}
	var sentBody struct {
		URLs   []string `json:"urls"`
		Format string   `json:"format"`
	}
	if err := json.Unmarshal([]byte(v.last(t).body), &sentBody); err != nil || len(sentBody.URLs) != 1 || sentBody.Format != "markdown" {
		t.Errorf("request: %s", v.last(t).body)
	}

	v.mu.Lock()
	v.reply = `{"results":[],"errors":[{"url":"https://example.com","error":"blocked by robots"}]}`
	v.mu.Unlock()
	obs, _ = call(t, r, tools, TinyFish, "web.scrape", "pass_1", `{"url":"https://example.com"}`)
	if obs.Delivered || obs.SettleTx != "" || obs.Class != routing.FailProvider || !strings.Contains(obs.Message, "blocked by robots") {
		t.Errorf("nothing came back: %+v", obs)
	}
}

func TestTinyFishAgent_IsBilledByTheStepAndAFailedRunIsFree(t *testing.T) {
	v := newVendor(t, "X-API-Key")
	v.reply = `{"run_id":"run_9","status":"COMPLETED","num_of_steps":7,"result":{"titles":["a","b"]},"error":null}`
	r, tools := runnerFor(t, map[string]string{"tinyfish/web.agent": v.URL})
	obs, _ := call(t, r, tools, TinyFish, "web.agent", "pass_1", `{"url":"https://example.com","goal":"list titles"}`)
	if !obs.Delivered || obs.ChargedMinor != 7*15_000 || obs.ProviderOperationID != "run_9" || obs.SettleTx != "billed:run_9" {
		t.Fatalf("seven steps at 1.5 cents: %+v", obs)
	}
	var out struct {
		Status string
		Result json.RawMessage
		Steps  int
	}
	if err := json.Unmarshal(obs.Body, &out); err != nil || out.Status != "completed" || out.Steps != 7 || !strings.Contains(string(out.Result), "titles") {
		t.Errorf("normalized: %s", obs.Body)
	}
	var sentBody map[string]any
	_ = json.Unmarshal([]byte(v.last(t).body), &sentBody)
	if sentBody["goal"] != "list titles" || sentBody["url"] != "https://example.com" {
		t.Errorf("request: %v", sentBody)
	}
	if cfg, _ := sentBody["agent_config"].(map[string]any); cfg["max_duration_seconds"] != float64(90) {
		t.Errorf("a run is cut off after ninety seconds: %v", sentBody)
	}

	v.mu.Lock()
	v.reply = `{"run_id":"run_10","status":"FAILED","num_of_steps":3,"result":null,"error":"the page needs a login"}`
	v.mu.Unlock()
	obs, _ = call(t, r, tools, TinyFish, "web.agent", "pass_1", `{"url":"https://example.com","goal":"list titles"}`)
	if obs.Delivered || obs.ChargedMinor != 0 || obs.SettleTx != "" || !strings.Contains(obs.Message, "login") {
		t.Errorf("a failed run costs nothing and is not a delivery: %+v", obs)
	}
}

func TestExa_ChargesWhatTheVendorReports(t *testing.T) {
	v := newVendor(t, "x-api-key")
	v.reply = `{"requestId":"req_1","results":[{"title":"T","url":"https://a.example","text":"hello"}],"costDollars":{"total":0.007}}`
	r, tools := runnerFor(t, map[string]string{"exa/web.search": v.URL})
	obs, _ := call(t, r, tools, Exa, "web.search", "pass_1", `{"query":"agents"}`)
	if !obs.Delivered || obs.ChargedMinor != 7_000 || obs.ProviderOperationID != "req_1" {
		t.Fatalf("Exa reported 0.007 dollars: %+v", obs)
	}
	if got := v.last(t); got.header != secret || got.method != "POST" || !strings.Contains(got.body, `"query":"agents"`) {
		t.Errorf("sent %+v", got)
	}
}

func TestFirecrawlAndTavily_SendTheKeyAsABearerAndChargeTheirListPrice(t *testing.T) {
	fc := newVendor(t, "Authorization")
	fc.reply = `{"success":true,"data":{"markdown":"# Doc","metadata":{"title":"Doc","sourceURL":"https://d.example"}}}`
	tv := newVendor(t, "Authorization")
	tv.reply = `{"query":"q","results":[{"title":"T","url":"https://a.example","content":"c","score":0.9}],"request_id":"tv_1"}`
	r, tools := runnerFor(t, map[string]string{"firecrawl/web.scrape": fc.URL, "tavily/web.search": tv.URL})

	obs, _ := call(t, r, tools, Firecrawl, "web.scrape", "pass_1", `{"url":"https://d.example"}`)
	if !obs.Delivered || obs.ChargedMinor != 1_000 || fc.last(t).header != "Bearer "+secret {
		t.Errorf("firecrawl: %+v sent %+v", obs, fc.last(t))
	}
	obs, _ = call(t, r, tools, Tavily, "web.search", "pass_1", `{"query":"q"}`)
	if !obs.Delivered || obs.ChargedMinor != 8_000 || tv.last(t).header != "Bearer "+secret {
		t.Errorf("tavily: %+v sent %+v", obs, tv.last(t))
	}
}

func TestRun_NoKeyMeansNothingIsAskedFor(t *testing.T) {
	v := newVendor(t, "X-API-Key")
	r, tools := runnerFor(t, map[string]string{"tinyfish/web.search": v.URL})
	// pass_2 belongs to user_2, who saved no key.
	obs, paid := call(t, r, tools, TinyFish, "web.search", "pass_2", `{"query":"x"}`)
	if obs.Delivered || *paid != 0 || obs.AuthorityReleased || v.calls() != 0 || obs.Class != routing.FailPolicy {
		t.Errorf("no key: refused before authority and before the vendor: %+v paid=%d calls=%d", obs, *paid, v.calls())
	}
	// A pass nobody owns.
	obs, paid = call(t, r, tools, TinyFish, "web.search", "pass_x", `{"query":"x"}`)
	if obs.Delivered || *paid != 0 || v.calls() != 0 {
		t.Errorf("unknown pass: %+v", obs)
	}
}

func TestRun_TheKeyGoesOnlyToTheVendorsOwnEndpoint(t *testing.T) {
	v := newVendor(t, "X-API-Key")
	r, tools := runnerFor(t, map[string]string{"tinyfish/web.search": v.URL})
	var cand routing.Candidate
	for _, c := range tools.Candidates(TinyFish) {
		if c.Capability == "web.search" {
			cand = c
		}
	}
	q, err := r.Quote(context.Background(), cand, json.RawMessage(`{"query":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	q.Provider, q.Capability, q.Method = cand.Provider, cand.Capability, cand.Method
	paid := 0
	run := func(endpoint, provider string) app.StepObservation {
		q := q
		q.Endpoint, q.Provider = endpoint, provider
		return r.Run(context.Background(), app.StepCall{
			Quote: q, Input: json.RawMessage(`{"query":"x"}`), Reservation: econ.Reservation{ExecutorPassID: "pass_1"},
			Pay: func(context.Context, app.PaymentRequest) (*app.PaymentAuthority, error) {
				paid++
				return &app.PaymentAuthority{}, nil
			},
		})
	}
	if obs := run("https://evil.example/steal", "keyed:tinyfish"); obs.Delivered || paid != 0 {
		t.Errorf("a quote naming another address gets no key: %+v", obs)
	}
	if obs := run(cand.Endpoint, "x402:evil.example"); obs.Delivered || paid != 0 {
		t.Errorf("a provider that is not a keyed vendor gets no key: %+v", obs)
	}
	if v.calls() != 0 {
		t.Errorf("the vendor server was reached %d times", v.calls())
	}
	if _, err := r.Quote(context.Background(), routing.Candidate{Provider: "keyed:tinyfish", Capability: "web.search", Endpoint: "https://evil.example/"}, json.RawMessage(`{"query":"x"}`)); err == nil {
		t.Error("a candidate with another endpoint is not priced")
	}
}

func TestRun_VendorFailuresAreNotDeliveriesAndNeverEchoTheKey(t *testing.T) {
	v := newVendor(t, "X-API-Key")
	r, tools := runnerFor(t, map[string]string{"tinyfish/web.search": v.URL})
	for status, want := range map[int]string{401: "refused the API key", 402: "out of credit", 429: "rate limiting", 500: "answered 500"} {
		v.mu.Lock()
		v.status, v.reply = status, `{"error":"bad key `+secret+`"}`
		v.mu.Unlock()
		obs, paid := call(t, r, tools, TinyFish, "web.search", "pass_1", `{"query":"x"}`)
		if obs.Delivered || obs.SettleTx != "" || obs.ChargedMinor != 0 || !obs.AuthorityReleased || *paid != 1 {
			t.Errorf("%d: %+v", status, obs)
		}
		if !strings.Contains(obs.Message, want) || strings.Contains(obs.Message, secret) {
			t.Errorf("%d: message %q should say %q and never the key", status, obs.Message, want)
		}
	}
	v.mu.Lock()
	v.status, v.reply = 200, `not json at all`
	v.mu.Unlock()
	if obs, _ := call(t, r, tools, TinyFish, "web.search", "pass_1", `{"query":"x"}`); obs.Delivered || obs.Class != routing.FailInvalid {
		t.Errorf("an unreadable answer is invalid: %+v", obs)
	}
}

func TestRun_BadInputIsRefusedBeforeAnythingIsSent(t *testing.T) {
	v := newVendor(t, "X-API-Key")
	r, tools := runnerFor(t, map[string]string{
		"tinyfish/web.search": v.URL, "tinyfish/web.scrape": v.URL, "tinyfish/web.agent": v.URL,
	})
	var cand routing.Candidate
	for _, c := range tools.Candidates(TinyFish) {
		if c.Capability == "web.agent" {
			cand = c
		}
	}
	for name, in := range map[string]string{
		"not an object":    `"hello"`,
		"no url":           `{"goal":"g"}`,
		"file url":         `{"url":"file:///etc/passwd","goal":"g"}`,
		"no goal":          `{"url":"https://example.com"}`,
		"goal too long":    `{"url":"https://example.com","goal":"` + strings.Repeat("g", 2001) + `"}`,
		"javascript url":   `{"url":"javascript:alert(1)","goal":"g"}`,
		"url without host": `{"url":"https://","goal":"g"}`,
	} {
		if _, err := r.Quote(context.Background(), cand, json.RawMessage(in)); err == nil {
			t.Errorf("%s was priced", name)
		}
	}
	if v.calls() != 0 {
		t.Errorf("nothing is sent while pricing: %d", v.calls())
	}
}

func TestRail_AuthorizesNothingToSpendAndSettlesOnlyWhatWasBilled(t *testing.T) {
	ctx := context.Background()
	rail := NewRail()
	auth, err := rail.Authorize(ctx, &econ.Reservation{ID: "rsv_1", ProviderID: "keyed:exa"}, app.PaymentRequest{})
	if err != nil || auth.AmountMinor != 0 {
		t.Fatalf("authority for no money: %+v %v", auth, err)
	}
	ev := auth.Evidence
	if ev.Rail != "keyed" || ev.Network != chain.Keyed || ev.PayTo != "exa" || ev.Payer != "your exa account" || ev.PaymentID == "" || ev.Test {
		t.Errorf("evidence: %+v", ev)
	}

	// A call that reported a charge is settled for it.
	billed := ev
	billed.Transaction, billed.AmountMinor = "billed:req_1", 7_000
	st, err := rail.Settlement(ctx, billed)
	if err != nil || st.Status != app.SettlementSettled || st.AmountMinor != 7_000 || st.Network != chain.Keyed {
		t.Errorf("billed: %+v %v", st, err)
	}
	// One that did not is provably not settled: the vendor never billed it.
	st, err = rail.Settlement(ctx, ev)
	if err != nil || st.Status != app.SettlementNotSettled {
		t.Errorf("not billed: %+v %v", st, err)
	}
	// A transaction that is not a billing record does not settle anything.
	forged := ev
	forged.Transaction, forged.AmountMinor = "5Kd3...signature", 1_000_000
	if st, _ := rail.Settlement(ctx, forged); st.Status != app.SettlementNotSettled {
		t.Errorf("only a billing record settles: %+v", st)
	}
}
