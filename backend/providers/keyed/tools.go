// Package keyed runs web tools (TinyFish, Exa, Firecrawl, Tavily) with the
// person's own API key.
//
// No money moves through Algebra for these: the vendor bills the person's own
// account. They still go through the same coordinator as every paid call, so a
// Spend Pass's budget, its rules and its ask-first gate apply to them, the
// router compares them with paid sellers, and the vendor's reported cost lands
// in the receipt and the task's P&L. The key is sealed at rest (app.ToolKeys),
// is read only here, only for the owner of the pass making the call, and is
// sent only to the vendor's own host.
package keyed

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"

	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/routing"
)

// Provider ids: "keyed:<vendor>".
const providerPrefix = "keyed:"

// ProviderID is the provider id of a vendor's keyed tools.
func ProviderID(vendor string) string { return providerPrefix + vendor }

// Vendor is one vendor whose tools can be used with the person's key.
type Vendor struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Site is where a person gets a key.
	Site string `json:"site"`
	// Tools names what the key opens, in plain words.
	Tools []string `json:"tools"`
}

// Vendor ids.
const (
	TinyFish  = "tinyfish"
	Exa       = "exa"
	Firecrawl = "firecrawl"
	Tavily    = "tavily"
)

// tool is one vendor endpoint behind a routing class.
type tool struct {
	vendor, class, name string
	endpoint            string
	method              string
	// listMinor is the vendor's list price per call in micro-USD: what a call
	// is quoted at and, when the vendor reports no cost, what it is charged.
	// Zero means free within the vendor's own quota.
	listMinor int64
	latencyMS int
	// header names the header the key travels in, and bearer whether it is
	// sent as "Bearer <key>".
	header string
	bearer bool
	// build turns the class input into the request's query string and body.
	build func(in input) (query url.Values, body []byte, err error)
	// read turns the vendor's answer into the class's shape, the cost it
	// reported (-1 when it reported none) and its operation id.
	read func(status int, body []byte) (out []byte, charged int64, op string, err error)
}

// quoteMinor is what a call is quoted at: a free tool still holds one unit, so
// the hold is the call's own and never the intent's whole ceiling.
func (t tool) quoteMinor() int64 { return max(t.listMinor, 1) }

// input is the class input every tool takes.
type input struct{ Query, URL, Goal string }

const (
	maxQuery = 400
	maxURL   = 2048
	maxGoal  = 2000
)

func parseInput(class string, raw json.RawMessage) (input, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return input{}, errors.New("the input must be a JSON object")
	}
	str := func(k string) string {
		var s string
		_ = json.Unmarshal(m[k], &s)
		return strings.TrimSpace(s)
	}
	in := input{Query: str("query"), URL: str("url"), Goal: str("goal")}
	switch class {
	case "web.search":
		if in.Query == "" || len(in.Query) > maxQuery {
			return in, fmt.Errorf("query is required and at most %d characters", maxQuery)
		}
	case "web.scrape", "web.agent":
		u, err := url.Parse(in.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || len(in.URL) > maxURL {
			return in, errors.New("url must be an http or https address")
		}
		if class == "web.agent" && (in.Goal == "" || len(in.Goal) > maxGoal) {
			return in, fmt.Errorf("goal is required and at most %d characters", maxGoal)
		}
	}
	return in, nil
}

// Tools is the set of tools Algebra can run with a key.
type Tools struct {
	list    []tool
	vendors []Vendor
}

// NewTools builds the set. endpoints overrides a tool's address by
// "<vendor>/<class>" (tests point it at a local server).
func NewTools(endpoints map[string]string) *Tools {
	t := &Tools{list: catalog(), vendors: vendors()}
	for i := range t.list {
		if e := endpoints[t.list[i].vendor+"/"+t.list[i].class]; e != "" {
			t.list[i].endpoint = e
		}
	}
	return t
}

// Vendors lists the vendors, in display order.
func (t *Tools) Vendors() []Vendor { return append([]Vendor(nil), t.vendors...) }

