package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	wsdomain "github.com/project-algebra/algebra/internal/domain/websearch"
)

// fakeGoogle serves both the generateContent endpoint and the grounding
// redirect paths, so the test exercises the real request, parse, verify and
// resolve path end to end.
func fakeGoogle(t *testing.T, modelText string) (*httptest.Server, *Gemini) {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ":generateContent"):
			if r.Header.Get("x-goog-api-key") != "test-key" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"google_search"`) {
				t.Errorf("request did not enable google_search grounding: %s", body)
			}
			text := strings.ReplaceAll(modelText, "REDIRECT", "https://"+srv.Listener.Addr().String()+groundingRedirectPath)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": text}}}}},
			})
		case strings.HasPrefix(r.URL.Path, groundingRedirectPath+"blinkit"):
			w.Header().Set("Location", "https://blinkit.com/prn/coke-zero/prid/1")
			w.WriteHeader(http.StatusFound)
		case strings.HasPrefix(r.URL.Path, groundingRedirectPath+"zepto"):
			w.Header().Set("Location", "https://www.zeptonow.com/pn/coke-zero/pvid/2")
			w.WriteHeader(http.StatusFound)
		case strings.HasPrefix(r.URL.Path, groundingRedirectPath+"reddit"):
			w.Header().Set("Location", "https://www.reddit.com/r/dealsforindia/comments/abc/coke_zero_24_pack/")
			w.WriteHeader(http.StatusFound)
		case strings.HasPrefix(r.URL.Path, groundingRedirectPath+"desidime"):
			w.Header().Set("Location", "https://www.desidime.com/deals/coke-zero-bank-offer")
			w.WriteHeader(http.StatusFound)
		case strings.HasPrefix(r.URL.Path, groundingRedirectPath+"blog"):
			w.Header().Set("Location", "https://some-coupon-blog.example.com/coke")
			w.WriteHeader(http.StatusFound)
		case strings.HasPrefix(r.URL.Path, groundingRedirectPath+"internal"):
			w.Header().Set("Location", "https://169.254.169.254/latest/meta-data")
			w.WriteHeader(http.StatusFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	g := NewGemini(GeminiConfig{APIKey: "test-key", APIBase: srv.URL + "/v1beta", HTTPClient: srv.Client()})
	g.redirectHost = srv.Listener.Addr().String()
	return srv, g
}

func TestGemini_KeepsOnlyVerifiedGroundedListings(t *testing.T) {
	text := "```json\n" + `[
	  {"name":"Coca-Cola Zero Sugar","variant":"300 ml can","price_inr":38,"store":"Blinkit","url":"REDIRECTblinkit/abc"},
	  {"name":"Coke Zero PET","variant":"250 ml","price_inr":20,"store":"","url":"REDIRECTzepto/def"},
	  {"name":"Invented listing","variant":"1 L","price_inr":99,"store":"BigBasket","url":"https://www.bigbasket.com/pd/99999/made-up"},
	  {"name":"SSRF bait","variant":"","price_inr":1,"store":"x","url":"REDIRECTinternal/zzz"},
	  {"name":"Blinkit dup","variant":"300 ml can","price_inr":38,"store":"Blinkit","url":"REDIRECTblinkit/abc"}
	]` + "\n```"
	_, g := fakeGoogle(t, text)

	got, err := g.Search(context.Background(), "Coke Zero", 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 verified listings (invented URL, SSRF redirect and duplicate dropped), got %d: %+v", len(got), got)
	}
	if got[0].URL != "https://blinkit.com/prn/coke-zero/prid/1" || got[0].PriceMinorUnits != 3800 || got[0].Currency != "INR" || got[0].Store != "Blinkit" {
		t.Errorf("first listing wrong: %+v", got[0])
	}
	if got[1].Store != "Zeptonow" {
		t.Errorf("empty store should fall back to the resolved host, got %q", got[1].Store)
	}
	for _, r := range got {
		if strings.Contains(r.URL, groundingRedirectPath) {
			t.Errorf("returned an unresolved redirect: %s", r.URL)
		}
	}
}

func TestGemini_BadKeyIsAClearError(t *testing.T) {
	srv, _ := fakeGoogle(t, "[]")
	g := NewGemini(GeminiConfig{APIKey: "wrong", APIBase: srv.URL + "/v1beta", HTTPClient: srv.Client()})
	if _, err := g.Search(context.Background(), "coke", 5); err == nil || !strings.Contains(err.Error(), "rejected the API key") {
		t.Errorf("want API-key error, got %v", err)
	}
}

func TestGemini_UnparseableAnswerIsEmptyNotError(t *testing.T) {
	_, g := fakeGoogle(t, "Sorry, I couldn't find anything.")
	got, err := g.Search(context.Background(), "unobtainium", 5)
	if err != nil || len(got) != 0 {
		t.Errorf("want (empty, nil), got (%v, %v)", got, err)
	}
}

func TestGemini_ModelPathIsEscaped(t *testing.T) {
	g := NewGemini(GeminiConfig{APIKey: "k", Model: "../evil"})
	if !strings.Contains(g.cfg.APIBase+"/models/"+url.PathEscape(g.cfg.Model), "..%2Fevil") {
		t.Error(fmt.Sprintf("model name must be path-escaped: %s", url.PathEscape(g.cfg.Model)))
	}
}

