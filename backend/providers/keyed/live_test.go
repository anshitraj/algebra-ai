package keyed

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/platform/safehttp"
)

// recording keeps the vendor's last answer, so a failure (or KEYED_LIVE_RAW=1)
// shows what it really said. A run that is billed is not repeated to find out.
type recording struct {
	inner  HTTPDoer
	mu     sync.Mutex
	status int
	body   []byte
}

func (r *recording) Do(req *http.Request) (*safehttp.Response, error) {
	resp, err := r.inner.Do(req)
	if err == nil {
		r.mu.Lock()
		r.status, r.body = resp.Status, resp.Body
		r.mu.Unlock()
	}
	return resp, err
}

func (r *recording) answer(key string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.ReplaceAll(cut(string(r.body), 1500), key, "[redacted]")
}

// Live checks against the real vendors. Each runs only when its key is in the
// environment of the test process, so a plain `go test` never touches the
// network and no key is ever written to a file:
//
//	KEYED_LIVE_TINYFISH_KEY=... go test ./providers/keyed -run Live -v -count=1
//
// Also KEYED_LIVE_EXA_KEY, KEYED_LIVE_FIRECRAWL_KEY and KEYED_LIVE_TAVILY_KEY.
// A TinyFish Agent run costs credits, so it needs KEYED_LIVE_AGENT=1 as well.
// They run the real adapters (request, header, endpoint, answer) end to end,
// through the same Runner Algebra uses, with the vendor's own address.

var liveCases = []struct {
	vendor, env, class, input string
	paid                      bool
}{
	{TinyFish, "KEYED_LIVE_TINYFISH_KEY", "web.search", `{"query":"solana x402 agent payments"}`, false},
	{TinyFish, "KEYED_LIVE_TINYFISH_KEY", "web.scrape", `{"url":"https://example.com"}`, false},
	{TinyFish, "KEYED_LIVE_TINYFISH_KEY", "web.agent", `{"url":"https://example.com","goal":"Report the page's main heading"}`, true},
	{Exa, "KEYED_LIVE_EXA_KEY", "web.search", `{"query":"solana x402 agent payments"}`, false},
	{Firecrawl, "KEYED_LIVE_FIRECRAWL_KEY", "web.scrape", `{"url":"https://example.com"}`, false},
	{Tavily, "KEYED_LIVE_TAVILY_KEY", "web.search", `{"query":"solana x402 agent payments"}`, false},
}

func TestLive_Vendors(t *testing.T) {
	for _, c := range liveCases {
		t.Run(c.vendor+"/"+c.class, func(t *testing.T) {
			key := os.Getenv(c.env)
			if key == "" {
				t.Skipf("%s not set", c.env)
			}
			if c.paid && os.Getenv("KEYED_LIVE_AGENT") != "1" {
				t.Skip("an agent run costs credits: set KEYED_LIVE_AGENT=1")
			}
			tools := NewTools(nil)
			rec := &recording{inner: safehttp.New(safehttp.Options{Timeout: 120 * time.Second, MaxBody: 4 << 20})}
			r := NewRunner(Config{
				HTTP:   rec,
				Keys:   keyStore{"user_1|" + c.vendor: key},
				Passes: passStore{"pass_1": "user_1"},
				Tools:  tools,
			})
			started := time.Now()
			obs, paid := call(t, r, tools, c.vendor, c.class, "pass_1", c.input)
			t.Logf("%s %s: delivered=%v status=%d in %s, charged %d micro-USD, class=%q", c.vendor, c.class, obs.Delivered, obs.HTTPStatus, time.Since(started).Round(time.Millisecond), obs.ChargedMinor, obs.Class)
			if !obs.Delivered || os.Getenv("KEYED_LIVE_RAW") == "1" {
				t.Logf("the vendor answered %d: %s", rec.status, rec.answer(key))
			}
			if strings.Contains(obs.Message, key) || strings.Contains(string(obs.Body), key) {
				t.Fatal("the key must never come back in a message or an answer")
			}
			if !obs.Delivered || *paid != 1 {
				t.Fatalf("not delivered (payment authority asked %d times): class=%s message=%q", *paid, obs.Class, obs.Message)
			}
			if !strings.HasPrefix(obs.SettleTx, BilledPrefix) {
				t.Errorf("a delivered call carries a billing reference: %q", obs.SettleTx)
			}
			switch c.class {
			case "web.search":
				var out struct {
					Results []hit `json:"results"`
				}
				if err := json.Unmarshal(obs.Body, &out); err != nil || len(out.Results) == 0 {
					t.Fatalf("a search returns results: %v %s", err, obs.Body)
				}
				for i, h := range out.Results {
					if h.Title == "" || !strings.HasPrefix(h.URL, "http") {
						t.Errorf("result %d is missing its title or address: %+v", i, h)
					}
				}
				t.Logf("%d results; first: %q %s", len(out.Results), out.Results[0].Title, out.Results[0].URL)
			case "web.scrape":
				var page struct{ URL, Title, Markdown string }
				if err := json.Unmarshal(obs.Body, &page); err != nil || page.Markdown == "" || page.URL == "" {
					t.Fatalf("a page comes back with its text and address: %v %s", err, obs.Body)
				}
				t.Logf("page %s %q, %d characters of markdown", page.URL, page.Title, len(page.Markdown))
			case "web.agent":
				var run struct {
					Status string
					Steps  int
				}
				if err := json.Unmarshal(obs.Body, &run); err != nil || run.Status != "completed" {
					t.Fatalf("the run completed: %v %s", err, obs.Body)
				}
				t.Logf("agent run: %d steps, charged %d micro-USD", run.Steps, obs.ChargedMinor)
			}
		})
	}
}
