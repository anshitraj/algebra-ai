package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/domain/account"
	agentpkg "github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/audit"
	"github.com/project-algebra/algebra/internal/domain/intent"
	"github.com/project-algebra/algebra/internal/domain/merchant"
	"github.com/project-algebra/algebra/internal/domain/quote"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/domain/websearch"
	"github.com/project-algebra/algebra/internal/platform/resilience"
)

type DiscoveryService struct {
	intents    IntentStore
	agents     AgentStore
	quotes     QuoteStore
	connectors *ConnectorRegistry
	audit      audit.Logger
	now        func() time.Time
	quoteTTL   time.Duration

	// breakers/connectorTimeout are optional (mandate §38: "concurrent
	// merchant discovery... with per-provider timeouts and circuit
	// breakers"). Zero values (SetResilience never called) mean every
	// connector call runs with whatever timeout the caller's ctx already
	// carries and no breaker ever opens — correct, just without the
	// fail-fast-on-a-known-bad-provider optimization.
	breakers         *resilience.Registry
	connectorTimeout time.Duration

	// urlAllowlist filters merchant-supplied product URLs — see
	// SetURLAllowlist.
	urlAllowlist *merchant.AllowedDomains

	// webSearch is the optional general web-search fallback — see
	// SetWebSearcher and SearchWeb. Nil means the capability is off.
	webSearch WebSearcher
	// searchCache is optional — see SetSearchCache.
	searchCache    SearchCache
	searchCacheTTL time.Duration

	// bankOffers is optional — see SetBankOffers (deals.go).
	bankOffers BankOfferSource

	// modes/listingObserver are optional — see SetAccountModes and
	// SetListingObserver.
	modes           AccountModes
	listingObserver func([]websearch.Result)

	// community/plugins are optional — see SetCommunitySearcher and
	// SetPlugins (community.go).
	community CommunitySearcher
	plugins   *PluginService
}

// AccountModes reports whether a user's account is live or demo
// (AccountService implements it).
type AccountModes interface {
	UserMode(ctx context.Context, userID string) (account.Mode, error)
}

// SetAccountModes turns on routing by account mode: demo accounts buy only
// through the demo checkout, live accounts never reach it (or the mock
// test store). Nil (never called) leaves every connector eligible, as in
// tests.
func (s *DiscoveryService) SetAccountModes(m AccountModes) { s.modes = m }

// SetListingObserver is told about every web-search result set, so the demo
// checkout can sell exactly the listing the user was shown.
func (s *DiscoveryService) SetListingObserver(fn func([]websearch.Result)) { s.listingObserver = fn }

// demoCheckout is the connector name only demo accounts may use
// (connectors/democheckout.Name, repeated here so app doesn't import a
// connector package).
const demoCheckout = "demo_checkout"

// modeFor returns the user's account mode, or "" when routing by mode is
// off. A lookup failure counts as live — the mode that can't reach a
// simulated store.
func (s *DiscoveryService) modeFor(ctx context.Context, userID string) account.Mode {
	if s.modes == nil {
		return ""
	}
	m, err := s.modes.UserMode(ctx, userID)
	if err != nil || m != account.ModeDemo {
		return account.ModeLive
	}
	return account.ModeDemo
}

// allowedFor reports whether a connector may serve an account in mode.
// Demo accounts see every real store (catalog results, handoff links) and
// buy only through the demo checkout (candidateMerchants); live accounts
// never reach the demo checkout or the mock test store.
func allowedFor(mode account.Mode, name string) bool {
	switch mode {
	case account.ModeDemo:
		return name != "mock"
	case account.ModeLive:
		return name != demoCheckout && name != "mock"
	default:
		return name != demoCheckout
	}
}

func NewDiscoveryService(intents IntentStore, agents AgentStore, quotes QuoteStore, connectors *ConnectorRegistry, auditLogger audit.Logger, quoteTTL time.Duration) *DiscoveryService {
	return &DiscoveryService{intents: intents, agents: agents, quotes: quotes, connectors: connectors, audit: auditLogger, now: time.Now, quoteTTL: quoteTTL}
}