func TestPageKind(t *testing.T) {
	cases := map[string]pageClass{
		"https://www.flipkart.com/timex-tweg360smu06t-analog-watch-men/p/itm4a1b2c3": pageProduct,
		"https://www.flipkart.com/q/timex-watches":                                   pageListing,
		"https://www.flipkart.com/watches/timex~brand/pr?sid=r18,f13":                pageListing,
		"https://blinkit.com/prn/coca-cola-zero/prid/487321":                         pageProduct,
		"https://blinkit.com/brand/timex":                                            pageListing,
		"https://www.zepto.com/pn/ferrero-rocher/pvid/9f3a":                          pageProduct,
		"https://www.bigbasket.com/pd/40001234/lays-chips/":                          pageProduct,
		"https://www.bigbasket.com/pc/snacks/":                                       pageListing,
		"https://www.amazon.in/Casio-Enticer-MTP-V002L-1BUDF/dp/B00XYZ":              pageProduct,
		"https://www.amazon.in/s?k=casio":                                            pageListing,
		"https://shop.timexindia.com/blogs/timex/top-watches":                        pageArticle,
		"https://shop.brand.in/products/classic-watch":                               pageProduct,
		"https://shop.brand.in/collections/men":                                      pageListing,
		"https://www.jiomart.com/":                                                   pageListing,
	}
	for u, want := range cases {
		if got := pageKind(u); got != want {
			t.Errorf("pageKind(%s) = %d, want %d", u, got, want)
		}
	}
}

func TestStoreMatchesHost(t *testing.T) {
	if !storeMatchesHost("Swiggy Instamart", "https://www.swiggy.com/instamart/item/1") {
		t.Error("Swiggy Instamart should match swiggy.com")
	}
	if storeMatchesHost("Flipkart", "https://pricehistory.app/p/casio") {
		t.Error("a price tracker's page labelled Flipkart must not keep that label")
	}
	if got := storeFromHost("https://pricehistory.app/p/casio"); got != "Pricehistory" {
		t.Errorf("relabel = %q", got)
	}
}

func TestGemini_CommunityTipsOnlyFromTheChosenSites(t *testing.T) {
	text := `[
	  {"title":"Coke Zero 24 pack at 499","summary":"Amazon price drop","code":"","price_inr":499,"posted":"2 days ago","url":"REDIRECTreddit/1"},
	  {"title":"Extra 10% off on Coke","summary":"with code","code":"cola10","price_inr":null,"posted":"","url":"REDIRECTdesidime/2"},
	  {"title":"Best coupons","summary":"","code":"use this code now!!","price_inr":null,"posted":"","url":"REDIRECTblog/3"},
	  {"title":"Made up","summary":"","code":"FAKE","price_inr":1,"posted":"","url":"https://www.reddit.com/r/dealsforindia/comments/zzz"}
	]`
	_, g := fakeGoogle(t, text)
	sites := []wsdomain.CommunitySite{
		{Filter: "reddit.com/r/dealsforindia", Host: "reddit.com"},
		{Filter: "desidime.com", Host: "desidime.com"},
	}
	got, err := g.SearchCommunity(context.Background(), "Coke Zero", sites, 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want the Reddit and DesiDime posts only (off-site blog and ungrounded link dropped), got %+v", got)
	}
	if got[0].Source != "r/dealsforindia" || got[0].PriceMinorUnits != 49900 || got[0].Posted != "2 days ago" {
		t.Errorf("reddit tip wrong: %+v", got[0])
	}
	if got[1].Source != "DesiDime" || got[1].Code != "COLA10" {
		t.Errorf("desidime tip wrong: %+v", got[1])
	}
}

func TestTavily_RecentPostsFromChosenSubredditsOnly(t *testing.T) {
	// Gemini fake: extraction only (no grounding expected).
	gem := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), `"google_search"`) {
			t.Error("extraction must not run a web search")
		}
		text := `[{"i":0,"summary":"MuscleBlaze Whey 1kg at ₹1,899 on Amazon","code":"mb200","price_inr":1899,"posted":""},
		          {"i":1,"summary":"ignored","code":"","price_inr":999,"posted":""},
		          {"i":7,"summary":"out of range","code":"","price_inr":1,"posted":""}]`
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": text}}}}},
		})
	}))
	t.Cleanup(gem.Close)
	var sent map[string]any
	tav := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tvly-test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&sent)
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{
			map[string]any{"title": "MuscleBlaze whey deal", "url": "https://www.reddit.com/r/IndianFitness/comments/a/mb_whey/", "content": "₹1899 with code MB200", "published_date": "2026-09-23T10:00:00Z"},
			map[string]any{"title": "Other sub", "url": "https://www.reddit.com/r/pcmasterrace/comments/b/x/", "content": "not chosen"},
			map[string]any{"title": "Blog", "url": "https://coupons.example.com/whey", "content": "spam"},
		}})
	}))
	t.Cleanup(tav.Close)

	g := NewGemini(GeminiConfig{APIKey: "test-key", APIBase: gem.URL + "/v1beta", HTTPClient: gem.Client()})
	tv := NewTavily(TavilyConfig{APIKey: "tvly-test", APIBase: tav.URL, HTTPClient: tav.Client()}, g)
	tv.now = func() time.Time { return time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC) }

	sites := []wsdomain.CommunitySite{{Filter: "reddit.com/r/IndianFitness", Host: "reddit.com", Path: "/r/IndianFitness"}}
	got, err := tv.SearchCommunity(context.Background(), "whey protein under 2000", sites, 6)
	if err != nil {
		t.Fatal(err)
	}
	if sent["time_range"] != "week" || !strings.Contains(fmt.Sprint(sent["query"]), "r/IndianFitness") {
		t.Errorf("search should be bounded to the last week and scoped to the subreddit: %v", sent)
	}
	if len(got) != 1 {
		t.Fatalf("want only the post from the chosen subreddit, got %+v", got)
	}
	tip := got[0]
	if tip.Source != "r/IndianFitness" || tip.Code != "MB200" || tip.PriceMinorUnits != 189900 || tip.Posted != "2 days ago" {
		t.Errorf("tip wrong: %+v", tip)
	}
}
