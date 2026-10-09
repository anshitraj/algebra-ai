package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/project-algebra/algebra/connectors/sanitize"
	"github.com/project-algebra/algebra/internal/domain/merchant"
	wsdomain "github.com/project-algebra/algebra/internal/domain/websearch"
)

const (
	DefaultGeminiAPIBase = "https://generativelanguage.googleapis.com/v1beta"
	// DefaultGeminiModel is Google's stable "latest Flash" alias: fast, and
	// grounded search doesn't need a Pro model.
	DefaultGeminiModel = "gemini-flash-latest"

	// groundingRedirectHost serves the result links Google Search grounding
	// hands the model. Only links on this host are trusted: a URL the model
	// typed out itself may be invented (observed in testing — product IDs
	// that change between identical calls), so it is dropped.
	groundingRedirectHost = "vertexaisearch.cloud.google.com"
	groundingRedirectPath = "/grounding-api-redirect/"
)

// GeminiConfig configures the Gemini-grounded shopping search.
type GeminiConfig struct {
	APIKey string
	// Model defaults to DefaultGeminiModel.
	Model string
	// Region biases the search ("India" by default).
	Region string
	// APIBase/HTTPClient exist for tests.
	APIBase    string
	HTTPClient *http.Client
}

// Gemini is a WebSearcher backed by Gemini with Google Search grounding: a
// real, live Google search whose result links come back through Google's
// grounding redirect. Each kept result is resolved to the store's own URL
// and checked by merchant.ValidatePublicHTTPSURL before anyone sees it.
type Gemini struct {
	cfg          GeminiConfig
	http         *http.Client
	redirectHost string // groundingRedirectHost; overridden only in tests
}

func NewGemini(cfg GeminiConfig) *Gemini {
	if cfg.Model == "" {
		cfg.Model = DefaultGeminiModel
	}
	if cfg.Region == "" {
		cfg.Region = "India"
	}
	if cfg.APIBase == "" {
		cfg.APIBase = DefaultGeminiAPIBase
	}
	cfg.APIBase = strings.TrimSuffix(cfg.APIBase, "/")
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 45 * time.Second}
	}
	client := *hc
	// Never follow redirects with the API key attached; redirect resolution
	// below reads Location itself.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Gemini{cfg: cfg, http: &client, redirectHost: groundingRedirectHost}
}

type geminiListing struct {
	Name     string   `json:"name"`
	Variant  string   `json:"variant"`
	PriceINR *float64 `json:"price_inr"`
	Store    string   `json:"store"`
	URL      string   `json:"url"`
	Delivery string   `json:"delivery"`
}

var jsonArray = regexp.MustCompile(`(?s)\[.*\]`)

// Search runs one grounded search and returns up to limit shopping
// listings, most relevant first.
func (g *Gemini) Search(ctx context.Context, query string, limit int) ([]wsdomain.Result, error) {
	return g.SearchWithBudget(ctx, query, limit, 0)
}

// SearchWithBudget steers the search toward listings at or under
// maxPriceMinor (paise; 0 = no ceiling). The caller still filters — this
// just stops the model spending its result slots on things over budget.
func (g *Gemini) SearchWithBudget(ctx context.Context, query string, limit int, maxPriceMinor int64) ([]wsdomain.Result, error) {
	q := sanitize.Text(strings.TrimSpace(query), 200)
	if q == "" {
		return nil, errors.New("websearch: empty search query")
	}
	if limit <= 0 || limit > 10 {
		limit = 8
	}
	budget := ""
	if maxPriceMinor > 0 {
		budget = fmt.Sprintf(" under ₹%d", maxPriceMinor/100)
	}
	prompt := fmt.Sprintf(`Search Google for: %s price%s %s — quick commerce and online stores (Blinkit, Zepto, Swiggy Instamart, BigBasket, Amazon, Flipkart, JioMart, the brand's own store).
From the search results only, list up to %d distinct buyable options as a JSON array and nothing else.
Give real variety: different brands, pack sizes and price points — not the same product repeated across stores.%s
[{"name":"","variant":"","price_inr":0,"store":"","url":"","delivery":""}]
name: the product as listed. variant: size or pack. store: the shop's name.
url must be the exact result link you were given — prefer links to one product's own page over search, category, brand or article pages.
price_inr: the price the result shows in rupees, or null if it shows none.
delivery: the delivery time or date the result shows (e.g. "10 minutes", "Tomorrow"), or "" if it shows none.`,
		q, budget, g.cfg.Region, limit, budgetRule(maxPriceMinor))

	text, err := g.groundedText(ctx, prompt)
	if err != nil {
		return nil, err
	}
	return g.verify(ctx, parseListings(text), limit), nil
}

// groundedText runs one prompt with Google Search grounding and returns the
// model's text. Callers parse it and must still verify every link: only
// grounding redirects are trusted (see verify).
func (g *Gemini) groundedText(ctx context.Context, prompt string) (string, error) {
	return g.generate(ctx, prompt, true)
}

