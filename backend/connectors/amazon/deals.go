package amazon

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/project-algebra/algebra/connectors/sanitize"
	"github.com/project-algebra/algebra/internal/domain/deal"
	"github.com/project-algebra/algebra/internal/domain/merchant"
)

// dealResources adds the OffersV2 deal fields to the catalog resources.
// OffersV2 has no coupon or promotion data — Amazon discontinued
// Offers.Listings.Promotions — so a discount here is always a price drop
// or a time-boxed deal, never a code to type in.
var dealResources = append(append([]string{}, catalogResources...), "offersV2.listings.dealDetails")

// dealDetails is OffersV2's time-boxed deal block (Lightning deals, Prime
// Day deals…). percentClaimed is documented as a string but the reference
// example shows a number, so both are accepted.
type dealDetails struct {
	AccessType                        string  `json:"accessType"` // ALL, PRIME_EARLY_ACCESS, PRIME_EXCLUSIVE
	Badge                             string  `json:"badge"`
	EarlyAccessDurationInMilliseconds int64   `json:"earlyAccessDurationInMilliseconds"`
	StartTime                         string  `json:"startTime"`
	EndTime                           string  `json:"endTime"`
	PercentClaimed                    flexInt `json:"percentClaimed"`
}

type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		*f = 0 // an unreadable percentage is dropped, not fatal
		return nil
	}
	*f = flexInt(n)
	return nil
}

// FindDeals searches Amazon for query and returns the results that are
// discounted right now — a saving against Amazon's reference price, a live
// deal, or both — in Amazon's own ranking. Fetched live every time:
// Associates policy limits how long a price may be shown without a refresh.
func (c *Connector) FindDeals(ctx context.Context, query string, limit int) ([]deal.Deal, error) {
	if limit <= 0 || limit > maxItems {
		limit = maxItems
	}
	// Amazon's catalog API has no "today's deals" listing — deals are only
	// found through a search.
	if strings.TrimSpace(query) == "" && c.configured() {
		return []deal.Deal{}, nil
	}
	items, err := c.searchItems(ctx, query, maxItems, dealResources)
	if err != nil {
		return nil, err
	}
	now := c.now()
	out := make([]deal.Deal, 0, limit)
	for _, it := range items {
		if len(out) >= limit {
			break
		}
		if d, ok := itemDeal(it, now); ok {
			out = append(out, d)
		}
	}
	return out, nil
}

// itemDeal maps the first priced listing of an item to a Deal, if it is
// discounted: a positive saving under the price it's measured against, or
// deal details whose window includes now.
func itemDeal(it catalogItem, now time.Time) (deal.Deal, bool) {
	p, ok := mapCatalogItem(it, 1)
	if !ok || !p.Available {
		return deal.Deal{}, false
	}
	for _, l := range it.OffersV2.Listings {
		if l.Price == nil || l.Price.Money == nil {
			continue
		}
		d := deal.Deal{
			Merchant:        Name,
			Kind:            deal.KindItemDeal,
			Source:          deal.SourceAmazonCreators,
			Title:           p.Name,
			URL:             p.URL,
			PriceMinorUnits: p.PriceMinorUnits,
			Currency:        p.Currency,
		}
		if p.Brand != "" {
			d.Description = p.Brand
		}
		if s := l.Price.Savings; s != nil && s.Money != nil && s.Money.Currency == p.Currency {
			if saved, ok := toMinorUnits(*s.Money); ok {
				d.SavingsMinorUnits = saved
				d.SavingsPercent = int(s.Percentage)
			}
		}
		if b := l.Price.SavingBasis; b != nil && b.Money != nil && b.Money.Currency == p.Currency {
			if was, ok := toMinorUnits(*b.Money); ok && was > p.PriceMinorUnits {
				d.WasMinorUnits = was
				d.BasisLabel = basisLabel(b.SavingBasisType, b.SavingBasisTypeLabel)
				if d.SavingsMinorUnits == 0 {
					d.SavingsMinorUnits = was - p.PriceMinorUnits
				}
			}
		}
		// A saving at or above the price it's taken from is a response this
		// code doesn't understand, not a free item.
		if d.WasMinorUnits > 0 && d.SavingsMinorUnits >= d.WasMinorUnits {
			d.SavingsMinorUnits, d.SavingsPercent = 0, 0
		}
		hasDeal := false
		if dd := l.DealDetails; dd != nil {
			d.StartsAt, d.EndsAt = parseDealTime(dd.StartTime), parseDealTime(dd.EndTime)
			if d.ActiveAt(now) {
				hasDeal = true
				d.Badge = dealBadge(dd.Badge)
				d.PercentClaimed = min(max(int(dd.PercentClaimed), 0), 100)
				d.PrimeOnly = primeOnly(*dd, d.StartsAt, now)
			} else {
				d.StartsAt, d.EndsAt = nil, nil
			}
		}
		if d.SavingsMinorUnits <= 0 && !hasDeal {
			return deal.Deal{}, false
		}
		return d, true
	}
	return deal.Deal{}, false
}

func primeOnly(dd dealDetails, startsAt *time.Time, now time.Time) bool {
	switch strings.ToUpper(dd.AccessType) {
	case "PRIME_EXCLUSIVE":
		return true
	case "PRIME_EARLY_ACCESS":
		if startsAt == nil || dd.EarlyAccessDurationInMilliseconds <= 0 {
			return true
		}
		return now.Before(startsAt.Add(time.Duration(dd.EarlyAccessDurationInMilliseconds) * time.Millisecond))
	}
	return false
}

// basisLabel prefers Amazon's own label ("M.R.P.:") and otherwise names the
// basis type in plain words.
func basisLabel(kind, label string) string {
	if l := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(label), ":")); l != "" {
		return sanitize.Text(l, 40)
	}
	switch strings.ToUpper(kind) {
	case "LIST_PRICE":
		return "List price"
	case "WAS_PRICE":
		return "Was"
	case "LOWEST_PRICE", "LOWEST_PRICE_STRIKETHROUGH":
		return "Lowest recent price"
	}
	return ""
}

// dealBadge drops countdown templates like "Ends in " whose number Amazon's
// page fills in client-side — shown bare, they'd read as broken.
func dealBadge(raw string) string {
	b := sanitize.Text(strings.TrimSpace(raw), 60)
	if b == "" || strings.HasSuffix(strings.ToLower(b), " in") || strings.EqualFold(b, "ends in") {
		return ""
	}
	return b
}

// parseDealTime reads the reference's UTC timestamps, which may omit
// seconds ("2025-02-21T05:35Z").
func parseDealTime(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04Z07:00"} {
		if t, err := time.Parse(layout, s); err == nil {
			t = t.UTC()
			return &t
		}
	}
	return nil
}

var _ merchant.DealFinder = (*Connector)(nil)
