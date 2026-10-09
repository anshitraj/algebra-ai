package flipkart

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/project-algebra/algebra/connectors/sanitize"
	"github.com/project-algebra/algebra/internal/domain/deal"
	"github.com/project-algebra/algebra/internal/domain/merchant"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// Offers come from the Affiliate API's offers endpoints
// (https://affiliate.flipkart.com/api-docs/af_offer_ref.html):
//
//	GET {OffersBaseURL}/all/json   → {"allOffersList": [...]}
//	GET {OffersBaseURL}/dotd/json  → {"dotdList": [...]}   (Deals of the Day)
//
// Both are the same for every caller, so the pair is fetched at most once
// per offerFeedTTL and matched against queries locally.
const (
	DefaultOffersBaseURL = "https://affiliate-api.flipkart.net/affiliate/offers/v1"
	OffersDocsURL        = "https://affiliate.flipkart.com/api-docs/af_offer_ref.html"

	offerFeedTTL   = 15 * time.Minute
	offerFeedStale = 2 * time.Hour
	maxDeals       = 10
)

// epochMillis accepts the feed's Unix-millisecond timestamps as a JSON
// number or a numeric string.
type epochMillis int64

func (e *epochMillis) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*e = 0
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("flipkart: bad timestamp %s", b)
	}
	*e = epochMillis(n)
	return nil
}

func (e epochMillis) time() *time.Time {
	if e <= 0 {
		return nil
	}
	t := time.UnixMilli(int64(e)).UTC()
	return &t
}

type feedImage struct {
	URL            string `json:"url"`
	ResolutionType string `json:"resolutionType"`
}

type feedOffer struct {
	Title        string      `json:"title"`
	Description  string      `json:"description"`
	URL          string      `json:"url"`
	Category     string      `json:"category"`
	StartTime    epochMillis `json:"startTime"`
	EndTime      epochMillis `json:"endTime"`
	Availability string      `json:"availability"`
	ImageURLs    []feedImage `json:"imageUrls"`

	dotd bool
}

type allOffersResponse struct {
	AllOffersList []feedOffer `json:"allOffersList"`
}

type dotdResponse struct {
	DotdList []feedOffer `json:"dotdList"`
}

// FindDeals returns Flipkart's live published offers that match query —
// Deals of the Day first on a tie. An empty query returns the current
// headline offers. Nothing matching means an empty list, never unrelated
// offers.
func (c *Connector) FindDeals(ctx context.Context, query string, limit int) ([]deal.Deal, error) {
	if !c.configured() {
		return nil, fmt.Errorf("%w: %s", shared.ErrNotImplemented, c.configErr)
	}
	if limit <= 0 || limit > maxDeals {
		limit = maxDeals
	}
	feed, err := c.offerFeed(ctx)
	if err != nil {
		return nil, err
	}
	now := c.now()

	type scored struct {
		o     feedOffer
		score int
	}
	want := dealWords(query)
	var hits []scored
	for _, o := range feed {
		if a := strings.ToUpper(strings.TrimSpace(o.Availability)); a != "" && a != "LIVE" {
			continue
		}
		if o.Title == "" || !(deal.Deal{StartsAt: o.StartTime.time(), EndsAt: o.EndTime.time()}).ActiveAt(now) {
			continue
		}
		score := 1
		if len(want) > 0 {
			score = overlap(want, dealWords(o.Title+" "+o.Description+" "+o.Category))
			if score == 0 {
				continue
			}
		}
		hits = append(hits, scored{o, score})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].o.dotd && !hits[j].o.dotd
	})

	out := make([]deal.Deal, 0, min(limit, len(hits)))
	for _, h := range hits {
		if len(out) >= limit {
			break
		}
		out = append(out, h.o.toDeal())
	}
	return out, nil
}

func (o feedOffer) toDeal() deal.Deal {
	d := deal.Deal{
		Merchant:    Name,
		Kind:        deal.KindStoreOffer,
		Source:      deal.SourceFlipkartAffiliate,
		Title:       sanitize.Text(o.Title, 160),
		Description: sanitize.Text(o.Description, 300),
		URL:         o.URL,
		ImageURL:    pickImage(o.ImageURLs),
		Category:    sanitize.Text(o.Category, 80),
		StartsAt:    o.StartTime.time(),
		EndsAt:      o.EndTime.time(),
	}
	if o.dotd {
		d.Badge = "Deal of the Day"
	}
	return d
}

