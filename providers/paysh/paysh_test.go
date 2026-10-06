package paysh

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/platform/safehttp"
)

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// --- pure functions ---

func TestMicroUSDC(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"0.0015", 1500, true},
		{"7.5", 7_500_000, true},
		{"0.027925", 27925, true}, // a float would give 27926 or 27924
		{"0.000035", 35, true},
		{"0.166252", 166252, true},
		{"1e-05", 10, true},
		{"0.0000001", 1, true}, // rounds up: a price is never understated
		{"0", 0, true},
		{"200", 200_000_000, true},
		{"", 0, false},
		{".5", 0, false},
		{"-1", 0, false},
		{"abc", 0, false},
		{"1/2", 0, false},
		{"0x10", 0, false},
		{"1e999", 0, false},
		{"999999999999", 0, false}, // beyond a million USDC
	}
	for _, c := range cases {
		got, ok := microUSDC(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("microUSDC(%q) = %d, %v; want %d, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestCleanText(t *testing.T) {
	if got := cleanText("  hello\x00\x1b[31m   wor\nld\t! \xe2\x80\x8b\xef\xbb\xbf", 100); got != "hello [31m wor ld !" {
		t.Errorf("control characters and spacing: %q", got)
	}
	if got := cleanText("a\xffb", 100); got != "a b" {
		t.Errorf("invalid UTF-8: %q", got)
	}
	got := cleanText(strings.Repeat("é", 50), 10)
	if n := len([]rune(got)); n != 10 || !strings.HasSuffix(got, "…") {
		t.Errorf("truncation counts characters, not bytes: %q (%d)", got, n)
	}
	if cleanText("\x00\x00", 10) != "" {
		t.Error("nothing but control characters is nothing")
	}
}

func TestCapabilityID(t *testing.T) {
	cases := []struct{ fqn, method, path, want string }{
		{"birdeye/data", "GET", "x402/defi/token_security", "birdeye.data.get.x402-defi-token_security"},
		{"solana-foundation/google/vision", "POST", "v1/images:annotate", "solana-foundation.google.vision.post.v1-images-annotate"},
		{"a/b", "DELETE", "api_keys", "a.b.delete.api_keys"},
		{"a/b", "GET", "{id}", "a.b.get.id"},
		{"a/b", "GET", "---", "a.b.get.root"},
	}
	for _, c := range cases {
		if got := capabilityID(c.fqn, c.method, c.path); got != c.want {
			t.Errorf("capabilityID(%q, %q, %q) = %q, want %q", c.fqn, c.method, c.path, got, c.want)
		}
	}

	// Too long for 64 characters: cut, and ended with a hash of the whole so
	// two long ones can't collide. Still a valid capability, and stable.
	fqn := "solana-foundation/alibaba/intelligentspeechinteraction"
	one := capabilityID(fqn, "POST", "v1/a-very-long-operation-name/one")
	two := capabilityID(fqn, "POST", "v1/a-very-long-operation-name/two")
	if one == two {
		t.Fatalf("different endpoints must not share a capability: %q", one)
	}
	if one != capabilityID(fqn, "POST", "v1/a-very-long-operation-name/one") {
		t.Error("the same endpoint must always get the same capability")
	}
	if len(one) != 64 || !regexp.MustCompile(`-[0-9a-f]{6}$`).MatchString(one) {
		t.Errorf("a long capability is cut to 64 characters and ends in a hash: %q (%d)", one, len(one))
	}
	if _, err := econ.NormalizeCapability(one); err != nil {
		t.Errorf("a derived capability must be one the coordinator accepts: %v", err)
	}
}

func TestParseEndpointTable(t *testing.T) {
	rows := parseEndpointTable(string(fixture(t, "google-translate.md")))
	if len(rows) != 7 {
		t.Fatalf("rows = %d, want 7", len(rows))
	}
	first := rows[0]
	if first.method != "POST" || first.path != "v3/projects/{projectsId}/locations/{locationsId}:detectLanguage" ||
		first.pricing != "$0.001/requests" || first.description != "Detects the language of text within a request." {
		t.Errorf("first row: %+v", first)
	}

	birdeye := parseEndpointTable(string(fixture(t, "birdeye-data.md")))
	if len(birdeye) != 11 || birdeye[0].path != "x402/defi/historical_price_unix" || birdeye[0].pricing != "free" {
		t.Errorf("birdeye rows: %d %+v", len(birdeye), birdeye[0])
	}

	if got := parseEndpointTable("# no table here\n\nthe end\n"); got != nil {
		t.Errorf("a page without a table has no endpoints, got %v", got)
	}

	md := "intro\n## Endpoint Table\n| Method | Path | Pricing | Description |\n| --- | --- | --- | --- |\n" +
		"| GET | a | free | pipe \\| inside |\n| GET | too | few |\n| POST | `/b` | $1/requests | ok |\n\nAfter the table.\n| GET | not | in | table |\n"
	rows = parseEndpointTable(md)
	if len(rows) != 2 || rows[0].description != "pipe | inside" || rows[1].path != "b" {
		t.Errorf("escaped pipes, short rows and the end of the table: %+v", rows)
	}
}

func TestBuildEndpoints(t *testing.T) {
	p := Provider{FQN: "x/y", ServiceURL: "https://gw.example/base"}
	rows := []endpointRow{
		{"GET", "a/b", "$0.0015/requests", "plain"},
		{"GET", "free-one", "free", ""},
		{"POST", "zero", "$0/requests", ""},
		{"PUT", "put/it", "$0.1/requests", ""},
		{"TRACE", "bad-method", "free", ""},
		{"GET", "../etc/passwd", "free", ""},
		{"GET", "has space", "free", ""},
		{"GET", "v1/{id}/x", "$0.002/requests", "templated"},
		{"GET", "unclear", "contact us", ""},
		{"DELETE", strings.Repeat("a", maxPathLen+1), "free", ""},
		{"GET", "", "free", ""},
	}
	got := buildEndpoints(p, rows)
	byPath := map[string]Endpoint{}
	for _, e := range got {
		byPath[e.Path] = e
	}
	if len(got) != 6 {
		t.Fatalf("usable endpoints = %d, want 6: %+v", len(got), got)
	}
	if e := byPath["a/b"]; !e.Callable || e.URL != "https://gw.example/base/a/b" || e.PriceMinor != 1500 || e.Free || e.Capability != "x.y.get.a-b" {
		t.Errorf("a priced endpoint: %+v", e)
	}
	if e := byPath["free-one"]; !e.Free || e.PriceMinor != 0 || !e.Callable {
		t.Errorf("a free endpoint: %+v", e)
	}
	if e := byPath["zero"]; !e.Free {
		t.Errorf("$0 is free: %+v", e)
	}
	if e := byPath["v1/{id}/x"]; e.Callable || e.Reason == "" || e.PriceMinor != 2000 {
		t.Errorf("a templated path is listed but can't be called yet: %+v", e)
	}
	if e := byPath["unclear"]; !e.Callable || e.Free || e.PriceMinor != 0 {
		t.Errorf("an unreadable price is not free: %+v", e)
	}
}

// --- the catalog document ---

func TestParseCatalog_RealSample(t *testing.T) {
	cat, err := parseCatalog(fixture(t, "catalog.json"), DefaultDocsURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.providers) != 9 {
		t.Fatalf("providers = %d, want 9", len(cat.providers))
	}
	if !cat.generated.Equal(time.Date(2026, 9, 30, 19, 0, 33, 0, time.UTC)) {
		t.Errorf("generated_at = %v", cat.generated)
	}
	names := make([]string, len(cat.providers))
	for i, p := range cat.providers {
		names[i] = strings.ToLower(p.Name)
		if _, err := econ.NormalizeProvider(p.ID); err != nil {
			t.Errorf("%s isn't a provider ID the coordinator accepts: %v", p.ID, err)
		}
	}
	if !slices.IsSorted(names) {
		t.Errorf("listings are sorted by name: %v", names)
	}

	p, _ := cat.find("solana-foundation/google/vision")
	if p.ID != "paysh:solana-foundation.google.vision" || p.Host != "vision.google.gateway-402.com" || p.Category != "ai_ml" ||
		p.MinPriceMinor != 1500 || p.MaxPriceMinor != 1500 || p.EndpointCount != 2 || p.Currency != "USDC" || p.Source != "pay.sh" ||
		p.PageURL != "https://pay.sh/api/solana-foundation/google/vision" {
		t.Errorf("google vision: %+v", p)
	}
	q, ok := cat.find("paysh:quicknode.rpc")
	if !ok || q.MinPriceMinor != 1000 || q.MaxPriceMinor != 1_000_000 || !q.FreeTier {
		t.Errorf("quicknode, found by provider ID: %+v", q)
	}
	if w, _ := cat.find("dynamic/wallets"); w.EndpointCount != 0 || w.MaxPriceMinor != 0 {
		t.Errorf("a provider with no endpoints: %+v", w)
	}
}

func TestParseCatalog_HostileEntriesAreDroppedOneByOne(t *testing.T) {
	good := `{"fqn":"ok/one","title":"Fine","description":"fine","category":"data","service_url":"https://api.ok.example","min_price_usd":0.01,"max_price_usd":0.02}`
	bad := []string{
		`{"fqn":"Bad Name!","title":"x","service_url":"https://a.example"}`,
		`{"fqn":"a/b/c/d/e","title":"too deep","service_url":"https://a.example"}`,
		`{"fqn":"../../etc","title":"traversal","service_url":"https://a.example"}`,
		`{"fqn":"plain/http","title":"x","service_url":"http://a.example"}`,
		`{"fqn":"user/info","title":"x","service_url":"https://user:pass@a.example"}`,
		`{"fqn":"no/host","title":"x","service_url":"https:///path"}`,
		`{"fqn":"js/scheme","title":"x","service_url":"javascript:alert(1)"}`,
		`{"fqn":"q/uery","title":"x","service_url":"https://a.example/?k=v"}`,
		`{"fqn":"wrong/types","title":"x","service_url":"https://a.example","endpoint_count":"many"}`,
		`{"fqn":"ip/literal","title":"x","service_url":"https://10.1.2.3/api"}`,
		`{"fqn":"ip/zero","title":"x","service_url":"https://0"}`,
		`{"fqn":"ip/hex","title":"x","service_url":"https://0x7f.1/"}`,
		`{"fqn":"ip/six","title":"x","service_url":"https://[::1]/"}`,
		`{"fqn":"one/label","title":"x","service_url":"https://localhost/"}`,
		`"not even an object"`,
		`null`,
		`{"fqn":"ok/one","title":"duplicate of the good one","service_url":"https://evil.example"}`,
	}
	// The first entry with a name wins, so the duplicate (in bad) can't replace the good one.
	doc := `{"version":2,"providers":[` + strings.Join(append([]string{good}, bad...), ",") + `]}`
	cat, err := parseCatalog([]byte(doc), DefaultDocsURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.providers) != 1 || cat.providers[0].Host != "api.ok.example" {
		t.Fatalf("only the good entry should survive, and the duplicate must not replace it: %+v", cat.providers)
	}

	// Text is bounded and cleaned; garbage prices and categories don't break the entry.
	nasty := `{"fqn":"n/asty","title":"` + strings.Repeat("T", 500) + `\u0000\u001b","description":"` + strings.Repeat("d", 5000) +
		`","category":"Not A Category!","service_url":"https://n.example/","min_price_usd":"free","max_price_usd":1e999,"endpoint_count":-5}`
	cat, err = parseCatalog([]byte(`{"providers":[`+nasty+`]}`), DefaultDocsURL)
	if err != nil || len(cat.providers) != 1 {
		t.Fatalf("a messy entry is still usable: %v %v", err, cat)
	}
	p := cat.providers[0]
	if len([]rune(p.Name)) > maxTitle || len([]rune(p.Description)) > maxDescription || p.Category != "other" ||
		p.EndpointCount != 0 || p.MinPriceMinor != 0 || p.MaxPriceMinor != 0 || p.ServiceURL != "https://n.example" {
		t.Errorf("bounds and defaults: %+v", p)
	}
	if strings.ContainsAny(p.Name, "\x00\x1b") {
		t.Error("control characters must not survive")
	}

	// A price range never ends below where it starts, even if the catalog only gave one end.
	cat, err = parseCatalog([]byte(`{"providers":[{"fqn":"m/m","title":"x","service_url":"https://m.example","min_price_usd":"0.5"}]}`), DefaultDocsURL)
	if err != nil || len(cat.providers) != 1 || cat.providers[0].MinPriceMinor != 500_000 || cat.providers[0].MaxPriceMinor != 500_000 {
		t.Errorf("one-sided price range: %v %+v", err, cat)
	}
}

func TestParseCatalog_Errors(t *testing.T) {
	if _, err := parseCatalog([]byte(`not json`), DefaultDocsURL); err == nil {
		t.Error("invalid JSON must be an error")
	}
	if _, err := parseCatalog([]byte(`{"providers":[{"fqn":"BAD"},{"fqn":"ALSO BAD"}]}`), DefaultDocsURL); err == nil {
		t.Error("providers with none usable means the format changed: an error, not an empty catalog")
	}
	if cat, err := parseCatalog([]byte(`{"providers":[]}`), DefaultDocsURL); err != nil || len(cat.providers) != 0 {
		t.Errorf("an honestly empty catalog is empty, not an error: %v", err)
	}
}

// --- candidates ---

func TestDetailCandidates(t *testing.T) {
	cat, _ := parseCatalog(fixture(t, "catalog.json"), DefaultDocsURL)
	p, _ := cat.find("birdeye/data")
	d := &Detail{Provider: p, Endpoints: buildEndpoints(p, parseEndpointTable(string(fixture(t, "birdeye-data.md")))), FetchedAt: time.Unix(1_800_000_000, 0)}
	cands, dropped := candidatesOf(d)
	if len(cands) != 11 || len(dropped) != 0 {
		t.Fatalf("candidates = %d, dropped = %v", len(cands), dropped)
	}
	c := cands[0]
	if c.Provider != "paysh:birdeye.data" || c.Capability != "birdeye.data.get.x402-defi-historical_price_unix" || c.ExecutionType != routing.ExecX402 ||
		c.Endpoint != "https://public-api.birdeye.so/x402/defi/historical_price_unix" || c.Method != "GET" || c.Network != "solana" || c.Asset != "USDC" ||
		!slices.Equal(c.Sources, []routing.DiscoverySource{routing.SourcePaySh}) || c.Trust() != routing.TrustListed || c.ID == "" {
		t.Errorf("candidate: %+v", c)
	}

	// Templated paths are listed, not offered.
	tp, _ := cat.find("solana-foundation/google/translate")
	td := &Detail{Provider: tp, Endpoints: buildEndpoints(tp, parseEndpointTable(string(fixture(t, "google-translate.md"))))}
	if len(td.Endpoints) != 7 {
		t.Fatalf("endpoints = %d", len(td.Endpoints))
	}
	if c, _ := candidatesOf(td); len(c) != 0 {
		t.Errorf("every Google Translate path has parameters, so none is callable yet: %d", len(c))
	}

	// A provider whose gateway is a private address is dropped with a reason, not offered.
	private := Provider{ID: "paysh:p.q", FQN: "p/q", Name: "Private", ServiceURL: "https://10.1.2.3/api"}
	pd := &Detail{Provider: private, Endpoints: buildEndpoints(private, []endpointRow{{"GET", "x", "free", ""}})}
	if c, dr := candidatesOf(pd); len(c) != 0 || len(dr) != 1 || !strings.Contains(dr[0].Reason, "private") {
		t.Errorf("a private address must be refused: %v %v", c, dr)
	}
}

// --- the client ---

type fakePaySh struct {
	srv *httptest.Server

	mu      sync.Mutex
	catalog []byte
	pages   map[string][]byte
	hits    map[string]int
	down    bool
	gate    chan struct{}
}

func newFakePaySh(t *testing.T) *fakePaySh {
	t.Helper()
	f := &fakePaySh{catalog: fixture(t, "catalog.json"), hits: map[string]int{}, pages: map[string][]byte{
		"birdeye/data":                       fixture(t, "birdeye-data.md"),
		"solana-foundation/google/vision":    fixture(t, "google-vision.md"),
		"solana-foundation/google/translate": fixture(t, "google-translate.md"),
	}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakePaySh) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.hits[r.URL.Path]++
	down, gate, catalog := f.down, f.gate, f.catalog
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
	if down {
		http.Error(w, "unavailable", http.StatusInternalServerError)
		return
	}
	switch path := r.URL.Path; {
	case path == "/api/catalog":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(catalog)
	case strings.HasPrefix(path, "/api/") && strings.HasSuffix(path, "/index.md"):
		f.mu.Lock()
		page, ok := f.pages[strings.TrimSuffix(strings.TrimPrefix(path, "/api/"), "/index.md")]
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write(page)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakePaySh) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[path]
}

func (f *fakePaySh) setDown(v bool) {
	f.mu.Lock()
	f.down = v
	f.mu.Unlock()
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newTestClient(f *fakePaySh, clk *clock, doer HTTPDoer) *Client {
	if doer == nil {
		doer = safehttp.New(safehttp.Options{AllowLoopback: true})
	}
	return New(Config{
		CatalogURL: f.srv.URL + "/api/catalog", DocsURL: f.srv.URL + "/api", HTTP: doer,
		CatalogTTL: time.Minute, DetailTTL: 10 * time.Minute, StaleFor: time.Hour, Now: clk.now,
	})
}

func newClock() *clock { return &clock{t: time.Unix(1_800_000_000, 0)} }

func TestClient_ListFiltersAndCounts(t *testing.T) {
	f := newFakePaySh(t)
	c := newTestClient(f, newClock(), nil)
	ctx := context.Background()

	all, err := c.List(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if all.Total != 9 || all.Count != 9 || all.Source != "pay.sh" || all.Stale || len(all.Categories) == 0 {
		t.Fatalf("all: total=%d count=%d stale=%v", all.Total, all.Count, all.Stale)
	}
	sum := 0
	for _, cat := range all.Categories {
		sum += cat.Count
	}
	if sum != 9 || all.Categories[0].Count < all.Categories[len(all.Categories)-1].Count {
		t.Errorf("categories sum to the catalog, most providers first: %+v", all.Categories)
	}

	fin, _ := c.List(ctx, Filter{Category: "FINANCE"})
	if fin.Total == 0 || fin.Providers[0].Category != "finance" || len(fin.Categories) != len(all.Categories) {
		t.Errorf("a category filter narrows the page but not the category counts: %+v", fin.Categories)
	}
	q, _ := c.List(ctx, Filter{Query: "  OCR  "})
	if q.Total == 0 {
		t.Error("a query matches names, descriptions and use cases, ignoring case")
	}
	none, _ := c.List(ctx, Filter{Query: "no such thing anywhere"})
	if none.Total != 0 || none.Providers == nil || len(none.Providers) != 0 {
		t.Errorf("no match is an empty list, not null: %+v", none)
	}
	page, _ := c.List(ctx, Filter{Limit: 2, Offset: 1})
	if page.Total != 9 || page.Count != 2 || page.Providers[0].ID != all.Providers[1].ID {
		t.Errorf("paging: %+v", page)
	}
	if past, _ := c.List(ctx, Filter{Offset: 100}); past.Count != 0 {
		t.Errorf("an offset past the end is an empty page: %d", past.Count)
	}
	// Pay.sh is mainnet only: nothing on devnet, not even in the category counts.
	if dev, _ := c.List(ctx, Filter{Network: "solana-devnet"}); dev.Total != 0 || len(dev.Categories) != 0 {
		t.Errorf("devnet: %+v", dev)
	}
	if main, _ := c.List(ctx, Filter{Network: "solana"}); main.Total != 9 {
		t.Errorf("mainnet: %d", main.Total)
	}
	if f.count("/api/catalog") != 1 {
		t.Errorf("one fetch served every call: %d", f.count("/api/catalog"))
	}
}

func TestClient_DetailAndCandidates(t *testing.T) {
	f := newFakePaySh(t)
	c := newTestClient(f, newClock(), nil)
	ctx := context.Background()

	d, err := c.Detail(ctx, "paysh:birdeye.data")
	if err != nil {
		t.Fatal(err)
	}
	if d.Name == "" || len(d.Endpoints) != 11 || d.Endpoints[0].Capability != "birdeye.data.get.x402-defi-historical_price_unix" {
		t.Fatalf("detail: %s %d", d.Name, len(d.Endpoints))
	}
	if byFQN, err := c.Detail(ctx, "birdeye/data"); err != nil || byFQN.ID != d.ID {
		t.Errorf("a provider can be asked for by its FQN: %v", err)
	}
	d.Endpoints[0].Path = "changed by the caller"
	if again, _ := c.Detail(ctx, "birdeye/data"); again.Endpoints[0].Path == "changed by the caller" {
		t.Error("callers get copies; they can't change what is cached")
	}
	if n := f.count("/api/birdeye/data/index.md"); n != 1 {
		t.Errorf("the provider page was fetched %d times, want 1", n)
	}

	cands, err := c.CandidatesFor(ctx, "paysh:birdeye.data", "birdeye.data.get.x402-defi-price")
	if err != nil || len(cands) != 1 || cands[0].Endpoint != "https://public-api.birdeye.so/x402/defi/price" {
		t.Fatalf("candidates for a capability: %v %+v", err, cands)
	}
	if none, err := c.CandidatesFor(ctx, "birdeye/data", "birdeye.data.get.nothing"); err != nil || len(none) != 0 {
		t.Errorf("an unknown capability has no candidates: %v %v", err, none)
	}
	if _, err := c.Detail(ctx, "paysh:nobody.here"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown provider: %v", err)
	}
	if _, err := c.Detail(ctx, "paysh:quicknode.rpc"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("a provider whose page can't be read is unavailable, not empty: %v", err)
	}
}

func TestClient_ForCapabilityFindsTheProviderFromThePrefix(t *testing.T) {
	f := newFakePaySh(t)
	f.catalog = []byte(`{"providers":[
	  {"fqn":"a/b","title":"AB","category":"data","service_url":"https://ab.example"},
	  {"fqn":"a.b/c","title":"ABC","category":"data","service_url":"https://abc.example"}]}`)
	f.pages["a/b"] = []byte("## Endpoint Table\n| Method | Path | Pricing | Description |\n| --- | --- | --- | --- |\n| GET | x | $0.001/requests | d |\n")
	f.pages["a.b/c"] = []byte("## Endpoint Table\n| Method | Path | Pricing | Description |\n| --- | --- | --- | --- |\n| GET | x | $0.002/requests | d |\n")
	c := newTestClient(f, newClock(), nil)
	ctx := context.Background()

	got, err := c.ForCapability(ctx, "a.b.get.x")
	if err != nil || len(got) != 1 || got[0].Provider != "paysh:a.b" || got[0].PriceMinor != 1000 {
		t.Fatalf("a.b.get.x: %v %+v", err, got)
	}
	got, err = c.ForCapability(ctx, "A.B.C.get.x")
	if err != nil || len(got) != 1 || got[0].Provider != "paysh:a.b.c" || got[0].PriceMinor != 2000 {
		t.Fatalf("the longest matching provider wins: %v %+v", err, got)
	}
	if _, err := c.ForCapability(ctx, "solana.token-risk"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a capability that isn't Pay.sh's: %v", err)
	}
}

func TestClient_CachesWithinTheTTL(t *testing.T) {
	f := newFakePaySh(t)
	clk := newClock()
	c := newTestClient(f, clk, nil)
	ctx := context.Background()
	for range 5 {
		if _, err := c.List(ctx, Filter{}); err != nil {
			t.Fatal(err)
		}
	}
	clk.advance(30 * time.Second)
	_, _ = c.List(ctx, Filter{})
	if f.count("/api/catalog") != 1 {
		t.Fatalf("within the TTL nothing is fetched again: %d", f.count("/api/catalog"))
	}
	clk.advance(31 * time.Second)
	if l, err := c.List(ctx, Filter{}); err != nil || l.Stale {
		t.Fatalf("after the TTL it refreshes: %v", err)
	}
	if f.count("/api/catalog") != 2 {
		t.Errorf("fetches after the TTL: %d", f.count("/api/catalog"))
	}
}

func TestClient_ServesAnOlderCopyWhenPaySHIsDown(t *testing.T) {
	f := newFakePaySh(t)
	clk := newClock()
	c := newTestClient(f, clk, nil)
	ctx := context.Background()
	if _, err := c.List(ctx, Filter{}); err != nil {
		t.Fatal(err)
	}

	f.setDown(true)
	clk.advance(2 * time.Minute) // past the TTL
	l, err := c.List(ctx, Filter{})
	if err != nil || !l.Stale || l.Total != 9 {
		t.Fatalf("an outage serves the older copy, marked stale: %v %+v", err, l)
	}
	hits := f.count("/api/catalog")
	for range 5 {
		if l, _ := c.List(ctx, Filter{}); !l.Stale {
			t.Fatal("still stale")
		}
	}
	if f.count("/api/catalog") != hits {
		t.Errorf("a failed refresh isn't retried on every request: %d -> %d", hits, f.count("/api/catalog"))
	}
	clk.advance(31 * time.Second)
	_, _ = c.List(ctx, Filter{})
	if f.count("/api/catalog") != hits+1 {
		t.Errorf("it retries after a pause: %d", f.count("/api/catalog"))
	}

	clk.advance(2 * time.Hour) // beyond TTL + StaleFor
	if _, err := c.List(ctx, Filter{}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("a copy that old isn't served: %v", err)
	}

	f.setDown(false)
	clk.advance(31 * time.Second)
	if l, err := c.List(ctx, Filter{}); err != nil || l.Stale {
		t.Errorf("it recovers when Pay.sh does: %v", err)
	}
}

func TestClient_NothingCachedAndPaySHDownIsUnavailable(t *testing.T) {
	f := newFakePaySh(t)
	f.setDown(true)
	c := newTestClient(f, newClock(), nil)
	if _, err := c.List(context.Background(), Filter{}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("got %v", err)
	}
}

func TestClient_ConcurrentCallersShareOneFetch(t *testing.T) {
	f := newFakePaySh(t)
	f.gate = make(chan struct{})
	c := newTestClient(f, newClock(), nil)

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.List(context.Background(), Filter{})
			errs <- err
		}()
	}
	time.Sleep(100 * time.Millisecond) // let them all arrive while the fetch is held
	close(f.gate)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	if n := f.count("/api/catalog"); n != 1 {
		t.Errorf("twenty callers at once made %d fetches, want 1", n)
	}
}

func TestClient_ACallerCanGiveUpWithoutFailingTheOthers(t *testing.T) {
	f := newFakePaySh(t)
	f.gate = make(chan struct{})
	c := newTestClient(f, newClock(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := c.List(ctx, Filter{}); done <- err }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("the impatient caller gets its own cancellation: %v", err)
	}
	close(f.gate)
	if _, err := c.List(context.Background(), Filter{}); err != nil {
		t.Errorf("the fetch it started still completed for the next caller: %v", err)
	}
}

func TestClient_ReadsOnlyThroughTheSafeClient(t *testing.T) {
	f := newFakePaySh(t)
	// A client that refuses loopback, like production's: the fake server is on loopback.
	c := newTestClient(f, newClock(), safehttp.New(safehttp.Options{}))
	if _, err := c.List(context.Background(), Filter{}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("a non-public address must not be read: %v", err)
	}
	if f.count("/api/catalog") != 0 {
		t.Error("nothing should have reached the server")
	}

	small := newTestClient(f, newClock(), safehttp.New(safehttp.Options{AllowLoopback: true, MaxBody: 100}))
	if _, err := small.List(context.Background(), Filter{}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("a body over the limit is refused, not truncated: %v", err)
	}
}

// --- fuzz: whatever a third party sends, nothing panics and nothing invalid gets through ---

func FuzzParseEndpointTable(f *testing.F) {
	for _, name := range []string{"google-translate.md", "birdeye-data.md", "google-vision.md"} {
		f.Add(string(fixture(f, name)))
	}
	f.Add("## Endpoint Table\n|||\n| | | | |\n| GET | \x00 | | |\n")
	p := Provider{FQN: "x/y", ServiceURL: "https://gw.example"}
	f.Fuzz(func(t *testing.T, md string) {
		for _, e := range buildEndpoints(p, parseEndpointTable(md)) {
			if _, err := econ.NormalizeCapability(e.Capability); err != nil {
				t.Fatalf("capability %q: %v", e.Capability, err)
			}
			if !strings.HasPrefix(e.URL, "https://gw.example/") || strings.Contains(e.Path, "..") || !slices.Contains(httpMethods, e.Method) {
				t.Fatalf("endpoint %+v", e)
			}
			if e.Callable && strings.ContainsAny(e.Path, "{}") {
				t.Fatalf("a templated path can't be callable: %+v", e)
			}
		}
	})
}

func FuzzParseCatalog(f *testing.F) {
	f.Add(fixture(f, "catalog.json"))
	f.Add([]byte(`{"providers":[{"fqn":"a/b","service_url":"https://x.example","min_price_usd":1e-400}]}`))
	f.Add([]byte(`[]`))
	f.Fuzz(func(t *testing.T, doc []byte) {
		cat, err := parseCatalog(doc, DefaultDocsURL)
		if err != nil {
			return
		}
		seen := map[string]bool{}
		for _, p := range cat.providers {
			if _, err := econ.NormalizeProvider(p.ID); err != nil {
				t.Fatalf("provider ID %q: %v", p.ID, err)
			}
			if seen[p.ID] || !strings.HasPrefix(p.ServiceURL, "https://") || p.MinPriceMinor < 0 || p.MaxPriceMinor < p.MinPriceMinor {
				t.Fatalf("provider %+v", p)
			}
			seen[p.ID] = true
		}
	})
}