// SetResilience attaches a per-connector circuit-breaker registry and a
// per-connector call timeout. Called once during wiring.
func (s *DiscoveryService) SetResilience(breakers *resilience.Registry, connectorTimeout time.Duration) {
	s.breakers = breakers
	s.connectorTimeout = connectorTimeout
}

// SetURLAllowlist attaches the domain allowlist every merchant-supplied
// product URL is checked against before Algebra passes it on to an agent
// (mandate §47 "merchant URL manipulation", §49 SSRF protection). Nil means
// no filtering — correct only for a build with no real merchant connectors.
func (s *DiscoveryService) SetURLAllowlist(allowed *merchant.AllowedDomains) {
	s.urlAllowlist = allowed
}

// SetWebSearcher attaches the optional general web-search fallback. Called
// once during wiring, only when GOOGLE_SEARCH_API_KEY and
// GOOGLE_SEARCH_ENGINE_ID are configured. Nil (never called) means
// SearchWeb always reports the capability as off.
// SetSearchCache caches web-search results for ttl. Prices move, so this is
// minutes, not hours; the UI labels them "as seen on the web" either way.
func (s *DiscoveryService) SetSearchCache(c SearchCache, ttl time.Duration) {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	s.searchCache, s.searchCacheTTL = c, ttl
}

func (s *DiscoveryService) SetWebSearcher(ws WebSearcher) {
	s.webSearch = ws
}

// sanitizeProductURLs blanks any product URL that isn't an https link to an
// allowlisted merchant domain. A merchant (or a compromised connector) must
// not be able to hand an agent a link pointing at an internal address.
func (s *DiscoveryService) sanitizeProductURLs(products []merchant.Product) []merchant.Product {
	if s.urlAllowlist == nil {
		return products
	}
	for i := range products {
		products[i].URL = s.urlAllowlist.SanitizeProductURL(products[i].URL)
	}
	return products
}

// callConnector runs fn against connector under this service's configured
// timeout and circuit breaker (either or both may be unset). It is the one
// choke point every discovery-side call to a connector goes through, so the
// mandate's "per-provider timeouts and circuit breakers" is enforced
// uniformly rather than per call site.
func (s *DiscoveryService) callConnector(ctx context.Context, connector merchant.Connector, fn func(context.Context) error) error {
	var breaker *resilience.CircuitBreaker
	if s.breakers != nil {
		breaker = s.breakers.Get(connector.Name())
		if !breaker.Allow() {
			return fmt.Errorf("%s: circuit breaker open, skipping call", connector.Name())
		}
	}

	callCtx := ctx
	if s.connectorTimeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, s.connectorTimeout)
		defer cancel()
	}

	err := fn(callCtx)
	if breaker != nil {
		if err != nil {
			breaker.RecordFailure(s.now())
		} else {
			breaker.RecordSuccess()
		}
	}
	return err
}

// candidateMerchants returns the connectors eligible for this intent's
// constraints: excluded merchants are dropped; if preferred merchants are
// given, only those are considered. Discovery ends in a checkout quote, so
// only connectors that can both search and hold a cart qualify. A
// search-only catalog integration (the Amazon and Flipkart affiliate APIs)
// would otherwise fail at CreateCart on every intent and trip its circuit
// breaker — taking its perfectly good search results down with it.
func (s *DiscoveryService) candidateMerchants(pi *intent.PurchaseIntent, mode account.Mode) []merchant.Connector {
	// A demo account's purchases all go through the demo checkout, whatever
	// stores its profile prefers.
	if mode == account.ModeDemo {
		c, err := s.connectors.Get(demoCheckout)
		if err != nil || !c.Capabilities().Cart {
			return nil
		}
		return []merchant.Connector{c}
	}
	excluded := map[string]bool{}
	for _, m := range pi.Constraints.ExcludedMerchants {
		excluded[m] = true
	}
	preferred := map[string]bool{}
	for _, m := range pi.Constraints.PreferredMerchants {
		preferred[m] = true
	}

	var out []merchant.Connector
	for _, c := range s.connectors.List() {
		if excluded[c.Name()] || !allowedFor(mode, c.Name()) {
			continue
		}
		if len(preferred) > 0 && !preferred[c.Name()] {
			continue
		}
		if caps := c.Capabilities(); !caps.Search || !caps.Cart {
			continue
		}
		out = append(out, c)
	}
	return out
}

// Discover fans out to every eligible connector, matches each requested
// item to the best-confidence available product, builds a cart, and
// retrieves a checkout quote. It transitions the intent DRAFT -> DISCOVERING
// -> QUOTED (at least one usable quote) or -> FAILED (none).
//
// A single connector's failure never fails the whole operation — Algebra
// degrades to "fewer options" rather than an opaque overall error, per the
// per-provider circuit-breaker principle in mandate §38.
func (s *DiscoveryService) Discover(ctx context.Context, agentID, intentID string) ([]*quote.CheckoutQuote, error) {
	_, pi, err := requireOwnedIntent(ctx, s.agents, s.intents, agentID, intentID, agentpkg.PermShoppingRead)
	if err != nil {
		return nil, err
	}

	if err := transitionIntent(ctx, s.intents, s.audit, s.now, pi, intent.StateDiscovering, "DiscoveryStarted", "fanning out to merchant connectors"); err != nil {
		return nil, err
	}

	var quotes []*quote.CheckoutQuote
	for _, connector := range s.candidateMerchants(pi, s.modeFor(ctx, pi.UserID)) {
		var q *quote.CheckoutQuote
		callErr := s.callConnector(ctx, connector, func(callCtx context.Context) error {
			var innerErr error
			q, innerErr = s.discoverOne(callCtx, pi, connector)
			return innerErr
		})
		if callErr != nil {
			evt := audit.NewEvent("ConnectorFailed", s.now())
			evt.IntentID = pi.ID
			evt.Merchant = connector.Name()
			evt.Result = callErr.Error()
			_ = s.audit.Record(ctx, evt)
			continue
		}
		if err := s.quotes.Save(ctx, pi.ID, q); err != nil {
			continue
		}
		quotes = append(quotes, q)
	}

	if len(quotes) == 0 {
		_ = transitionIntent(ctx, s.intents, s.audit, s.now, pi, intent.StateFailed, "DiscoveryFailed", "no connector returned a usable quote")
		return nil, nil
	}

	if err := transitionIntent(ctx, s.intents, s.audit, s.now, pi, intent.StateQuoted, "QuoteCreated", "quotes available"); err != nil {
		return nil, err
	}
	return quotes, nil
}

// MerchantSearchResult pairs a matched product with the merchant it came
// from, for commerce.search_products / commerce.compare_products — an
// agent exploring options before committing to a PurchaseIntent.
type MerchantSearchResult struct {
	Merchant string             `json:"merchant"`
	Products []merchant.Product `json:"products,omitempty"`
	// HandoffURL is set instead of Products for a merchant Algebra cannot
	// search through an official integration (e.g. Blinkit): a link to that
	// merchant's own search page, for the user to open and finish there.
	HandoffURL string `json:"handoff_url,omitempty"`
}

// SearchProducts fans out a free-text query to every searchable connector,
// independent of any intent. Used for browsing/comparison, not execution —
// nothing here touches policy, approval, or payment. Merchants that can't
// search but offer a handoff link are included with that link, so an agent
// can still present them as an option without pretending to have results.
func (s *DiscoveryService) SearchProducts(ctx context.Context, agentID, query string, limit int) ([]MerchantSearchResult, error) {
	ag, err := requirePermission(ctx, s.agents, agentID, agentpkg.PermShoppingRead)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 5
	}
	mode := s.modeFor(ctx, ag.UserID)
	var out []MerchantSearchResult
	for _, c := range s.connectors.List() {
		// The demo checkout sells what web_search found; listing it here too
		// would only run the same web search twice.
		if c.Name() == demoCheckout || !allowedFor(mode, c.Name()) {
			continue
		}
		if !c.Capabilities().Search {
			if link := s.handoffLink(c, query); link != "" {
				out = append(out, MerchantSearchResult{Merchant: c.Name(), HandoffURL: link})
			}
			continue
		}
		var products []merchant.Product
		err := s.callConnector(ctx, c, func(callCtx context.Context) error {
			var innerErr error
			products, innerErr = c.SearchProducts(callCtx, query, limit)
			return innerErr
		})
		if err != nil || len(products) == 0 {
			continue
		}
		out = append(out, MerchantSearchResult{Merchant: c.Name(), Products: s.sanitizeProductURLs(products)})
	}
	return out, nil
}