// GroundedText is groundedText for callers outside this package, such as the
// web discovery of paid x402 endpoints (providers/webdiscovery). What comes back
// is a model's text: untrusted, and every URL in it still has to be checked.
func (g *Gemini) GroundedText(ctx context.Context, prompt string) (string, error) {
	return g.groundedText(ctx, prompt)
}

// generate runs one prompt, with or without Google Search grounding.
func (g *Gemini) generate(ctx context.Context, prompt string, grounded bool) (string, error) {
	payload := map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": prompt}}}},
		"generationConfig": map[string]any{
			"thinkingConfig": map[string]any{"thinkingLevel": "low"},
		},
	}
	if grounded {
		payload["tools"] = []any{map[string]any{"google_search": map[string]any{}}}
	}
	reqBody, _ := json.Marshal(payload)
	endpoint := g.cfg.APIBase + "/models/" + url.PathEscape(g.cfg.Model) + ":generateContent"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("websearch: building gemini request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", g.cfg.APIKey)

	resp, err := g.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("websearch: gemini search failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return "", fmt.Errorf("websearch: reading gemini response: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return "", fmt.Errorf("websearch: Gemini rejected the API key (HTTP %d)", resp.StatusCode)
	case resp.StatusCode == http.StatusTooManyRequests:
		return "", errors.New("websearch: Gemini rate limit reached — try again shortly")
	case resp.StatusCode != http.StatusOK:
		return "", fmt.Errorf("websearch: Gemini returned HTTP %d", resp.StatusCode)
	}

	var body struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return "", fmt.Errorf("websearch: decoding gemini response: %w", err)
	}
	var text strings.Builder
	for _, c := range body.Candidates {
		for _, p := range c.Content.Parts {
			text.WriteString(p.Text)
		}
	}
	return text.String(), nil
}

func budgetRule(maxPriceMinor int64) string {
	if maxPriceMinor <= 0 {
		return ""
	}
	return fmt.Sprintf("\nOnly include options priced at or under ₹%d; skip anything above it.", maxPriceMinor/100)
}

func parseListings(text string) []geminiListing {
	m := jsonArray.FindString(text)
	if m == "" {
		return nil
	}
	var out []geminiListing
	if err := json.Unmarshal([]byte(m), &out); err != nil {
		return nil
	}
	return out
}

// verify keeps only listings whose link is a genuine grounding redirect,
// resolves each to the store's real URL (one request each, in parallel,
// never following further), and drops anything that isn't a public https
// page.
func (g *Gemini) verify(ctx context.Context, listings []geminiListing, limit int) []wsdomain.Result {
	type slot struct {
		res wsdomain.Result
		ok  bool
	}
	slots := make([]slot, len(listings))
	var wg sync.WaitGroup
	for i, l := range listings {
		if i >= limit*2 { // bound the fan-out even if the model over-delivers
			break
		}
		if !g.isGroundingRedirect(l.URL) || strings.TrimSpace(l.Name) == "" {
			continue
		}
		wg.Add(1)
		go func(i int, l geminiListing) {
			defer wg.Done()
			target, err := g.resolve(ctx, l.URL)
			if err != nil {
				return
			}
			if _, err := merchant.ValidatePublicHTTPSURL(target); err != nil {
				return
			}
			kind := pageKind(target)
			if kind == pageArticle {
				return // a blog post or news story isn't somewhere to buy
			}
			r := wsdomain.Result{
				Title:       sanitize.Text(l.Name, 160),
				Snippet:     sanitize.Text(l.Variant, 120),
				URL:         target,
				Store:       sanitize.Text(l.Store, 60),
				Delivery:    sanitize.Text(l.Delivery, 40),
				ProductPage: kind == pageProduct,
			}
			// The link decides the store, not the model's label: a price
			// tracker's page called "Flipkart" is not Flipkart.
			if r.Store == "" || !storeMatchesHost(r.Store, target) {
				r.Store = storeFromHost(target)
			}
			if l.PriceINR != nil && *l.PriceINR > 0 && *l.PriceINR < 10_000_000 {
				r.PriceMinorUnits = int64(math.Round(*l.PriceINR * 100))
				r.Currency = "INR"
			}
			r.ImageURL = g.productImage(ctx, target)
			slots[i] = slot{res: r, ok: true}
		}(i, l)
	}
	wg.Wait()

	// One product's own page first — it has the photo, price and item the
	// user can act on — then store search/category pages.
	out := make([]wsdomain.Result, 0, limit)
	seen := map[string]bool{}
	for _, productPass := range []bool{true, false} {
		for _, s := range slots {
			if !s.ok || seen[s.res.URL] || s.res.ProductPage != productPass {
				continue
			}
			seen[s.res.URL] = true
			out = append(out, s.res)
			if len(out) == limit {
				return out
			}
		}
	}
	return out
}

type pageClass int

const (
	pageProduct pageClass = iota
	pageListing           // a store's search, category, brand or collection page
	pageArticle           // blog posts, news, guides — not a place to buy
)