// pickImage prefers the 400px rendition, then the default, then any — and
// only an https link to a public host.
func pickImage(imgs []feedImage) string {
	rank := map[string]int{"mid": 0, "default": 1, "high": 2, "low": 3}
	best, bestRank := "", 99
	for _, img := range imgs {
		if _, err := merchant.ValidatePublicHTTPSURL(img.URL); err != nil {
			continue
		}
		r, ok := rank[strings.ToLower(img.ResolutionType)]
		if !ok {
			r = 4
		}
		if r < bestRank {
			best, bestRank = img.URL, r
		}
	}
	return best
}

// offerFeed returns the cached all-offers + DOTD feed, refreshing it when
// older than offerFeedTTL. If a refresh fails, a feed up to offerFeedStale
// old is served rather than nothing.
func (c *Connector) offerFeed(ctx context.Context) ([]feedOffer, error) {
	c.offersMu.Lock()
	defer c.offersMu.Unlock()
	now := c.now()
	if c.offers != nil && now.Sub(c.offersFetched) < offerFeedTTL {
		return c.offers, nil
	}

	var all allOffersResponse
	var dotd dotdResponse
	errAll := c.getOffers(ctx, "/all/json", &all)
	errDotd := c.getOffers(ctx, "/dotd/json", &dotd)
	if errAll != nil && errDotd != nil {
		if c.offers != nil && now.Sub(c.offersFetched) < offerFeedStale {
			return c.offers, nil
		}
		return nil, errAll
	}
	feed := make([]feedOffer, 0, len(all.AllOffersList)+len(dotd.DotdList))
	for _, o := range dotd.DotdList {
		o.dotd = true
		feed = append(feed, o)
	}
	feed = append(feed, all.AllOffersList...)
	c.offers, c.offersFetched = feed, now
	return feed, nil
}

func (c *Connector) getOffers(ctx context.Context, path string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.OffersBaseURL+path, nil)
	if err != nil {
		return fmt.Errorf("flipkart: building offers request: %w", err)
	}
	req.Header.Set("Fk-Affiliate-Id", c.cfg.AffiliateID)
	req.Header.Set("Fk-Affiliate-Token", c.cfg.AffiliateToken)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("flipkart: offers request failed: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("flipkart: affiliate API rejected the credentials (HTTP %d)", resp.StatusCode)
	case resp.StatusCode == http.StatusTooManyRequests:
		return errors.New("flipkart: affiliate API rate limit reached")
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("flipkart: offers API returned HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(into); err != nil {
		return fmt.Errorf("flipkart: decoding offers response: %w", err)
	}
	return nil
}

// dealFiller is promo boilerplate that appears in almost every offer title
// ("Flat 50% Off", "Min 40% off", "Best Deals") and so says nothing about
// WHAT is on offer — matching on it would pair "flat feet" with a bedsheet
// sale.
var dealFiller = map[string]bool{
	"flat": true, "off": true, "upto": true, "up": true, "min": true, "extra": true, "sale": true,
	"deal": true, "offer": true, "best": true, "new": true, "top": true, "great": true, "price": true,
	"discount": true, "buy": true, "get": true, "free": true, "now": true, "under": true, "only": true,
	"from": true, "just": true, "starting": true, "for": true, "with": true, "the": true, "and": true,
	"all": true, "day": true, "today": true, "shop": true, "big": true, "low": true, "save": true,
}

// dealWords splits text into lowercase, singular-ish content words.
func dealWords(s string) map[string]bool {
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) })
	out := make(map[string]bool, len(words))
	for _, w := range words {
		w = singular(w)
		if len(w) < 3 || dealFiller[w] {
			continue
		}
		out[w] = true
	}
	return out
}

func singular(w string) string {
	switch {
	case strings.HasSuffix(w, "ies") && len(w) > 4:
		return w[:len(w)-3] + "y"
	case strings.HasSuffix(w, "sses") || strings.HasSuffix(w, "xes") || strings.HasSuffix(w, "shes") || strings.HasSuffix(w, "ches"):
		return w[:len(w)-2]
	case strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") && len(w) > 3:
		return w[:len(w)-1]
	}
	return w
}

func overlap(a, b map[string]bool) int {
	n := 0
	for w := range a {
		if b[w] {
			n++
		}
	}
	return n
}

var _ merchant.DealFinder = (*Connector)(nil)