func (t *Tools) find(provider, class string) (tool, bool) {
	vendor, ok := strings.CutPrefix(provider, providerPrefix)
	if !ok {
		return tool{}, false
	}
	for _, x := range t.list {
		if x.vendor == vendor && x.class == class {
			return x, true
		}
	}
	return tool{}, false
}

// Candidates are a vendor's tools as routing candidates. Network "keyed" says
// the vendor, not a chain, bills the call; the price is the vendor's list
// price, a quote until the call reports what it cost.
func (t *Tools) Candidates(vendor string) []routing.Candidate {
	var out []routing.Candidate
	for _, x := range t.list {
		if x.vendor != vendor {
			continue
		}
		c, err := routing.Candidate{
			Capability: x.class, Provider: ProviderID(x.vendor), Name: x.displayName(),
			ExecutionType: routing.ExecHTTPAPI, Endpoint: x.endpoint, Method: x.method,
			PriceMinor: x.quoteMinor(), Asset: "USDC", Network: chain.Keyed,
			EstimatedLatencyMS: x.latencyMS,
			Sources:            []routing.DiscoverySource{routing.SourceConfigured},
		}.Normalize()
		if err == nil {
			out = append(out, c)
		}
	}
	return out
}

func (x tool) displayName() string {
	for _, v := range vendors() {
		if v.ID == x.vendor {
			return v.Name + " " + x.name + " (your key)"
		}
	}
	return x.name
}

func vendors() []Vendor {
	return []Vendor{
		{ID: TinyFish, Name: "TinyFish", Site: "https://www.tinyfish.ai", Tools: []string{"Search", "Fetch", "Agent"}},
		{ID: Exa, Name: "Exa", Site: "https://exa.ai", Tools: []string{"Search"}},
		{ID: Firecrawl, Name: "Firecrawl", Site: "https://www.firecrawl.dev", Tools: []string{"Scrape"}},
		{ID: Tavily, Name: "Tavily", Site: "https://tavily.com", Tools: []string{"Search"}},
	}
}

func catalog() []tool {
	return []tool{
		{vendor: TinyFish, class: "web.search", name: "Search", endpoint: "https://api.search.tinyfish.ai", method: http.MethodGet,
			latencyMS: 1200, header: "X-API-Key", build: tinyfishSearchReq, read: tinyfishSearchRead},
		{vendor: TinyFish, class: "web.scrape", name: "Fetch", endpoint: "https://api.fetch.tinyfish.ai", method: http.MethodPost,
			latencyMS: 3000, header: "X-API-Key", build: tinyfishFetchReq, read: tinyfishFetchRead},
		// An agent run is billed by the step (about 1.5 cents); a typical run
		// is twenty steps, and a run past ninety seconds is cut off.
		{vendor: TinyFish, class: "web.agent", name: "Agent", endpoint: "https://agent.tinyfish.ai/v1/automation/run", method: http.MethodPost,
			listMinor: 300_000, latencyMS: 60_000, header: "X-API-Key", build: tinyfishAgentReq, read: tinyfishAgentRead},
		{vendor: Exa, class: "web.search", name: "Search", endpoint: "https://api.exa.ai/search", method: http.MethodPost,
			listMinor: 10_000, latencyMS: 1500, header: "x-api-key", build: exaReq, read: exaRead},
		{vendor: Firecrawl, class: "web.scrape", name: "Scrape", endpoint: "https://api.firecrawl.dev/v2/scrape", method: http.MethodPost,
			listMinor: 1_000, latencyMS: 4000, header: "Authorization", bearer: true, build: firecrawlReq, read: firecrawlRead},
		{vendor: Tavily, class: "web.search", name: "Search", endpoint: "https://api.tavily.com/search", method: http.MethodPost,
			listMinor: 8_000, latencyMS: 1500, header: "Authorization", bearer: true, build: tavilyReq, read: tavilyRead},
	}
}

// What the tools hand back, in one shape per class whatever the vendor.

type hit struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

const (
	maxHits    = 10
	maxSnippet = 500
	maxPage    = 200_000
)

