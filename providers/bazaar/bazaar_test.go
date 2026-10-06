package bazaar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/platform/safehttp"
	"github.com/project-algebra/algebra/providers/catalog"
)

const (
	usdc       = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
	devnetUSDC = "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU"
)

func fixture(t *testing.T, name string) []json.RawMessage {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Items
}

func listed(network string, raws ...json.RawMessage) []netItem {
	out := make([]netItem, len(raws))
	for i, r := range raws {
		out[i] = netItem{network: network, raw: r}
	}
	return out
}

// item builds a Circle-style discovery entry, with overrides applied to a valid default.
func item(mod func(m map[string]any)) json.RawMessage {
	m := map[string]any{
		"resource": "https://api.ok.example/v1/thing", "type": "http", "x402Version": 2, "lastUpdated": "2026-09-01T00:00:00Z",
		"accepts": []any{map[string]any{"scheme": "exact", "network": caipMainnet, "asset": usdc, "payTo": "PayTo1111111111111111111111111111", "amount": "2500"}},
		"metadata": map[string]any{
			"provider": map[string]any{"name": "Ok Provider", "website": "https://ok.example", "description": "fine", "category": "DATA_ENRICHMENT"},
			"method":   "GET", "description": "a thing", "siwx": false, "supportsVanillax402": true,
		},
	}
	if mod != nil {
		mod(m)
	}
	b, _ := json.Marshal(m)
	return b
}

func meta(m map[string]any) map[string]any { return m["metadata"].(map[string]any) }