// SearchWeb shows what's actually out there beyond the connected merchants:
// live web listings (store, product, the price the result showed, a link)
// for an agent to hand to the user — never a quote, never a cart, never an
// order; same non-custodial shape as a merchant's HandoffURL but not scoped
// to one storefront. Off (ErrNotImplemented) unless GEMINI_API_KEY (or
// Custom Search credentials) is configured.
func (s *DiscoveryService) SearchWeb(ctx context.Context, agentID, query string, limit int) ([]websearch.Result, error) {
	return s.SearchWebWithin(ctx, agentID, query, limit, 0)
}

// SearchWebWithin is SearchWeb with a price ceiling (minor units; 0 = none).
// Anything the search shows above the ceiling is dropped — a user who said
// "under ₹500" should never be shown an ₹896 box. Listings with no visible
// price can't be checked, so they're kept but ranked after priced ones.
func (s *DiscoveryService) SearchWebWithin(ctx context.Context, agentID, query string, limit int, maxPriceMinor int64) ([]websearch.Result, error) {
	if _, err := requirePermission(ctx, s.agents, agentID, agentpkg.PermShoppingRead); err != nil {
		return nil, err
	}
	results, err := s.webListings(ctx, query, limit, maxPriceMinor)
	if err == nil && s.listingObserver != nil && len(results) > 0 {
		s.listingObserver(results)
	}
	return results, err
}

// WebListings is the cached web product search without an agent — for the
// demo checkout connector, which prices real listings. Same cache as the
// agent's web_search, so buying what the agent just showed doesn't search
// again.
func (s *DiscoveryService) WebListings(ctx context.Context, query string, limit int) ([]websearch.Result, error) {
	return s.webListings(ctx, query, limit, 0)
}

func (s *DiscoveryService) webListings(ctx context.Context, query string, limit int, maxPriceMinor int64) ([]websearch.Result, error) {
	if s.webSearch == nil {
		return nil, fmt.Errorf("%w: web search is not configured (set GEMINI_API_KEY, or GOOGLE_SEARCH_API_KEY + GOOGLE_SEARCH_ENGINE_ID)", shared.ErrNotImplemented)
	}
	if limit <= 0 {
		limit = 5
	}
	// Normalized so "Coke Zero", "coke zero" and "  Coke  Zero " share one
	// cached answer.
	// v3: bump whenever websearch.Result's shape changes, so a deploy never
	// serves rows cached in the old shape (v2 added image_url, v3 warnings).
	cacheKey := fmt.Sprintf("algebra:websearch:v5:%d:%d:%s", limit, maxPriceMinor, strings.ToLower(strings.Join(strings.Fields(query), " ")))
	if s.searchCache != nil {
		var cached []websearch.Result
		if s.searchCache.GetJSON(ctx, cacheKey, &cached) {
			return cached, nil
		}
	}
	var results []websearch.Result
	var err error
	if bs, ok := s.webSearch.(BudgetSearcher); ok && maxPriceMinor > 0 {
		results, err = bs.SearchWithBudget(ctx, query, limit, maxPriceMinor)
	} else {
		results, err = s.webSearch.Search(ctx, query, limit)
	}
	if err != nil {
		return nil, err
	}
	results = websearch.FlagSuspicious(withinBudget(results, maxPriceMinor))
	// Only cache a useful answer: an empty result is often a transient
	// upstream hiccup, and caching it would hide the product for minutes.
	if s.searchCache != nil && len(results) > 0 {
		s.searchCache.SetJSON(ctx, cacheKey, results, s.searchCacheTTL)
	}
	return results, nil
}

// withinBudget drops priced listings above maxPriceMinor and moves
// unpriced ones after the priced ones. 0 means no ceiling.
func withinBudget(in []websearch.Result, maxPriceMinor int64) []websearch.Result {
	if maxPriceMinor <= 0 {
		return in
	}
	priced := make([]websearch.Result, 0, len(in))
	var unpriced []websearch.Result
	for _, r := range in {
		switch {
		case r.PriceMinorUnits == 0:
			unpriced = append(unpriced, r)
		case r.PriceMinorUnits <= maxPriceMinor:
			priced = append(priced, r)
		}
	}
	return append(priced, unpriced...)
}