func searchOut(query string, hits []hit) []byte {
	if len(hits) > maxHits {
		hits = hits[:maxHits]
	}
	for i := range hits {
		hits[i].Snippet = cut(hits[i].Snippet, maxSnippet)
	}
	if hits == nil {
		hits = []hit{}
	}
	b, _ := json.Marshal(struct {
		Query   string `json:"query"`
		Results []hit  `json:"results"`
	}{query, hits})
	return b
}

func pageOut(pageURL, title, markdown string) []byte {
	b, _ := json.Marshal(struct {
		URL      string `json:"url"`
		Title    string `json:"title"`
		Markdown string `json:"markdown"`
	}{pageURL, title, cut(markdown, maxPage)})
	return b
}

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && n < len(s) && s[n]&0xC0 == 0x80 { // not mid-rune
		n--
	}
	return s[:n]
}

// vendorFailure is the vendor saying, in a well-formed answer, that it did not
// do the work. An answer that is not well-formed is invalid_response instead.
type vendorFailure struct{ error }

func failure(msg string) error { return vendorFailure{errors.New(msg)} }

// vendorError turns a failed status into words that leave the key out.
func vendorError(status int, body []byte) error {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return errors.New("the vendor refused the API key")
	case status == http.StatusPaymentRequired:
		return errors.New("the vendor says the account is out of credit")
	case status == http.StatusTooManyRequests:
		return errors.New("the vendor is rate limiting this key")
	}
	msg := strings.Join(strings.Fields(string(body)), " ")
	return fmt.Errorf("the vendor answered %d: %s", status, cut(msg, 120))
}

func usd(dollars float64) int64 { return int64(math.Round(dollars * 1_000_000)) }

// ---- TinyFish

func tinyfishSearchReq(in input) (url.Values, []byte, error) {
	return url.Values{"query": {in.Query}}, nil, nil
}

func tinyfishSearchRead(status int, body []byte) ([]byte, int64, string, error) {
	if status/100 != 2 {
		return nil, 0, "", vendorError(status, body)
	}
	var r struct {
		Query   string `json:"query"`
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Snippet string `json:"snippet"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, 0, "", errors.New("the answer was not the JSON the vendor documents")
	}
	hits := make([]hit, 0, len(r.Results))
	for _, x := range r.Results {
		hits = append(hits, hit{x.Title, x.URL, x.Snippet})
	}
	return searchOut(r.Query, hits), -1, "", nil
}

func tinyfishFetchReq(in input) (url.Values, []byte, error) {
	b, _ := json.Marshal(map[string]any{"urls": []string{in.URL}, "format": "markdown"})
	return nil, b, nil
}

func tinyfishFetchRead(status int, body []byte) ([]byte, int64, string, error) {
	if status/100 != 2 {
		return nil, 0, "", vendorError(status, body)
	}
	var r struct {
		Results []struct {
			URL      string `json:"url"`
			FinalURL string `json:"final_url"`
			Title    string `json:"title"`
			Text     string `json:"text"`
		} `json:"results"`
		Errors []struct {
			Error string `json:"error"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, 0, "", errors.New("the answer was not the JSON the vendor documents")
	}
	if len(r.Results) == 0 {
		why := "no page came back"
		if len(r.Errors) > 0 {
			why = cut(r.Errors[0].Error, 120)
		}
		return nil, 0, "", failure("the vendor could not fetch the page: " + why)
	}
	p := r.Results[0]
	u := p.FinalURL
	if u == "" {
		u = p.URL
	}
	return pageOut(u, p.Title, p.Text), -1, "", nil
}

func tinyfishAgentReq(in input) (url.Values, []byte, error) {
	b, _ := json.Marshal(map[string]any{"url": in.URL, "goal": in.Goal, "agent_config": map[string]any{"max_duration_seconds": 90}})
	return nil, b, nil
}

// tinyfishStepMinor is what one agent step costs on pay-as-you-go.
const tinyfishStepMinor = 15_000

