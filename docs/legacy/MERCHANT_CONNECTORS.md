# Merchant Connectors

Every merchant integration implements `internal/domain/merchant.Connector`. Nothing outside a connector talks to a merchant directly — `internal/app`'s discovery/quote/order services only ever call through this interface, via `app.ConnectorRegistry`.

## Merchant options at a glance

Checked against each merchant's public documentation in September 2026 (sources at the end).

| Merchant (`name`) | Integration | Search | Cart | Checkout | Coupons | Order tracking | To turn it on |
|---|---|---|---|---|---|---|---|
| Mock (`mock`) | in-memory mock | ✅ | ✅ | ✅ | ✅ | ✅ | nothing — dev/test only |
| Swiggy Instamart (`swiggy_instamart`) | **official MCP** `mcp.swiggy.com/im` | ✅† | ✅† | ✅† Cash on Delivery | ❌ | ✅† | opt in via `ENABLED_MERCHANTS`, link an account, Swiggy production approval |
| Zepto (`zepto`) | **official MCP** `mcp.zepto.co.in/mcp` | ❌ | ❌ | ❌ | ❌ | ❌ | linking works today; capabilities need Zepto's tool schemas verified and mapped |
| Amazon (`amazon`) | **official API** — Creators API | ✅† | ❌ | ❌ | ❌ | ❌ | Associates account + Creators API credentials |
| Flipkart (`flipkart`) | **official API** — Affiliate API | ✅† | ❌ | ❌ | ❌ | ❌ | Affiliate ID + token |
| Blinkit (`blinkit`) | none exists → handoff link | ❌ | ❌ | ❌ | ❌ | ❌ | nothing; always a handoff link |
| Generic browser (`generic-browser`) | not implemented (Phase 6) | ❌ | ❌ | ❌ | ❌ | ❌ | — |

† once that merchant's setup is done. Until then the capability is `false`, and `GET /api/v1/merchants` / `commerce.list_merchants` return a `status` saying exactly what is missing:

```json
{
  "name": "flipkart",
  "mode": "real",
  "capabilities": { "search": false, "cart": false, "checkout": false, "coupons": false, "order_tracking": false },
  "status": {
    "integration": "affiliate_api",
    "ready": false,
    "detail": "Set FLIPKART_AFFILIATE_ID and FLIPKART_AFFILIATE_TOKEN (from the Flipkart Affiliate program) to enable catalog search. Meanwhile agents get a link to Flipkart's own search page.",
    "source": "https://affiliate.flipkart.com/api-docs/af_prod_ref.html"
  }
}
```

`Capabilities()` is computed by code at request time, never a hand-typed table (mandate §9/§44: "Never pretend an unsupported capability exists").

## How merchants are selected

- **Registration** — `ENABLED_MERCHANTS` (default `mock,zepto,amazon,flipkart,blinkit,generic-browser`). An unknown name fails startup. A connector that still needs setup is registered anyway, so it can explain itself.
- **Per intent** — `constraints.preferred_merchants` / `excluded_merchants` take the names above.
- **Quotes** (`commerce.get_quotes` via discovery) come only from connectors that can **search and hold a cart**: today, `mock` and a linked `swiggy_instamart`.
- **Search / compare** (`commerce.search_products`, `commerce.compare_products`) include search-only merchants (Amazon, Flipkart) with real catalog results. Merchants that can't search but have a handoff link (Blinkit, Zepto, and Amazon/Flipkart without credentials) appear with a `handoff_url` instead of products — a link to the merchant's own search page for the user to open. Algebra never fetches it.

## Linking an account (Zepto, Swiggy Instamart)

Both publish OAuth-protected remote MCP servers that act on the user's own account. Linking is a one-time, user-driven step:

```bash
go run ./cmd/merchant-login -merchant zepto
```

```bash
go run ./cmd/merchant-login -merchant swiggy_instamart -alias shipping:home
```