// handoffLink returns a connector's merchant-owned search link, if it has
// one, filtered through the same allowlist as product URLs.
func (s *DiscoveryService) handoffLink(c merchant.Connector, query string) string {
	h, ok := c.(merchant.HandoffLinker)
	if !ok {
		return ""
	}
	link := h.HandoffURL(query)
	if s.urlAllowlist != nil {
		link = s.urlAllowlist.SanitizeProductURL(link)
	}
	return link
}

func (s *DiscoveryService) discoverOne(ctx context.Context, pi *intent.PurchaseIntent, connector merchant.Connector) (*quote.CheckoutQuote, error) {
	cart, err := connector.CreateCart(ctx, pi.UserID)
	if err != nil {
		return nil, err
	}

	var matched []string
	for _, item := range pi.Items {
		products, err := connector.SearchProducts(ctx, item.Query, 5)
		if err != nil {
			return nil, err
		}
		best := bestMatch(products)
		if best == nil {
			continue // this item has no match at this merchant; cart may end up partial or empty
		}
		if _, err := connector.AddToCart(ctx, cart.ID, best.MerchantProductID, item.Quantity); err != nil {
			return nil, err
		}
		matched = append(matched, best.MerchantProductID)
	}

	// Delivery options are informational here — the connector's own
	// GetCheckoutQuote below remains authoritative on what's actually
	// charged (mandate §18: only a merchant-confirmed quote is financial
	// truth). Asking for them with the user's shipping ALIAS, never a
	// resolved address, is the point: the merchant can price delivery for
	// "home" without Algebra having handed over where home is yet.
	if connector.Capabilities().Cart && pi.Constraints.DeliveryProfile != "" {
		if _, err := connector.GetDeliveryOptions(ctx, cart.ID, pi.Constraints.DeliveryProfile); err != nil {
			// A merchant that can't price delivery yet isn't a discovery
			// failure — the checkout quote still decides.
			_ = err
		}
	}

	// Coupons: ask the merchant which offers apply to what's in the cart and
	// try them, best-effort. An invalid or expired code is a normal outcome
	// (mandate §63 lists "coupon expired" as a failure condition to handle,
	// not to crash on), so failures are swallowed and the un-couponed quote
	// stands.
	if connector.Capabilities().Coupons {
		s.applyBestCoupon(ctx, connector, cart.ID, matched)
	}

	q, err := connector.GetCheckoutQuote(ctx, cart.ID)
	if err != nil {
		return nil, err
	}
	q.CartID = cart.ID
	q.RetrievedAt = s.now()
	if q.ExpiresAt.IsZero() {
		q.ExpiresAt = s.now().Add(s.quoteTTL)
	}
	q.Recompute()
	return q, nil
}

// applyBestCoupon asks the merchant which offers exist for the matched
// products and tries their codes until one sticks. Algebra never invents,
// computes, or trusts a discount AMOUNT here — it only passes a code the
// merchant itself advertised and lets the merchant's own refreshed quote
// report what that did to the price (mandate §9: the agent supplies a
// coupon code to try, never an offer amount).
func (s *DiscoveryService) applyBestCoupon(ctx context.Context, connector merchant.Connector, cartID string, productIDs []string) {
	tried := map[string]bool{}
	for _, productID := range productIDs {
		offers, err := connector.GetOffers(ctx, productID)
		if err != nil {
			continue
		}
		for _, offer := range offers {
			code := strings.TrimSpace(offer.Code)
			if code == "" || tried[code] {
				continue
			}
			tried[code] = true
			if _, err := connector.ApplyCoupon(ctx, cartID, code); err == nil {
				return // one coupon per cart — merchants rarely stack them
			}
		}
	}
}

// bestMatch picks the highest-confidence available product. Below a 0.5
// confidence threshold nothing is returned — an uncertain match is worse
// than no match (mandate §16: "Never merge products below a safe confidence
// threshold").
func bestMatch(products []merchant.Product) *merchant.Product {
	const minConfidence = 0.5
	var best *merchant.Product
	for i := range products {
		p := &products[i]
		if !p.Available || p.Confidence < minConfidence {
			continue
		}
		if best == nil || p.Confidence > best.Confidence {
			best = p
		}
	}
	return best
}
