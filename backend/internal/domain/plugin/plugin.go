// Package plugin is the catalog of sources a user can switch on or off for
// their agent: where prices, deals and community tips come from. Every
// plugin is read-only — it can suggest what to buy and where it's cheaper,
// but it can never place an order, approve a purchase, or see who the user
// is. Checkout stays with vetted store connectors.
package plugin

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/project-algebra/algebra/internal/domain/websearch"
)

// Purpose groups plugins by what they're for.
type Purpose string

const (
	PurposePrices    Purpose = "prices"
	PurposeDeals     Purpose = "deals"
	PurposeCommunity Purpose = "community"
)

// Trust says who stands behind what a plugin returns.
type Trust string

const (
	// TrustStore: the store's own API — its deals apply as shown.
	TrustStore Trust = "store"
	// TrustCurated: Algebra-run sources (live search, curated bank offers).
	TrustCurated Trust = "curated"
	// TrustCommunity: what people post. May be expired or wrong; never a price.
	TrustCommunity Trust = "community"
)

// Plugin IDs referenced by code.
const (
	WebPrices      = "web_prices"
	BankOffers     = "bank_offers"
	AmazonDeals    = "amazon_deals"
	FlipkartOffers = "flipkart_offers"
	RedditDeals    = "reddit_deals"
	DesiDimeDeals  = "desidime_deals"
)

type Plugin struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Purpose Purpose `json:"purpose"`
	Trust   Trust   `json:"trust"`
	Summary string  `json:"summary"`
	// Sees is what the plugin is sent — always the product search, never
	// the user's identity, address or payment details.
	Sees string `json:"sees"`
	// Icon is a domain whose icon represents the plugin; empty for a
	// source that isn't one site (bank offers span many banks).
	Icon      string `json:"icon,omitempty"`
	DefaultOn bool   `json:"default_on"`
	// Core plugins can't be switched off: the agent can't shop without them.
	Core bool `json:"core"`

	// DealMerchant is the store connector whose published deals this
	// plugin gates ("amazon").
	DealMerchant string `json:"-"`
	// Sites are where a community plugin reads tips from.
	Sites []websearch.CommunitySite `json:"-"`
}

const seesSearch = "Only the product you're searching for. Never your name, address, cards or order history."

// Catalog is every plugin, in display order.
var Catalog = []Plugin{
	{
		ID: WebPrices, Name: "Live web prices", Purpose: PurposePrices, Trust: TrustCurated, Icon: "google.com",
		Summary: "Searches Google for real listings, prices and delivery times across Blinkit, Zepto, Instamart, Amazon, Flipkart and more.",
		Sees:    seesSearch, DefaultOn: true, Core: true,
	},
	{
		ID: BankOffers, Name: "Bank & card offers", Purpose: PurposeDeals, Trust: TrustCurated,
		Summary: "Card offers from banks like HDFC, ICICI and SBI that apply at checkout, with the saving worked out for your order.",
		Sees:    "The product, its price and which banks' cards you hold (if you've told the agent).", DefaultOn: true,
	},
	{
		ID: AmazonDeals, Name: "Amazon deals", Purpose: PurposeDeals, Trust: TrustStore, Icon: "amazon.in",
		Summary: "Price drops, Deal of the Day and Lightning deals, straight from Amazon's own API.",
		Sees:    seesSearch, DefaultOn: true, DealMerchant: "amazon",
	},
	{
		ID: FlipkartOffers, Name: "Flipkart offers", Purpose: PurposeDeals, Trust: TrustStore, Icon: "flipkart.com",
		Summary: "Live offers from Flipkart's affiliate API.",
		Sees:    seesSearch, DefaultOn: true, DealMerchant: "flipkart",
	},
	{
		ID: RedditDeals, Name: "Reddit deal threads", Purpose: PurposeCommunity, Trust: TrustCommunity, Icon: "reddit.com",
		Summary: "Deals and coupon codes people post on Reddit — r/dealsforindia and r/IndianShoppers, or two subreddits you choose. Recent posts only.",
		Sees:    seesSearch,
		// The subreddits are each person's choice — see SitesFor.
	},
	{
		ID: DesiDimeDeals, Name: "DesiDime", Purpose: PurposeCommunity, Trust: TrustCommunity, Icon: "desidime.com",
		Summary: "Community-posted deals and coupon codes from DesiDime, India's deal-sharing forum, found through Google.",
		Sees:    seesSearch,
		Sites:   []websearch.CommunitySite{{Filter: "desidime.com", Host: "desidime.com"}},
	},
}

// Get finds a plugin by ID.
func Get(id string) (Plugin, bool) {
	for _, p := range Catalog {
		if p.ID == id {
			return p, true
		}
	}
	return Plugin{}, false
}

// ForDealMerchant finds the plugin gating a store connector's deals.
func ForDealMerchant(merchant string) (Plugin, bool) {
	for _, p := range Catalog {
		if p.DealMerchant != "" && p.DealMerchant == merchant {
			return p, true
		}
	}
	return Plugin{}, false
}

// Config is a plugin's per-user settings.
type Config struct {
	// Subreddits the Reddit plugin reads, without "r/". At most
	// MaxSubreddits; empty means the defaults.
	Subreddits []string `json:"subreddits,omitempty"`
}

// MaxSubreddits is how many subreddits one person's Reddit plugin reads.
const MaxSubreddits = 2

// DefaultSubreddits are what the Reddit plugin reads until someone chooses.
var DefaultSubreddits = []string{"dealsforindia", "IndianShoppers"}

var subredditName = regexp.MustCompile(`^[A-Za-z0-9_]{2,21}$`)

// NormalizeSubreddits trims "r/" and slashes, drops invalid or repeated
// names, and reports an error for a name Reddit could never have.
func NormalizeSubreddits(in []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		s = strings.TrimPrefix(strings.TrimPrefix(s, "/"), "r/")
		s = strings.Trim(s, "/")
		if s == "" {
			continue
		}
		if !subredditName.MatchString(s) {
			return nil, fmt.Errorf("%q isn't a subreddit name", s)
		}
		if seen[strings.ToLower(s)] {
			continue
		}
		seen[strings.ToLower(s)] = true
		out = append(out, s)
	}
	if len(out) > MaxSubreddits {
		return nil, fmt.Errorf("pick at most %d subreddits", MaxSubreddits)
	}
	return out, nil
}

// SitesFor is where a community plugin reads from for this person: the
// Reddit plugin's chosen subreddits, or the plugin's fixed sites.
func SitesFor(p Plugin, c Config) []websearch.CommunitySite {
	if p.ID != RedditDeals {
		return p.Sites
	}
	subs := c.Subreddits
	if len(subs) == 0 {
		subs = DefaultSubreddits
	}
	out := make([]websearch.CommunitySite, 0, len(subs))
	for _, s := range subs {
		out = append(out, websearch.CommunitySite{Filter: "reddit.com/r/" + s, Host: "reddit.com", Path: "/r/" + s})
	}
	return out
}

// Resolve applies a user's saved choices over the defaults. Core plugins
// are always on, whatever was saved.
func Resolve(choices map[string]bool) map[string]bool {
	out := make(map[string]bool, len(Catalog))
	for _, p := range Catalog {
		on, set := choices[p.ID]
		out[p.ID] = p.Core || (set && on) || (!set && p.DefaultOn)
	}
	return out
}