func tinyfishAgentRead(status int, body []byte) ([]byte, int64, string, error) {
	if status/100 != 2 {
		return nil, 0, "", vendorError(status, body)
	}
	var r struct {
		RunID  string          `json:"run_id"`
		Status string          `json:"status"`
		Steps  int64           `json:"num_of_steps"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, 0, "", errors.New("the answer was not the JSON the vendor documents")
	}
	// A run that failed costs nothing, so it is not a delivery.
	if !strings.EqualFold(r.Status, "COMPLETED") {
		why := strings.Trim(string(r.Error), `"`)
		if why == "" || why == "null" {
			why = "the run ended " + strings.ToLower(r.Status)
		}
		return nil, 0, r.RunID, failure("the vendor's agent did not finish: " + cut(why, 120))
	}
	out, _ := json.Marshal(struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
		Steps  int64           `json:"steps"`
	}{"completed", orNull(r.Result), r.Steps})
	return out, r.Steps * tinyfishStepMinor, r.RunID, nil
}

func orNull(r json.RawMessage) json.RawMessage {
	if len(r) == 0 {
		return json.RawMessage("null")
	}
	return r
}

// ---- Exa

func exaReq(in input) (url.Values, []byte, error) {
	b, _ := json.Marshal(map[string]any{
		"query": in.Query, "type": "auto", "numResults": maxHits,
		"contents": map[string]any{"text": map[string]any{"maxCharacters": maxSnippet}},
	})
	return nil, b, nil
}

func exaRead(status int, body []byte) ([]byte, int64, string, error) {
	if status/100 != 2 {
		return nil, 0, "", vendorError(status, body)
	}
	var r struct {
		RequestID string `json:"requestId"`
		Results   []struct {
			Title string `json:"title"`
			URL   string `json:"url"`
			Text  string `json:"text"`
		} `json:"results"`
		Cost *struct {
			Total float64 `json:"total"`
		} `json:"costDollars"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, 0, "", errors.New("the answer was not the JSON the vendor documents")
	}
	hits := make([]hit, 0, len(r.Results))
	for _, x := range r.Results {
		hits = append(hits, hit{x.Title, x.URL, x.Text})
	}
	charged := int64(-1)
	if r.Cost != nil {
		charged = usd(r.Cost.Total)
	}
	return searchOut("", hits), charged, r.RequestID, nil
}

// ---- Firecrawl

func firecrawlReq(in input) (url.Values, []byte, error) {
	b, _ := json.Marshal(map[string]any{"url": in.URL, "formats": []string{"markdown"}, "onlyMainContent": true})
	return nil, b, nil
}

func firecrawlRead(status int, body []byte) ([]byte, int64, string, error) {
	if status/100 != 2 {
		return nil, 0, "", vendorError(status, body)
	}
	var r struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
		Data    struct {
			Markdown string `json:"markdown"`
			Metadata struct {
				Title     string `json:"title"`
				SourceURL string `json:"sourceURL"`
			} `json:"metadata"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, 0, "", errors.New("the answer was not the JSON the vendor documents")
	}
	if !r.Success {
		return nil, 0, "", failure("the vendor could not scrape the page: " + cut(r.Error, 120))
	}
	return pageOut(r.Data.Metadata.SourceURL, r.Data.Metadata.Title, r.Data.Markdown), -1, "", nil
}

// ---- Tavily

func tavilyReq(in input) (url.Values, []byte, error) {
	b, _ := json.Marshal(map[string]any{"query": in.Query, "max_results": maxHits})
	return nil, b, nil
}

// tavilyCreditMinor is what one Tavily credit costs pay-as-you-go.
const tavilyCreditMinor = 8_000

func tavilyRead(status int, body []byte) ([]byte, int64, string, error) {
	if status/100 != 2 {
		return nil, 0, "", vendorError(status, body)
	}
	var r struct {
		Query     string `json:"query"`
		RequestID string `json:"request_id"`
		Results   []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
		Usage *struct {
			Credits float64 `json:"credits"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, 0, "", errors.New("the answer was not the JSON the vendor documents")
	}
	hits := make([]hit, 0, len(r.Results))
	for _, x := range r.Results {
		hits = append(hits, hit{x.Title, x.URL, x.Content})
	}
	charged := int64(-1)
	if r.Usage != nil && r.Usage.Credits > 0 {
		charged = int64(math.Round(r.Usage.Credits * tavilyCreditMinor))
	}
	return searchOut(r.Query, hits), charged, r.RequestID, nil
}
