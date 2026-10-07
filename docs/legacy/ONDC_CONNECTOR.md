# ONDC connector — scope, not yet built

This is a design doc, not a shipped connector. No ONDC subscriber
credentials exist in this environment (BAP registration, signing keypair,
registry entry) — writing HTTP calls against the real network without them
would mean guessing a wire format for real orders, which is exactly what
this repo's connectors refuse to do (see the Zepto pre-mapping stub in
[MERCHANT_CONNECTORS.md](MERCHANT_CONNECTORS.md)). This doc exists so
building it later is a known, scoped task instead of a fresh investigation.

## Why ONDC, and why it doesn't cover Amazon/Flipkart

ONDC (Open Network for Digital Commerce) is India's government-backed open
protocol for commerce, built on the open-source **Beckn protocol**. Anyone
can register as a **Buyer App (BAP)** and transact with any registered
**Seller App (BPP)** over a standard API — no bilateral integration, no
scraping, no credential automation. It is the only path here where an agent
can *complete* a real purchase end to end through an official protocol
rather than a merchant-specific handoff link.

**Amazon and Flipkart are not on ONDC.** This connector would add a
different, real set of merchants — grocery/retail sellers, and platforms
like Paytm and Snapdeal that participate as BAPs/BPPs on the network — not
the two named in the original request.

## Protocol shape (verified against the official spec repo)

Source: [ONDC-Protocol-Specs](https://github.com/ONDC-Official/ONDC-Protocol-Specs).

ONDC/Beckn is **asynchronous and callback-based**, not request/response like
every other connector in this repo:

| BAP calls (outbound) | BPP responds later (inbound callback to the BAP's own registered URL) |
|---|---|
| `search` | `on_search` |
| `select` | `on_select` |
| `init` | `on_init` |
| `confirm` | `on_confirm` |
| `status` | `on_status` |
| `update` | `on_update` |
| `track` | `on_track` |
| `cancel` | `on_cancel` |
| `rating`, `support` | `on_rating`, `on_support` |

A BAP's outbound call to the Gateway/BPP gets only a synchronous ACK; the
actual result (search results, order confirmation, status) arrives as a
**separate inbound POST** to a callback URL the BAP registered at
`subscriber_url`. Every request and callback carries a `context` block with
a `transaction_id` (spans the whole order lifecycle) and `message_id` (one
call/callback pair), which is how a BAP correlates the async callback back
to the request that triggered it.

This maps loosely onto `merchant.Connector`'s verbs (`SearchProducts` →
`search`, `CreateCart`/`AddToCart` → `select`, `GetCheckoutQuote` → `init`,
`ExecuteCheckout` → `confirm`, `GetOrder` → `status`, `CancelOrder` →
`cancel`) but **the interface is synchronous** (`(result, error)` returns)
and ONDC fundamentally isn't. A real connector needs a correlation layer in
front of it — hold the call open until the matching callback lands (with a
timeout that maps to a merchant error, not a silent hang), or move
`merchant.Connector` to a polling/webhook shape for this one connector. That
design decision needs making before any code, not guessing mid-implementation.

## What registration actually requires

Not a credentials env-var away, unlike Amazon/Flipkart:

1. **Legal entity onboarding** — GST, PAN, business KYC with ONDC (or via a
   Technology Service Provider that's already a registered NP).
2. **A signing keypair** registered with the ONDC Registry — every
   request/callback is signed and the registry holds the public key used to
   verify it. The exact signing scheme (canonical request construction,
   digest, header format) needs reading from the spec repo directly when
   this is built — not reproduced here from memory, for the same reason the
   rest of this repo won't guess a payment wire format.
3. **A public HTTPS `subscriber_url`** that can receive the `on_*` callbacks
   — Algebra's API server would need an inbound endpoint the ONDC network
   can reach, which none of the other connectors need (they're all
   outbound-only).
4. **Staging network first** — ONDC runs a pre-production network for
   integration testing before a subscriber is promoted to production.
5. **Domain/category codes** (e.g. grocery vs. F&B vs. fashion use different
   `context.domain` values) — which domains to onboard is a product
   decision, not a technical default.

## Recommendation

Real payoff (actual completed orders through an official protocol) but real
work (signing, async correlation, a public callback endpoint, registry
onboarding) and it doesn't reach Amazon or Flipkart. Scope it as its own
project once:

- someone has run (or started) BAP registration on ONDC staging, and
- there's a decision on whether `merchant.Connector` gets an async variant
  or ONDC gets a bespoke path in `internal/app` alongside it.

Until then this stays a doc, not a `connectors/ondc` package — registering
an empty stub (like Zepto's link-and-list pattern) isn't possible here
because there's no equivalent of "link an account" without first being a
registered network participant.