func TestCircleRealSample(t *testing.T) {
	snap, err := parse(Circle(""), listed("solana", fixture(t, "circle-mainnet.json")...), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, p := range snap.providers {
		ids = append(ids, p.ID)
		if _, err := econ.NormalizeProvider(p.ID); err != nil {
			t.Errorf("%s: %v", p.ID, err)
		}
	}
	// The QuickNode entry needs a browser sign-in (SIWX), so it isn't listed.
	if strings.Join(ids, ",") != "circle:agentic-reservations,circle:birdeye,circle:exa" {
		t.Fatalf("providers: %v", ids)
	}
	b := snap.providers[snap.byID["circle:birdeye"]]
	if b.Name != "Birdeye" || b.Category != "finance" || b.Host != "public-api.birdeye.so" || b.EndpointCount != 3 || b.MinPriceMinor != 3000 ||
		b.Source != "circle" || b.Website != "https://birdeye.so" || b.PageURL != "https://agents.circle.com/services" || strings.Join(b.Networks, ",") != "solana" {
		t.Errorf("birdeye: %+v", b)
	}
	e := snap.endpoints["circle:birdeye"][0]
	if e.Capability != "circle.birdeye.get.x402-defi-historical_price_unix" || e.Network != "solana" || e.Pricing != "0.003 USDC" ||
		!e.Callable || len(e.Payments) != 1 || e.Payments[0].PayTo == "" {
		t.Errorf("endpoint: %+v", e)
	}
	if e := snap.endpoints["circle:agentic-reservations"][0]; e.Callable {
		t.Errorf("a templated path is listed but not callable: %+v", e)
	}
	for _, e := range snap.endpoints["circle:exa"] {
		if e.Method != "POST" || len(e.InputSchema) == 0 {
			t.Errorf("exa: %+v", e)
		}
	}
}

func TestPayAIRealSample(t *testing.T) {
	items := append(listed("solana", fixture(t, "payai-mainnet.json")...), listed("solana-devnet", fixture(t, "payai-devnet.json")...)...)
	snap, err := parse(PayAI(""), items, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]catalog.Provider{}
	for _, p := range snap.providers {
		byName[p.Name] = p
		if !strings.HasPrefix(p.ID, "payai:") || p.Source != "payai" || p.Website == "" || len(p.Networks) == 0 {
			t.Errorf("provider: %+v", p)
		}
	}
	doctor, ok := byName["x402 Doctor"]
	if !ok || doctor.LogoURL != "https://x402-doctor.fizzl.eu/icon.png" || doctor.MinPriceMinor != 1000 || strings.Join(doctor.Networks, ",") != "solana" {
		t.Errorf("a v2 entry with a service name and an icon: %+v", doctor)
	}
	// A v1 entry: network "solana", price in maxAmountRequired, named by its host.
	if v1, ok := byName["mpp.hyreagent.fun"]; !ok || v1.MinPriceMinor != 80000 {
		t.Errorf("a v1 entry: %+v (%v)", v1, ok)
	}
	// Devnet entries are payable in devnet USDC and say so.
	dev := 0
	for _, p := range snap.providers {
		if len(p.Networks) == 1 && p.Networks[0] == "solana-devnet" {
			dev++
			e := snap.endpoints[p.ID][0]
			if e.Network != "solana-devnet" || e.Payments[0].Network != "solana-devnet" {
				t.Errorf("devnet endpoint: %+v", e)
			}
		}
	}
	if dev != 2 {
		t.Errorf("devnet providers = %d, want 2", dev)
	}
}

func TestOneEndpointOnBothClusters(t *testing.T) {
	both := item(func(m map[string]any) {
		m["accepts"] = []any{
			map[string]any{"scheme": "exact", "network": caipMainnet, "asset": usdc, "payTo": "Main", "amount": "3000"},
			map[string]any{"scheme": "exact", "network": caipDevnet, "asset": devnetUSDC, "payTo": "Dev", "amount": "1000"},
		}
	})
	snap, err := parse(Circle(""), append(listed("solana", both), listed("solana-devnet", both)...), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	p := snap.providers[0]
	e := snap.endpoints[p.ID][0]
	if strings.Join(p.Networks, ",") != "solana,solana-devnet" || len(e.Payments) != 2 || e.Network != "solana" || e.PriceMinor != 3000 || p.EndpointCount != 1 {
		t.Fatalf("one endpoint, two networks, mainnet terms as the headline: %+v %+v", p, e)
	}
	cands, dropped := catalog.Candidates(&catalog.Detail{Provider: p, Endpoints: []catalog.Endpoint{e}}, routing.SourceCircle, nil)
	if len(cands) != 2 || len(dropped) != 0 {
		t.Fatalf("a candidate per network: %d %v", len(cands), dropped)
	}
	nets := map[string]routing.Candidate{}
	for _, c := range cands {
		nets[c.Network] = c
	}
	if nets["solana"].AssetAddress != usdc || nets["solana"].PriceMinor != 3000 || nets["solana-devnet"].AssetAddress != devnetUSDC || nets["solana-devnet"].PriceMinor != 1000 {
		t.Errorf("each network in its own USDC at its own price: %+v", nets)
	}
	if nets["solana"].ID == nets["solana-devnet"].ID {
		t.Error("the two networks are different candidates")
	}
}

func TestParseDropsWhatCantBePaidOrTrusted(t *testing.T) {
	good := item(nil)
	bad := []json.RawMessage{
		item(func(m map[string]any) { meta(m)["siwx"] = true }),
		item(func(m map[string]any) { meta(m)["supportsVanillax402"] = false }),
		item(func(m map[string]any) { m["type"] = "mcp" }),
		item(func(m map[string]any) { m["resource"] = "http://api.ok.example/plain" }),
		item(func(m map[string]any) { m["resource"] = "https://10.0.0.1/private" }),
		item(func(m map[string]any) { m["resource"] = "https://user:pw@api.ok.example/creds" }),
		item(func(m map[string]any) { meta(m)["method"] = "TRACE" }),
		item(func(m map[string]any) { meta(m)["provider"].(map[string]any)["name"] = "  !!  " }),
		item(func(m map[string]any) { // USDT only
			m["accepts"] = []any{map[string]any{"scheme": "exact", "network": caipMainnet, "asset": "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB", "amount": "1"}}
		}),
		item(func(m map[string]any) { // Base only
			m["accepts"] = []any{map[string]any{"scheme": "exact", "network": "eip155:8453", "asset": "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913", "amount": "1"}}
		}),
		item(func(m map[string]any) { // mainnet USDC's address on devnet is not devnet USDC; listed as mainnet below
			m["accepts"] = []any{map[string]any{"scheme": "exact", "network": caipDevnet, "asset": usdc, "amount": "1"}}
		}),
		item(func(m map[string]any) {
			m["accepts"] = []any{map[string]any{"scheme": "exact", "network": caipMainnet, "asset": usdc, "amount": "-5"}}
		}),
		item(func(m map[string]any) {
			m["accepts"] = []any{map[string]any{"scheme": "upto", "network": caipMainnet, "asset": usdc, "amount": "5"}}
		}),
		json.RawMessage(`{"resource": 42}`),
		json.RawMessage(`"not an object"`),
		good,
	}
	snap, err := parse(Circle(""), listed("solana", append([]json.RawMessage{good}, bad...)...), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.providers) != 1 || snap.providers[0].EndpointCount != 1 || snap.endpoints["circle:ok-provider"][0].PriceMinor != 2500 {
		t.Fatalf("only the good entry survives, once: %+v", snap.providers)
	}
	// An entry listed under devnet with mainnet terms is not payable there.
	if s, err := parse(Circle(""), listed("solana-devnet", good), time.Now()); err == nil && len(s.providers) != 0 {
		t.Error("terms for another cluster don't count")
	}
	if _, err := parse(Circle(""), listed("solana", bad[:13]...), time.Now()); err == nil {
		t.Error("entries with none usable means something changed: an error")
	}
}

func TestLogoMustBePublicHTTPS(t *testing.T) {
	for raw, want := range map[string]string{
		"https://x.example/icon.png":                         "https://x.example/icon.png",
		"http://x.example/icon.png":                          "",
		"javascript:alert(1)":                                "",
		"https://127.0.0.1/a.png":                            "",
		"data:image/png;base64,AAAA":                         "",
		"https://" + strings.Repeat("a", 600) + ".example/x": "",
	} {
		if got := catalog.LogoURL(raw); got != want {
			t.Errorf("LogoURL(%.40q) = %q, want %q", raw, got, want)
		}
	}
}

// --- the client, against a fake directory ---

type fakeDir struct {
	srv   *httptest.Server
	mu    sync.Mutex
	pages map[string][]json.RawMessage // network value -> items
	hits  int
	down  bool
	query []string
}

func newFakeDir(t *testing.T, pages map[string][]json.RawMessage) *fakeDir {
	f := &fakeDir{pages: pages}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.hits++
		f.query = append(f.query, r.URL.RawQuery)
		if f.down {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		q := r.URL.Query()
		items := f.pages[q.Get("network")]
		off, _ := strconv.Atoi(q.Get("offset"))
		limit, _ := strconv.Atoi(q.Get("limit"))
		page := []json.RawMessage{}
		if off < len(items) {
			page = items[off:min(off+limit, len(items))]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": page, "pagination": map[string]int{"limit": limit, "offset": off, "total": len(items)}})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

func client(profile Profile, f *fakeDir, clk *clock) *Client {
	profile.URL = f.srv.URL
	return New(Config{Profile: profile, HTTP: safehttp.New(safehttp.Options{AllowLoopback: true}), TTL: time.Minute, StaleFor: time.Hour, Now: clk.now})
}

func TestClientPagesThroughBothClusters(t *testing.T) {
	var main []json.RawMessage
	for i := range 450 {
		main = append(main, item(func(m map[string]any) {
			m["resource"] = fmt.Sprintf("https://api.ok.example/v1/thing-%d", i)
			meta(m)["provider"].(map[string]any)["name"] = fmt.Sprintf("Provider %d", i%3)
		}))
	}
	devOnly := item(func(m map[string]any) {
		m["resource"] = "https://dev.example/x"
		meta(m)["provider"].(map[string]any)["name"] = "Devnet Thing"
		m["accepts"] = []any{map[string]any{"scheme": "exact", "network": caipDevnet, "asset": devnetUSDC, "payTo": "D", "amount": "10"}}
	})
	f := newFakeDir(t, map[string][]json.RawMessage{caipMainnet: main, caipDevnet: {devOnly}})
	c := client(Circle(""), f, &clock{t: time.Unix(1_800_000_000, 0)})
	ctx := context.Background()

	all, err := c.List(ctx, catalog.Filter{})
	if err != nil || all.Total != 4 {
		t.Fatalf("3 mainnet providers + 1 devnet: %v %+v", err, all)
	}
	if f.hits != 4 {
		t.Errorf("3 mainnet pages and 1 devnet page: %d", f.hits)
	}
	for _, q := range f.query {
		if !strings.Contains(q, "supportsVanillax402=true") || !strings.Contains(q, "siwx=false") || !strings.Contains(q, "limit=200") {
			t.Errorf("query %q", q)
		}
	}
	if dev, _ := c.List(ctx, catalog.Filter{Network: "solana-devnet"}); dev.Total != 1 || dev.Providers[0].Name != "Devnet Thing" {
		t.Errorf("devnet filter: %+v", dev)
	}
	if mainnet, _ := c.List(ctx, catalog.Filter{Network: "solana"}); mainnet.Total != 3 {
		t.Errorf("mainnet filter: %+v", mainnet)
	}
}

func TestPayAIAsksForBothNetworkSpellings(t *testing.T) {
	f := newFakeDir(t, map[string][]json.RawMessage{
		caipMainnet: fixture(t, "payai-mainnet.json")[:3], "solana": fixture(t, "payai-mainnet.json")[3:],
		"solana-devnet": fixture(t, "payai-devnet.json"),
	})
	c := client(PayAI(""), f, &clock{t: time.Unix(1_800_000_000, 0)})
	l, err := c.List(context.Background(), catalog.Filter{})
	if err != nil || l.Total < 6 || f.hits != 4 {
		t.Fatalf("v2 and v1 spellings for each cluster: %v total=%d hits=%d", err, l.Total, f.hits)
	}
}

func TestClientLookups(t *testing.T) {
	f := newFakeDir(t, map[string][]json.RawMessage{caipMainnet: fixture(t, "circle-mainnet.json")})
	c := client(Circle(""), f, &clock{t: time.Unix(1_800_000_000, 0)})
	ctx := context.Background()
	for _, id := range []string{"circle:birdeye", "birdeye", "CIRCLE:Birdeye"} {
		if d, err := c.Detail(ctx, id); err != nil || d.ID != "circle:birdeye" || len(d.Endpoints) != 3 {
			t.Fatalf("%s: %v", id, err)
		}
	}
	if _, err := c.Detail(ctx, "circle:nobody"); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("unknown: %v", err)
	}
	got, err := c.CandidatesFor(ctx, "circle:exa", "circle.exa.post.search")
	if err != nil || len(got) != 1 || got[0].Endpoint != "https://api.exa.ai/search" || got[0].Network != "solana" || got[0].AssetAddress != usdc {
		t.Fatalf("candidates: %v %+v", err, got)
	}
	if got, err := c.ForCapability(ctx, "circle.exa.post.search"); err != nil || len(got) != 1 {
		t.Errorf("for capability: %v %v", err, got)
	}
	if _, err := c.ForCapability(ctx, "payai.exa.post.search"); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("another directory's capability: %v", err)
	}
}

func TestClientServesStaleWhenDown(t *testing.T) {
	f := newFakeDir(t, map[string][]json.RawMessage{caipMainnet: fixture(t, "circle-mainnet.json")})
	clk := &clock{t: time.Unix(1_800_000_000, 0)}
	c := client(Circle(""), f, clk)
	if _, err := c.List(context.Background(), catalog.Filter{}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.down = true
	f.mu.Unlock()
	clk.add(2 * time.Minute)
	if l, err := c.List(context.Background(), catalog.Filter{}); err != nil || !l.Stale || l.Total != 3 {
		t.Fatalf("older copy: %v %+v", err, l)
	}
	clk.add(2 * time.Hour)
	if _, err := c.List(context.Background(), catalog.Filter{}); !errors.Is(err, catalog.ErrUnavailable) {
		t.Errorf("too old: %v", err)
	}
}

func TestClientRefusesNonPublicAddresses(t *testing.T) {
	f := newFakeDir(t, map[string][]json.RawMessage{caipMainnet: fixture(t, "circle-mainnet.json")})
	p := Circle("")
	p.URL = f.srv.URL
	c := New(Config{Profile: p, HTTP: safehttp.New(safehttp.Options{})})
	if _, err := c.List(context.Background(), catalog.Filter{}); !errors.Is(err, catalog.ErrUnavailable) || f.hits != 0 {
		t.Errorf("loopback must not be read: %v hits=%d", err, f.hits)
	}
}
