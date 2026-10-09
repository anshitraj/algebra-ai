package app

import (
	"context"
	"errors"
	"sort"
	"strings"

	agentpkg "github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/deal"
	"github.com/project-algebra/algebra/internal/domain/merchant"
	"github.com/project-algebra/algebra/internal/domain/plugin"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

const (
	defaultDealLimit = 5
	maxDealLimit     = 10
	maxDealQueryLen  = 200
)

// DealQuery asks for deals on something the user is shopping for.
type DealQuery struct {
	// Query is what they're shopping for ("wireless mouse"). Empty returns
	// headline offers where a merchant has them.
	Query string
	// Merchants limits the search to these connector names; empty means
	// every merchant that publishes deals.
	Merchants []string
	// PriceMinorUnits is the price of the item being considered, if known:
	// bank offers with a higher minimum order are left out, and the rest
	// carry an estimated discount for it.
	PriceMinorUnits int64
	// Banks are the banks the user holds cards from ("HDFC", "SBI"), if
	// known: matching bank offers are flagged and listed first.
	Banks []string
	// Limit caps deals per merchant and bank offers overall (default 5,
	// max 10).
	Limit int
}

// DealNote says why a merchant (or bank offers, Merchant "") produced
// nothing, so an agent can explain it instead of implying there's no deal.
type DealNote struct {
	Merchant string `json:"merchant,omitempty"`
	Detail   string `json:"detail"`
}

type DealResults struct {
	Deals []deal.Deal `json:"deals"`
	Notes []DealNote  `json:"notes,omitempty"`
}

// SetBankOffers attaches the curated bank/card offer source. Nil (never
// called) means bank offers are off and FindDeals says so in a note.
func (s *DiscoveryService) SetBankOffers(src BankOfferSource) {
	s.bankOffers = src
}

// FindDeals collects what's discounted for q.Query: each merchant's own
// published deals (through its official API) and the curated bank/card
// offers that apply. Read-only and informational — nothing is applied to a
// cart, and no coupon code is ever produced: neither Amazon's nor
// Flipkart's API publishes codes.
func (s *DiscoveryService) FindDeals(ctx context.Context, agentID string, q DealQuery) (*DealResults, error) {
	if _, err := requirePermission(ctx, s.agents, agentID, agentpkg.PermShoppingRead); err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = defaultDealLimit
	}
	limit = min(limit, maxDealLimit)
	query := strings.TrimSpace(q.Query)
	if len(query) > maxDealQueryLen {
		query = query[:maxDealQueryLen]
	}
	want := map[string]bool{}
	for _, m := range q.Merchants {
		if m = strings.ToLower(strings.TrimSpace(m)); m != "" {
			want[m] = true
		}
	}
	wanted := func(name string) bool { return len(want) == 0 || want[name] }
	// Each deal source is a plugin the person can switch off.
	on, gated := s.dealPluginsFor(ctx, agentID)
	pluginOff := func(id string) bool { _, ok := on[id]; return gated && !ok }

	out := &DealResults{Deals: []deal.Deal{}}
	for _, c := range s.connectors.List() {
		if !wanted(c.Name()) {
			continue
		}
		if p, ok := plugin.ForDealMerchant(c.Name()); ok && pluginOff(p.ID) {
			if want[c.Name()] {
				out.Notes = append(out.Notes, DealNote{Merchant: c.Name(), Detail: "The " + p.Name + " plugin is off."})
			}
			continue
		}
		finder, ok := c.(merchant.DealFinder)
		if !ok {
			if want[c.Name()] {
				out.Notes = append(out.Notes, DealNote{Merchant: c.Name(), Detail: "This store doesn't publish deals through an API Algebra can read."})
			}
			continue
		}
		// A connector that isn't set up would only fail — and a failure
		// counts against the circuit breaker its searches share.
		if sr, ok := c.(merchant.StatusReporter); ok {
			if st := sr.Status(); !st.Ready {
				out.Notes = append(out.Notes, DealNote{Merchant: c.Name(), Detail: st.Detail})
				continue
			}
		}
		var found []deal.Deal
		err := s.callConnector(ctx, c, func(callCtx context.Context) error {
			var innerErr error
			found, innerErr = finder.FindDeals(callCtx, query, limit)
			return innerErr
		})
		if err != nil {
			out.Notes = append(out.Notes, DealNote{Merchant: c.Name(), Detail: dealErrorDetail(err)})
			continue
		}
		for _, d := range found {
			if s.urlAllowlist != nil {
				d.URL = s.urlAllowlist.SanitizeProductURL(d.URL)
			}
			out.Deals = append(out.Deals, d)
		}
	}

	if pluginOff(plugin.BankOffers) {
		out.Notes = append(out.Notes, DealNote{Detail: "The Bank & card offers plugin is off."})
		return out, nil
	}
	bank, note := s.bankOfferDeals(ctx, wanted, q, limit)
	out.Deals = append(out.Deals, bank...)
	if note != "" {
		out.Notes = append(out.Notes, DealNote{Detail: note})
	}
	return out, nil
}

// bankOfferDeals returns the live bank offers for the wanted merchants,
// cards the user holds first, then by estimated saving.
func (s *DiscoveryService) bankOfferDeals(ctx context.Context, wanted func(string) bool, q DealQuery, limit int) ([]deal.Deal, string) {
	if s.bankOffers == nil {
		return nil, "Bank and card offers aren't set up on this server."
	}
	offers, err := s.bankOffers.Offers(ctx)
	if err != nil {
		return nil, "Couldn't read bank and card offers right now."
	}
	now := s.now()
	var out []deal.Deal
	for _, o := range offers {
		if !wanted(o.Merchant) {
			continue
		}
		if q.PriceMinorUnits > 0 && q.PriceMinorUnits < o.MinOrderMinorUnits {
			continue
		}
		d := o.Deal(q.PriceMinorUnits)
		if !d.ActiveAt(now) {
			continue
		}
		if s.urlAllowlist != nil {
			d.URL = s.urlAllowlist.SanitizeProductURL(d.URL)
		}
		for _, b := range q.Banks {
			if deal.SameBank(b, o.Bank) {
				d.MatchesUserCard = true
				break
			}
		}
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.MatchesUserCard != b.MatchesUserCard {
			return a.MatchesUserCard
		}
		if a.EstimatedDiscountMinorUnits != b.EstimatedDiscountMinorUnits {
			return a.EstimatedDiscountMinorUnits > b.EstimatedDiscountMinorUnits
		}
		return a.DiscountPercent > b.DiscountPercent
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, ""
}

// dealErrorDetail turns a connector error into something safe to show: a
// not-configured connector's own explanation (which never contains a
// credential), and a generic line for anything else.
func dealErrorDetail(err error) string {
	if errors.Is(err, shared.ErrNotImplemented) {
		if _, detail, ok := strings.Cut(err.Error(), shared.ErrNotImplemented.Error()+": "); ok && detail != "" {
			return detail
		}
		return "Deals aren't available for this store on this server."
	}
	return "Couldn't read this store's deals right now."
}