1. The CLI discovers the server's protected-resource metadata (RFC 9728), the authorization server's metadata (RFC 8414), and registers a **public** OAuth client dynamically (RFC 7591).
2. It prints an authorization URL. **You** open it and sign in on the merchant's own page — phone number and OTP. Algebra never sees, relays, or automates the OTP (mandate §23/§28).
3. The merchant redirects to a loopback listener (`http://127.0.0.1:8765/callback` by default); the CLI verifies `state`, the RFC 9207 issuer, exchanges the code with the PKCE verifier and RFC 8707 resource indicator, and stores the tokens.
4. It lists the live MCP tools and writes a manifest (names + JSON schemas, no account data) to `.data/merchant-tools/<merchant>.json`.
5. Swiggy only: it shows your saved Swiggy addresses in your terminal and records which one the alias stands for (the opaque address ID only).

Unlink with `-unlink`.

What `connectors/remotemcp` guarantees, each covered by a test:

- PKCE **S256** required (refuses servers that don't advertise it); `state` compared in constant time; the callback page is static and single-use; the listener binds loopback only.
- Every scope the resource server advertises is requested up front — Zepto's first 401 challenge asks only for `tools:read`, and a server process can't do interactive step-up later.
- Sessions are AES-256-GCM encrypted under `ALGEBRA_MASTER_KEY`, with the merchant name as AAD (a session file copied to another merchant's name fails to decrypt), and bound to the endpoint they were issued for.
- Bearer tokens are refreshed and rotated tokens persisted; a redirect is never followed with a token attached; HTTP 401/403 turns into `ErrSessionExpired`, which switches the connector's capabilities off until the user re-links.
- Merchant-supplied text that reaches an agent (product names, tool errors, tool names) goes through `connectors/sanitize` — control, zero-width, and bidi characters stripped, length capped. It is still treated as data.

**Scope of this build:** one linked account per merchant — the operator's own. Per-user merchant accounts need a `merchant_sessions` table keyed by user, and connector calls that carry the user through; not built yet.

## Swiggy Instamart (`connectors/swiggyinstamart`)

Built against Swiggy's published Instamart tool reference. The connector checks the live `tools/list` against the tools and argument names it uses (`swiggyinstamart.Contract`: `get_addresses`, `search_products`, `update_cart`, `clear_cart`, `get_cart`, `get_payment_options`, `checkout`, `get_orders`) at startup and every minute while not ready. Any mismatch turns every capability off.

- **Real orders, Cash on Delivery only.** Algebra is non-custodial and never pays Swiggy on anyone's behalf. Every quote carries `payment_source_requirements: ["CASH_ON_DELIVERY"]` so policy and the approval screen show it. The UPI flow (user approves in their UPI app, then the order is confirmed) is not wired.
- **The address never leaves Algebra.** Swiggy orders against saved address IDs. The intent's shipping alias must be the one mapped at link time (checked in `GetDeliveryOptions`). At checkout, the privacy-resolved profile is used only to cross-check the PIN code against the saved address; the address itself is never sent (asserted in `TestCODOrder_EndToEnd`).
- **One account, one server-side cart.** `update_cart` replaces the whole cart. Every quote and checkout re-reads it and refuses to continue unless it matches exactly what Algebra added — e.g. if the user edited it in the Swiggy app.
- **Quotes equal Swiggy's own "To Pay".** Bill lines are mapped onto the quote's named fees by label; anything unexplained is reconciled into `other_fee`, so `final_payable` is always Swiggy's payable amount. Item prices are JSON numbers without a documented unit and are read as rupees; they are informational only.
- **Ambiguous outcomes need a human, never a retry.** A transport failure during `checkout`, a multi-store checkout with any failed store, or an unexpected online-payment order all return `MERCHANT_INTERVENTION_REQUIRED`. Swiggy's `success: false` returns `FAILED` with Swiggy's message.
- **Cancellation** isn't offered by Swiggy's MCP (Swiggy directs it to customer care), so `CancelOrder` is `ErrNotImplemented`.
- **Production access:** Swiggy lets anyone build against `http://localhost`, but reviews production access (builders@swiggy.in). That's why the connector is opt-in.

## Zepto (`connectors/zepto`)

Verified: `https://mcp.zepto.co.in/mcp` answers unauthenticated requests with a 401 pointing at its protected-resource metadata, which names `https://auth.zepto.co.in` (dynamic registration, PKCE S256, public clients) and scopes `tools:read`, `tools:write`, `dev.ucp.shopping.cart:manage`. Zepto documents that the server searches the live catalog, manages the cart, places real orders (COD, UPI, cards, Zepto Cash) and reads order history — but publishes **no tool names or argument/result schemas**.

So the connector links, connects and lists the live tools, and reports them in its status — but enables nothing. Guessing a wire format for real orders is exactly what mandate §10/§75 forbids. To enable Zepto:

1. Link an account (above) and review `.data/merchant-tools/zepto.json`.
2. Map the verified tools in `connectors/zepto`, guarded by `remotemcp.CheckTools` like the Swiggy connector, with tests against a fake server returning those exact shapes.

Meanwhile agents get a handoff link to Zepto's search page. (The `dev.ucp.shopping.cart:manage` scope suggests Zepto implements the Universal Commerce Protocol cart capability — `create_cart`/`get_cart`/`update_cart`/`cancel_cart` — which is worth checking against the manifest first.)

## Amazon (`connectors/amazon`)

Uses the **Creators API**, which replaced Product Advertising API 5.0 (retired May 2026): `POST https://creatorsapi.amazon/catalog/v1/searchItems` with `x-marketplace: www.amazon.in`, an OAuth2 client-credentials token, and `Authorization: Bearer <token>` (plus `, Version 2.x` for legacy Cognito credentials). India belongs to the Europe/Middle East/India credential group: version **3.2** (`api.amazon.co.uk`) or **2.2** (Cognito `eu-south-2`).

- Search only for cart/checkout. Amazon offers Associates no cart or order API; buying happens on Amazon through the detail-page link (`HandoffURL`/`Product.URL`) or the prefilled cart link (`CartURL`, below).
- Results are fetched live, never cached (Associates policy limits showing stale prices).
- Not yet proven against a live credential: the resource names `itemInfo.byLineInfo` and `offersV2.listings.availability` are the camelCase forms of PA-API 5 resources, and the v3 token endpoint's client-auth style is auto-detected. If Amazon rejects either, search fails with the API's own error — never silently.
- **`GetProduct(asin)`** calls `getItems` on the same base URL, auth and resource vocabulary as `searchItems`. Its exact request/response envelope (`itemIds`/`itemIdType`, `itemsResult.items`) is extrapolated from `searchItems`' verified lowerCamelCase convention and PA-API 5's documented `GetItems`, not from a confirmed live Creators API example — same "tested against a fake server, not live" tier as the rest of this connector. Nothing calls it yet (no caller wired).
- **`CartURL(items)`** builds Amazon's Associates "Add to Cart" link (`https://www.amazon.in/gp/aws/cart/add.html?AssociateTag=…&ASIN.1=…&Quantity.1=…`, up to 10 lines) — a plain HTML form action, not a PA-API REST call, so it predates and should be unaffected by the PA-API 5 retirement. It is a pure URL builder: no network call, same non-custodial shape as `HandoffURL`, just prefilled with items so the user has one link to review and pay on Amazon. **Not confirmed against a live Amazon page** — its old documentation page now redirects to the PA-API 5 deprecation notice, so verify it still loads a populated cart before depending on it. Not wired into any REST/MCP response yet.

## Flipkart (`connectors/flipkart`)

Uses the **Affiliate API**: `GET https://affiliate-api.flipkart.net/affiliate/1.0/search.json?query=…&resultCount=…` with `Fk-Affiliate-Id` / `Fk-Affiliate-Token` headers.

- Search only. The Affiliate API is a read-only catalog API; Flipkart publishes no third-party cart or order API and no MCP server.
- Accepts both result keys seen in the wild (`productInfoList` per the reference, `products` in responses and client libraries). Prices must be INR; the Flipkart special price is used when present, else the selling price.
- The token is a custom header, so redirects are never followed (net/http would forward it to another host).
- Product-by-ID lookup isn't wired: its parameter contract isn't in the public reference.

## Deals, offers and bank/card offers (`commerce.find_deals`, `GET /api/v1/deals`)

`DiscoveryService.FindDeals` collects what's discounted for a product. It's informational: nothing is applied to a cart, and **no coupon code is ever returned** — neither store's API publishes codes, and Algebra never scrapes coupon sites, Reddit or merchant pages for them.

| Source | What it gives | How |
|---|---|---|
| Flipkart | Published offers + Deals of the Day (category/brand sales, with start/end times) | Affiliate **Offers API**: `GET https://affiliate-api.flipkart.net/affiliate/offers/v1/all/json` (`allOffersList`) and `/dotd/json` (`dotdList`), same `Fk-Affiliate-*` headers. The feed is global, so it's fetched at most every 15 min and matched to the query on content words (promo filler like "flat"/"off" is ignored, so "flat feet" never matches a bedsheet sale). A failed refresh serves the last feed for up to 2 h. |
| Amazon | Price drops against Amazon's reference price (M.R.P./list/was) and live time-boxed deals (badge, end time, % claimed, Prime-only) | Creators API `searchItems` with `offersV2.listings.price` (`savings`, `savingBasis`) and `offersV2.listings.dealDetails`. Live, never cached. OffersV2 has **no coupon/promotion data** (Amazon discontinued `Offers.Listings.Promotions`). |
| Bank/card offers | "10% off with HDFC credit cards, up to ₹1,250, on ₹5,000+, until 2 Oct" | **Operator-curated** — neither store publishes these through an API. `BANK_OFFERS_FILE` points at a JSON file ([example](../bank-offers.example.json)); each offer must have `ends_at` and a `terms_url` on the merchant's own (allowlisted) domain, or it's skipped. Expired offers hide themselves; the file is re-read when it changes, and a broken edit keeps the last good set. Given the item's price, offers above their minimum order are dropped and the rest carry an `estimated_discount_minor_units`; given the user's banks (the agent stores them as the `payment.cards` preference), matching offers are flagged and listed first. |

A merchant that returns nothing comes back with a `notes` entry saying why (e.g. not configured), so an agent never implies "no deal exists" when it simply couldn't look. Unconfigured connectors are skipped without a call, so they never count against the circuit breaker their searches share.

## Blinkit (`connectors/blinkit`)

Blinkit publishes no public API, partner catalog API, affiliate API, or MCP server. The community "Blinkit MCP" projects work by driving blinkit.com's consumer site with a headless browser and replaying private endpoints behind Cloudflare/anti-bot protection — mandate §10 forbids exactly that, so none are used or ported. The connector only returns a link to Blinkit's own search page; every capability is false and checkout returns `USER_INTERVENTION_REQUIRED`.

## General web-search fallback (`connectors/websearch`, not a merchant)

`commerce.web_search` (MCP only, like `commerce.search_products`) is the
last resort when no connected merchant connector finds the product: a
general web search via Google's official Custom Search JSON API. It is
deliberately **not** a `merchant.Connector` — it isn't registered in
`ConnectorRegistry` and never appears in `GET /api/v1/merchants` — because
its results are arbitrary public pages, not a priced item from a storefront
Algebra has an integration with. No price, no cart, no checkout, no order;
just a title/snippet/link for the user to open. Off (`ErrNotImplemented`)
unless `GOOGLE_SEARCH_API_KEY` and `GOOGLE_SEARCH_ENGINE_ID` are set (a
Google Cloud API key and a Programmable Search Engine ID configured to
search the whole web, from https://programmablesearchengine.google.com/).

Every result link is checked against
`merchant.ValidatePublicHTTPSURL` (https-only, no loopback/private/
link-local address) before it reaches an agent — the same SSRF guard every
merchant product URL gets, minus the domain allowlist, since a general
web-search result can legitimately point at any public domain.

## Generic browser connector

`connectors/genericbrowser` is a named, empty slot for the `BrowserExecutor`-backed fallback (mandate §13, Phase 6) — constrained, domain-allowlisted, non-CAPTCHA-bypassing automation for merchants with no official API. It is deliberately unimplemented: the mandate is explicit that browser execution comes only after merchant/API flows are stable, and shipping even a minimal version before the domain-allowlist / ephemeral-environment / redaction machinery it needs exists would be an unsafe shortcut.

## What the app layer actually calls today

| Method | Called by | Notes |
|---|---|---|
| `SearchProducts` | `DiscoveryService.Discover` + `SearchProducts` | Per-connector timeout (`CONNECTOR_TIMEOUT`, default 20s) + circuit breaker |
| `CreateCart` / `AddToCart` | `DiscoveryService.discoverOne` | Only for connectors with Search **and** Cart |
| `GetOffers` / `ApplyCoupon` | `DiscoveryService.applyBestCoupon` | Best-effort; Algebra passes a merchant-advertised **code**, never an amount |
| `GetDeliveryOptions` | `DiscoveryService.discoverOne` | Called with the shipping **alias**, never a resolved address |
| `GetCheckoutQuote` | `DiscoveryService` + `QuoteService.RefreshQuote` | The only authoritative source of what will be charged |
| `ExecuteCheckout` | `OrderService.Execute` | Receives `merchant.Fulfillment` — the privacy-resolved address, materialized for this call only |
| `GetOrder` | `OrderService.CompleteAuthentication` | Fetches the merchant's own confirmed order; Algebra never invents one |
| `CancelOrder` | `OrderService.CancelOrder` | A merchant refusal is an error, not a local status flip |
| `Capabilities` / `Name` / `Mode` | Everywhere | |
| `Status` (optional `StatusReporter`) | `app.DescribeMerchants` → REST + MCP merchant listing | Plain-language readiness; never contains credentials or personal data |
| `HandoffURL` (optional `HandoffLinker`) | `DiscoveryService.SearchProducts` | Only for connectors that can't search; allowlist-filtered |
| `Warm` (optional `Warmer`) | `wiring.warmConnectors` at startup | Background; connectors also re-check every minute while not ready |
| `Authenticate`, `GetProduct`, `RemoveFromCart` | **nothing yet** | Implement them honestly; don't expect a caller today |

## Product and handoff URLs are sanitized

Any `Product.URL` or handoff link a connector returns passes through `AllowedDomains.SanitizeProductURL` before it reaches an agent (`MERCHANT_URL_ALLOWLIST`, default `zepto.co.in,zeptonow.com,swiggy.com,amazon.in,flipkart.com,blinkit.com`). A URL that isn't https, isn't on the allowlist, or points at a loopback/private/link-local address is blanked (and a handoff with no valid link is dropped). The private-address rejection is independent of the allowlist, so widening the allowlist can't open an SSRF hole.

## Adding a connector

1. Get official API access, or confirm an MCP tool contract from the merchant's published reference or a reviewed live manifest.
2. Implement `merchant.Connector` — `Capabilities()` first, honestly — plus `Status()`. For an MCP merchant, build on `connectors/remotemcp` and guard the mapping with `remotemcp.CheckTools`.
3. Test against a fake server that returns the merchant's documented shapes (see the Swiggy, Flipkart and Amazon tests).
4. Add a factory in `internal/platform/wiring/merchants.go` — the one place both REST and MCP get connectors from — and document its env vars in `.env.example`.

## Sources

- Swiggy Instamart MCP reference — https://mcp.swiggy.com/builders/docs/reference/instamart/
- Zepto MCP — https://github.com/zeptonow/mcp and https://mcp.zepto.co.in/.well-known/oauth-protected-resource
- Amazon Creators API — https://affiliate-program.amazon.com/creatorsapi/docs/en-us/api-reference and https://affiliate-program.amazon.com/creatorsapi/docs/en-us/migrating-to-creatorsapi-from-paapi
- Flipkart Affiliate API — https://affiliate.flipkart.com/api-docs/af_prod_ref.html
- Universal Commerce Protocol, cart MCP binding — https://ucp.dev/specification/shopping/cart/mcp/
- MCP authorization (OAuth 2.1, RFC 9728/8414/7591/8707) — https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization
