package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/project-algebra/algebra/connectors/sanitize"
	"github.com/project-algebra/algebra/internal/domain/merchant"
	wsdomain "github.com/project-algebra/algebra/internal/domain/websearch"
)

type communityPost struct {
	Title    string   `json:"title"`
	Summary  string   `json:"summary"`
	Code     string   `json:"code"`
	PriceINR *float64 `json:"price_inr"`
	Posted   string   `json:"posted"`
	URL      string   `json:"url"`
}

// couponCode is what a real code looks like; anything else the model put in
// the code field (a sentence, "N/A", a URL) is dropped.
var couponCode = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]{2,23}$`)

// SearchCommunity finds recent community posts about a product on the given
// sites, through the same grounded Google search as SearchWithBudget and
// under the same rules: only links Google actually returned are kept, each
// resolved to its real URL, which must be on one of the sites asked for.
// The result is tips, never prices Algebra stands behind.
func (g *Gemini) SearchCommunity(ctx context.Context, query string, sites []wsdomain.CommunitySite, limit int) ([]wsdomain.Tip, error) {
	q := sanitize.Text(strings.TrimSpace(query), 160)
	if q == "" || len(sites) == 0 {
		return nil, errors.New("websearch: community search needs a query and at least one site")
	}
	if limit <= 0 || limit > 8 {
		limit = 6
	}
	filters := make([]string, 0, len(sites))
	for _, s := range sites {
		filters = append(filters, "site:"+s.Filter)
	}
	prompt := fmt.Sprintf(`Search Google for: %s deal OR coupon OR offer (%s) — posts from the last two weeks.
Deals like these expire within days: skip anything older than two weeks, or marked expired or dead.
From the search results only, list up to %d posts about this product, newest first, as a JSON array and nothing else:
[{"title":"","summary":"","code":"","price_inr":0,"posted":"","url":""}]
title: the post's title. summary: one short line on what the deal is.
code: a coupon code only if the result text shows one, else "". price_inr: a price the result shows in rupees, else null.
posted: how old the post is if the result shows it (e.g. "3 days ago"), else "".
url must be the exact result link you were given.`, q, strings.Join(filters, " OR "), limit)

	text, err := g.groundedText(ctx, prompt)
	if err != nil {
		return nil, err
	}
	var posts []communityPost
	if m := jsonArray.FindString(text); m != "" {
		_ = json.Unmarshal([]byte(m), &posts)
	}
	return g.verifyTips(ctx, posts, sites, limit), nil
}

func (g *Gemini) verifyTips(ctx context.Context, posts []communityPost, sites []wsdomain.CommunitySite, limit int) []wsdomain.Tip {
	slots := make([]*wsdomain.Tip, len(posts))
	var wg sync.WaitGroup
	for i, p := range posts {
		if i >= limit*2 || !g.isGroundingRedirect(p.URL) || strings.TrimSpace(p.Title) == "" {
			continue
		}
		wg.Add(1)
		go func(i int, p communityPost) {
			defer wg.Done()
			target, err := g.resolve(ctx, p.URL)
			if err != nil {
				return
			}
			if _, err := merchant.ValidatePublicHTTPSURL(target); err != nil {
				return
			}
			source, ok := tipSource(target, sites)
			if !ok {
				return // a result from somewhere the user didn't switch on
			}
			tip := &wsdomain.Tip{
				Title:   sanitize.Text(p.Title, 160),
				Summary: sanitize.Text(p.Summary, 160),
				URL:     target,
				Source:  source,
				Posted:  sanitize.Text(p.Posted, 30),
			}
			if code := strings.ToUpper(strings.TrimSpace(p.Code)); couponCode.MatchString(code) {
				tip.Code = code
			}
			if p.PriceINR != nil && *p.PriceINR > 0 && *p.PriceINR < 10_000_000 {
				tip.PriceMinorUnits = int64(math.Round(*p.PriceINR * 100))
			}
			slots[i] = tip
		}(i, p)
	}
	wg.Wait()
	out := make([]wsdomain.Tip, 0, limit)
	seen := map[string]bool{}
	for _, t := range slots {
		if t == nil || seen[t.URL] {
			continue
		}
		seen[t.URL] = true
		out = append(out, *t)
		if len(out) == limit {
			break
		}
	}
	return out
}

// tipSource names where a tip was posted ("r/dealsforindia", "DesiDime"),
// and reports false when its link isn't on any of the allowed sites.
func tipSource(raw string, sites []wsdomain.CommunitySite) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	for _, s := range sites {
		if host != s.Host && !strings.HasSuffix(host, "."+s.Host) {
			continue
		}
		if s.Path != "" && !strings.HasPrefix(strings.ToLower(u.Path)+"/", strings.ToLower(s.Path)+"/") {
			continue // same site, but not the subreddit this person chose
		}
		if s.Host == "reddit.com" {
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			if len(parts) >= 2 && parts[0] == "r" {
				return "r/" + parts[1], true
			}
			return "Reddit", true
		}
		if s.Host == "desidime.com" {
			return "DesiDime", true
		}
		return s.Host, true
	}
	return "", false
}