// pageKind classifies a store URL by its path segments, using the stores'
// own public URL shapes: Flipkart /q/ and /pr, Blinkit and Zepto /cn/ and
// /brand/, BigBasket /pc/ /pb/ /ps/, Amazon /s and /b, Shopify /collections/.
// Anything unrecognised counts as a product page.
func pageKind(raw string) pageClass {
	u, err := url.Parse(raw)
	if err != nil {
		return pageListing
	}
	p := strings.Trim(strings.ToLower(u.Path), "/")
	if p == "" {
		return pageListing
	}
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "blog", "blogs", "article", "articles", "news", "guide", "guides", "stories", "magazine":
			return pageArticle
		case "search", "q", "s", "b", "brand", "brands", "category", "categories", "cn", "c", "pc", "pb", "ps", "pr", "collections", "stores":
			return pageListing
		}
	}
	return pageProduct
}

// storeMatchesHost reports whether the store name the model gave plausibly
// belongs to the link's host ("Swiggy Instamart" → swiggy.com).
func storeMatchesHost(store, raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, word := range strings.Fields(strings.ToLower(store)) {
		w := strings.Trim(word, ".,()'")
		if len(w) >= 4 && strings.Contains(host, w) {
			return true
		}
	}
	return false
}

func (g *Gemini) isGroundingRedirect(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == g.redirectHost && strings.HasPrefix(u.Path, groundingRedirectPath)
}

// resolve reads the grounding redirect's Location without following it.
func (g *Gemini) resolve(ctx context.Context, redirect string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, redirect, nil)
	if err != nil {
		return "", err
	}
	resp, err := g.http.Do(req)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	if resp.StatusCode < 300 || resp.StatusCode > 399 {
		return "", fmt.Errorf("websearch: grounding redirect returned %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", errors.New("websearch: grounding redirect had no Location")
	}
	return loc, nil
}

var ogImageRE = regexp.MustCompile(`(?i)<meta[^>]+property=["']og:image(?::secure_url)?["'][^>]+content=["']([^"']+)["']`)
var ogImageAltRE = regexp.MustCompile(`(?i)<meta[^>]+content=["']([^"']+)["'][^>]+property=["']og:image(?::secure_url)?["']`)

// imageTagREs are the places a page publishes its main picture, most
// specific first: Open Graph, Twitter cards, schema.org microdata, the old
// image_src link, then a Product's JSON-LD "image".
var imageTagREs = []*regexp.Regexp{
	ogImageRE,
	ogImageAltRE,
	regexp.MustCompile(`(?i)<meta[^>]+name=["']twitter:image(?::src)?["'][^>]+content=["']([^"']+)["']`),
	regexp.MustCompile(`(?i)<meta[^>]+content=["']([^"']+)["'][^>]+name=["']twitter:image(?::src)?["']`),
	regexp.MustCompile(`(?i)<meta[^>]+itemprop=["']image["'][^>]+content=["']([^"']+)["']`),
	regexp.MustCompile(`(?i)<link[^>]+rel=["']image_src["'][^>]+href=["']([^"']+)["']`),
	regexp.MustCompile(`"image"\s*:\s*\[?\s*"(https?://[^"\s]+)"`),
}

// productImage returns the photo the store publishes for this page, or the
// store's own icon if it publishes none (several Indian storefronts render
// client-side or refuse non-browser requests — that's their call, and this
// falls back rather than working around it). Never a guessed URL.
func (g *Gemini) productImage(ctx context.Context, pageURL string) string {
	if img := g.fetchOGImage(ctx, pageURL); img != "" {
		return img
	}
	u, err := url.Parse(pageURL)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	return "https://www.google.com/s2/favicons?sz=64&domain=" + url.QueryEscape(u.Hostname())
}

func (g *Gemini) fetchOGImage(ctx context.Context, pageURL string) string {
	// Short: every listing already has an icon fallback, so a slow store
	// must not hold up the whole search.
	ctx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return ""
	}
	// Identifies itself honestly, like any link-preview fetcher. A store
	// that declines simply gets the icon fallback.
	req.Header.Set("User-Agent", "AlgebraLinkPreview/1.0 (+https://github.com/anshitraj/algebra)")
	req.Header.Set("Accept", "text/html")
	resp, err := g.http.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	// Preview tags live in <head>; product JSON-LD can sit a little later.
	// 512 KiB covers both and still bounds a slow page.
	head, err := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	if err != nil {
		return ""
	}
	var m [][]byte
	for _, re := range imageTagREs {
		if m = re.FindSubmatch(head); m != nil {
			break
		}
	}
	if m == nil {
		return ""
	}
	img := strings.TrimSpace(html.UnescapeString(string(m[1])))
	// Resolve a relative og:image against the page, then apply the same
	// public-https rule as every other URL we hand to a browser.
	base, err := url.Parse(pageURL)
	if err != nil {
		return ""
	}
	abs, err := base.Parse(img)
	if err != nil {
		return ""
	}
	if _, err := merchant.ValidatePublicHTTPSURL(abs.String()); err != nil {
		return ""
	}
	return abs.String()
}

func storeFromHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	h := strings.TrimPrefix(u.Hostname(), "www.")
	if i := strings.Index(h, "."); i > 0 {
		h = h[:i]
	}
	if h == "" {
		return ""
	}
	return strings.ToUpper(h[:1]) + h[1:]
}
