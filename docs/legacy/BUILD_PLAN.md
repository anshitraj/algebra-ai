# Project Algebra — Build Plan

Status: Phase 1 (Foundation) in progress. Written after repository audit, before implementation, per build mandate.

> **This is a point-in-time snapshot**, written before the Next.js console (`web/`), the standalone `Integrator` policy-evaluation surface, and the B2B agentic-payments layer (`Tenant`/`AgenticPaymentIntent`/`paymentprovider.Provider`) existed — several statements below (e.g. "no Next.js console") are now stale by omission, not by correction. For current state, see [README.md](../../README.md), [docs/legacy/B2B_INTEGRATION.md](B2B_INTEGRATION.md), and [docs/legacy/PROVIDER_STATUS.md](PROVIDER_STATUS.md). This document is kept as-is rather than rewritten — it's the historical record of the original audit and plan.

## 1. Repository audit result

`E:\algebra` was empty (no git repo, no source, no docs) at the start of this build. There is nothing to preserve, migrate, or reverse-engineer. This is a greenfield implementation, not a refactor.

External systems referenced by the mandate were checked for real, documented integration surfaces before any adapter code was written:

| System | What's actually there | Consequence for this build |
|---|---|---|
| OmniClaw (`omniclaw.ai`, `github.com/omnuron/omniclaw`) | Real project, still **vendored as a git submodule at `third_party/omniclaw`** (v0.0.8) for reference. Confirmed by reading it directly: a Python financial policy engine + non-custodial payment router for agent buyers, covering two rails — `circle_transfer` (Circle Developer Wallet) and `x402` (paid HTTP endpoints / Gateway nanopayments). Its policy schema (`src/omniclaw/agent/policy_schema.py`) is keyed on a wallet-address-or-URL `recipient`, a USDC decimal `amount`, and a `confirm_threshold` — there is genuinely no merchant name, INR amount, or product-category field anywhere in it. | **Superseded an earlier HTTP-client integration** (`internal/domain/policy/omniclaw_provider.go`, since deleted) that called a live OmniClaw agent server for the crypto-rail decision. That added an operational dependency — a separate running Python/FastAPI process, `OMNICLAW_SERVER_URL`/`OMNICLAW_TOKEN`, Circle/x402 credentials — for a decision that is genuinely small: recipient allow/block-list, a per-transaction cap, a confirm-above-threshold rule. OmniClaw's `guards/{recipient,single_tx,confirm}.py` (MIT-licensed) implement exactly that, so it's ported directly into `internal/domain/policy.LocalProvider` as first-party Go (`rules.go`'s `AllowedCryptoRecipients`/`BlockedCryptoRecipients`/`MaxCryptoTxUSDC`/`CryptoConfirmThresholdUSDC`) instead of called over HTTP. `LocalProvider` is now the sole `PolicyProvider` — merchant/category/INR and crypto-rail alike — fully in-process, no external service, nothing not-yet-implemented about the crypto-rail policy decision anymore. What OmniClaw's Go client still would have added beyond policy — actual `circle_transfer`/x402 payment *execution* (`Pay`/`ApproveConfirmation`/`DenyConfirmation`) — was never wired into `OrderService` in the first place and remains a real gap; the submodule stays vendored as the reference implementation if that execution rail gets built later. |
| MCP spec `2026-07-28` | Confirmed current. `github.com/modelcontextprotocol/go-sdk` v1.7.0+ supports it (typed tools via `mcp.AddTool`, stdio + streamable HTTP transports). | Our MCP server is built on the official Go SDK, stateless at the transport layer, calling into the same application services as REST. |
| Zepto MCP (`github.com/zeptonow/mcp`, `mcp.zepto.co.in/mcp`) | Real, hosted, remote MCP server behind OAuth. Verified with live unauthenticated requests: the 401 challenge points at protected-resource metadata naming `auth.zepto.co.in` (dynamic client registration, PKCE S256, public clients; scopes `tools:read`, `tools:write`, `dev.ucp.shopping.cart:manage`). Zepto documents search, cart, real order placement and order history, but **publishes no tool names or argument/result schemas**. | `connectors/zepto` on the shared `connectors/remotemcp` client: account linking (`cmd/merchant-login`, user signs in with phone + OTP on Zepto's page) and live tool listing are real. Every capability stays `false` — mapping unpublished tool shapes would mean guessing a wire format for real orders. `merchant-login` writes the live tool manifest for review; agents get a handoff link meanwhile. |
| Swiggy Instamart MCP (`mcp.swiggy.com/im`) | Real, hosted MCP server with a **published per-tool reference** (arguments and output schemas) and OAuth (dynamic registration, PKCE). Swiggy's builder program allows building against localhost; production access is reviewed by Swiggy. | `connectors/swiggyinstamart` implements search → cart → quote → Cash-on-Delivery checkout → order status against the published contract, re-validated against the live `tools/list` at startup. Tested end to end against a go-sdk MCP server returning the published shapes; **not run against a live Swiggy account** in this environment. Opt-in via `ENABLED_MERCHANTS`. |
| Amazon Creators API | The official Associates catalog API that replaced PA-API 5.0 (retired May 2026). Search/get items only; Amazon offers no third-party cart or order API. Its Associates "Add to Cart" link (`gp/aws/cart/add.html`) is a separate, older plain-HTML form action, not a PA-API operation. | `connectors/amazon`: real `searchItems` client (client-credentials token per credential version; India uses 3.2 or 2.2) and a `getItems`-based `GetProduct(asin)`, both tested against a fake server; not run against live credentials (none here). `CartURL(items)` builds the Associates add-to-cart link (pure, no network call) — not confirmed against a live Amazon page, since its old docs page now redirects to the PA-API 5 deprecation notice. Neither `GetProduct` nor `CartURL` is wired into a REST/MCP response yet. |
| Flipkart Affiliate API | Official, read-only catalog API (`affiliate-api.flipkart.net`). No cart/order API, no MCP server. | `connectors/flipkart`: real search client. Tested against a fake server in the documented shape; not run against live credentials (none here). |
| Blinkit | No public API, partner API, affiliate API, or MCP server. Community "Blinkit MCP" projects automate the consumer site behind anti-bot protection. | None used (mandate §10). `connectors/blinkit` returns only a handoff link to Blinkit's own search page; every capability is `false`. |
| Spreedly / card tokenization | Not integrated — no credentials available in this environment. | `CardVaultProvider` interface + a `SandboxVaultProvider` (deterministic fake tokens, clearly `provider_mode=sandbox`, never touches real PAN) + a `SpreedlyProvider` stub that fails closed with `ErrNotImplemented` until real API keys exist. |
| Arcium | Optional per mandate. No program/environment configured. | `ConfidentialComputeProvider` interface, `LocalEncryptedProvider` (real AES-256-GCM envelope encryption, functional today) as default, `ArciumProvider` stub returning `ErrNotImplemented`. |
| GCP | No project/credentials provided. | Deployment docs describe the target Cloud Run/Cloud SQL/Memorystore/Pub/Sub/KMS mapping; nothing deploys itself. Local dev uses Docker Compose Postgres+Redis. |

**Nothing above is faked.** Every "not yet implemented" path returns an explicit typed error and is labeled in code and docs, never a fabricated success.

## 2. Architecture (this build)

Modular monolith in Go, per the mandate's explicit preference ("start as a modular monolith... boundaries clean enough to split later"). Layout:

```
cmd/api        — HTTP API entrypoint (REST v1)
cmd/mcp        — MCP server entrypoint (stdio transport)
cmd/merchant-login — links a Zepto / Swiggy Instamart account (the user signs in on the merchant's own page)
internal/domain    — entities + interfaces, no I/O: intent, agent, policy, merchant, quote,
                      payment, privacy, approval, order, audit
internal/app       — application services (the ONE place business logic lives; REST, MCP,
                      and future SDK all call these, never duplicate logic)
internal/platform  — postgres, redis, config, structured logging w/ redaction
internal/api/v1    — REST handlers (thin: parse → call app service → serialize)
internal/mcpserver — MCP tool registration (thin: parse → call app service → serialize)
connectors/        — MerchantConnector implementations (mock, swiggyinstamart, zepto, amazon,
                      flipkart, blinkit, genericbrowser), plus remotemcp (shared OAuth +
                      MCP client) and sanitize (merchant-supplied text)
providers/         — pluggable external-system adapters (vault, arcium)
migrations/        — versioned SQL, Postgres is authoritative for commerce state
packages/schemas/  — JSON Schema for PurchaseIntent and other wire contracts, shared
                      across languages
docs/              — architecture, threat model, MCP, payment security, privacy,
                      connectors, GCP deployment, local dev
```

Full detail and diagrams: [docs/ARCHITECTURE.md](../ARCHITECTURE.md).

Python discovery/normalization workers and the TypeScript frontend/SDK are designed for (interfaces, schemas) but not built in Phase 1 — the mandate explicitly scopes Phase 1 to the Go foundation with no UI.

## 3. Data model

PostgreSQL is authoritative for all financial/commerce state (never Redis-only). Core tables (see [migrations/0001_init.sql](../../migrations/0001_init.sql)):

`users, agents, agent_permissions, purchase_intents, intent_items, merchant_connectors, private_profiles, payment_sources, quotes, quote_items, approvals, orders, order_events, policy_decisions, audit_events, idempotency_keys, webhook_events`

Redis is used only for: rate limiting, short-lived locks, idempotency-key fast path, quote caching. Never the system of record.

## 4. Trust boundaries

Five boundaries, never collapsed into one:

1. **User ↔ Agent** — user authentication (OIDC/session) is separate from agent authorization (`AgentIdentity` + scoped permissions, revocable).
2. **Agent ↔ Algebra** — every mutating MCP/REST call must carry `user_id + agent_id + client_id`. Agents get capabilities (`shopping.create_intent`, `payments.request`, ...), never raw credentials.
3. **Algebra ↔ Policy** — `PolicyProvider` decisions (`ALLOW/DENY/REQUIRE_APPROVAL`) are server-side, deterministic, logged, and **cannot be overridden by an LLM**. A DENY is terminal.
4. **Algebra ↔ Privacy** — `PrivacyResolver` is the only path from an alias (`shipping:home`, `payment:personal`) to real PII/payment metadata. The LLM/agent never sees resolved values; only merchant-execution code paths call the resolver, and every resolution is audited.
5. **Algebra ↔ Payment rails** — no PAN/CVV/private-key custody. Cards go through a tokenization vault (interface now, sandbox adapter now, real vendor later); crypto is non-custodial (Algebra prepares, wallet signs, network executes).

Full threat catalogue: [docs/THREAT_MODEL.md](../THREAT_MODEL.md).

## 5. Real vs. sandbox vs. mock vs. not-yet-implemented

This is the authoritative labeling for everything shipped in this build; the completion report at the end restates it.

- **REAL**: `PurchaseIntent` state machine, Postgres schema/migrations, append-only audit log, `AgentIdentity` + scoped permissions + revocation, `LocalPolicyProvider` rule engine (merchant/category/INR **and** crypto-rail recipient/amount/confirm-threshold — see §1's OmniClaw row), `PrivacyResolver` + AES-256-GCM envelope encryption (`LocalEncryptedProvider`), approval binding/hashing, idempotency-key middleware, MCP server (official Go SDK, real tool surface), REST API v1, quote/effective-cost calculator, structured logging with redaction.
- **SANDBOX**: `SandboxVaultProvider` (deterministic fake tokens shaped like a real vault's, `provider_mode=sandbox`, never a real card), `MockConnector` (deterministic fake merchant, `provider_mode=mock`, used for local E2E and tests).
- **REAL CODE, VERIFIED ONLY AGAINST CONTRACT-SHAPED FAKES** (no linked merchant account or merchant credentials exist in this environment, so none of these has run against the live service): Swiggy Instamart connector (official MCP), Amazon Creators API search, Flipkart Affiliate API search, and `connectors/remotemcp` OAuth linking + MCP client. Zepto's OAuth discovery chain was checked live, unauthenticated.
- **NOT YET IMPLEMENTED** (interface + stub exists, fails closed with a typed error, needs an external prerequisite before it can be real): Zepto search/cart/checkout mapping (linking and live tool listing work; Zepto publishes no tool schemas to map against), Amazon/Flipkart cart and checkout (neither offers a third-party cart/order API — `USER_INTERVENTION_REQUIRED` with the product link, never a fake response), anything for Blinkit beyond a handoff link (no official interface exists), Swiggy Instamart UPI payment (COD only), per-user merchant sessions (one operator-linked account per merchant), `SpreedlyProvider` (needs Spreedly account/keys), `ArciumProvider` (needs Arcium program/environment), `BrowserExecutor` (Phase 6, intentionally deferred — the mandate orders it after merchant/API flows are stable).

## 6. Security risks called out up front

- A `PolicyProvider` that an agent/LLM can influence is a full compromise. Mitigation: policy decisions are computed server-side from the persisted intent/agent/payment-source records only; the request handler never accepts a client-supplied decision.
- Approval replay / stale-quote execution: mitigated by binding approvals to `(merchant, items_hash, amount±tolerance, currency, payment_source)` and requiring quote refresh immediately before authorization (`REAPPROVAL_REQUIRED` on drift).
- Secret/PII leakage into logs or MCP tool results: mitigated by a redaction layer in `internal/platform/logging` plus MCP tool output types that structurally cannot carry PAN/CVV/OTP/private-key fields (they don't exist as struct fields anywhere in the MCP layer).
- SSRF via merchant/browser connectors: domain allowlist + URL validation live in the `merchant` domain package and are enforced before any outbound connector call.

Full list: [docs/THREAT_MODEL.md](../THREAT_MODEL.md).

## 7. Implementation phases (this session)

Following the mandate's execution order (§73/§56-57), scoped to what's achievable without external credentials:

1. ✅ Repository audit (this document).
2. ✅ Architecture + threat model docs.
3. PurchaseIntent + deterministic state machine (server-validated transitions, audit event per transition).
4. Postgres schema + migrations + audit layer.
5. `PolicyProvider` (local real engine + OmniClaw stub).
6. `PrivacyResolver` + encrypted profile abstraction.
7. `MerchantConnector` abstraction + mock + labeled stubs.
8. MCP server (official Go SDK) exposing the mandate's limited tool surface.
9. REST API v1 + OpenAPI, sharing application services with MCP.
10. Approval workflow (binding, hashing, expiry, reapproval-required).
11. `PaymentSource` + `CardVaultProvider` interfaces + sandbox adapter.
12. Idempotency + concurrency (transactional state-transition locking).
13. Tests: unit (state machine, policy, quote math, privacy redaction, idempotency) plus in-memory-fake application-service orchestration tests covering the full `create → discover → quote → policy → approve → execute → receipt` flow, and a Postgres-backed integration/E2E suite for the same flow against real SQL.
14. Docker Compose for local Postgres/Redis, `.env.example`, CI (Go fmt/vet/test).

Phases 14+ from the mandate (multi-merchant discovery breadth, real tokenization, Arcium, browser executor, Next.js console) are explicitly **not** in this session's scope — see "Next highest-value milestone" in the completion report.

## 8. Local environment note: Postgres-backed tests unverified in this session

This machine's Docker Desktop backend is crash-looping on a corrupted internal socket (`...\Docker\run\dockerInference`) left over from a prior session — confirmed from its own logs (`starting services: initializing Inference manager: ... The file cannot be accessed by the system`), not caused by anything in this build. Killing every Docker/WSL process and deleting the stale socket file both failed with the same OS-level "cannot be accessed" error; this needs a reboot to clear (not done without asking — see below). A native PostgreSQL 18 service is also installed and running on this machine, but requires a superuser password this session doesn't have and won't attempt to guess.

Given that, `internal/platform/postgres/integration_test.go` and `test/e2e/e2e_test.go` are fully written (real SQL against the real schema; the full sandbox purchase flow end to end) and skip cleanly when no database is reachable — they have **not** been run against a live database in this session. Everything not requiring a database — all domain-layer unit tests, and the `internal/app` orchestration tests running the same flow against in-memory fakes plus the real mock connector and real policy engine — has been run and is green; see the completion report's "Tests" section, including a real bug those orchestration tests caught and a fix that landed as a direct result.

To finish verification once the Docker/Postgres situation is sorted: `make dev-up && make test-integration` (see [docs/LOCAL_DEVELOPMENT.md](../LOCAL_DEVELOPMENT.md)).

## 9. Wiring audit — components that existed but were unreachable

A pass specifically looking for code that was built, tested in isolation, and then never actually called by a running request. What it found, and what was done:

| Found | Status now |
|---|---|
| **`PrivacyResolver` was never called on the execution path.** The entire alias→address mechanism — the product's central privacy claim — was decorative: `ExecuteCheckout` had nowhere to put an address. | Fixed. `merchant.Connector.ExecuteCheckout` now takes a `merchant.Fulfillment`; `OrderService.resolveFulfillment` is its single call site, invoked immediately before the connector call. Proven by `TestOrchestration_PrivacyBoundary_MerchantGetsAddressAgentNeverDoes`, which asserts the merchant got the address AND that no agent-visible object's JSON contains it. |
| `GetOffers` / `ApplyCoupon` never called — no coupon engine despite mandate §17. | Fixed. `DiscoveryService.applyBestCoupon` asks the merchant which offers apply and tries the codes; the merchant's own refreshed quote reports the discount (Algebra never computes one). Tested. |
| `GetDeliveryOptions` never called. | Fixed — called during discovery with the shipping **alias**, never a resolved address. |
| `CancelOrder` never called — no way to cancel a placed order. | Fixed. `OrderService.CancelOrder` + `POST /api/v1/intents/{id}/cancel-order`; a merchant refusal is an error, and the test asserts the *merchant's* record also flipped. |
| `OrderService.CompleteAuthentication` existed but no transport exposed it — the 3DS/OTP resume path was unreachable. | Fixed — `POST /api/v1/intents/{id}/complete-authentication`. |
| No way to read the audit trail back, despite "complete audit trail" being a headline claim and §45 listing `/audit`. | Fixed. `AuditRepo.ListByIntent` (read side only — still no Update/Delete anywhere) + `AuditService` + `GET /api/v1/intents/{id}/audit`. |
| `order_events` rows were written but never readable. | Fixed — `OrderRepo.ListEvents`. |
| `merchant.AllowedDomains` / `ValidateURL` defined but enforcing nothing. | Fixed. Now hardened with loopback/private/link-local rejection (independent of the allowlist) and applied to every merchant-supplied product URL via `DiscoveryService.SetURLAllowlist`. Tested against metadata-endpoint and private-subnet URLs. |
| `providers/arcium` + `internal/domain/confidential` never constructed anywhere — dead packages. | Fixed — `LocalEncryptedProvider` is constructed in `wiring.Build` and exposed on the `Bundle`. |
| `Authenticate`, `GetProduct`, `RemoveFromCart` on `Connector` still have no callers. | **Left as-is, deliberately.** These are interface surface a real connector genuinely needs (session establishment, product detail, cart editing); inventing artificial call sites would be worse than documenting the gap — see [docs/legacy/MERCHANT_CONNECTORS.md](MERCHANT_CONNECTORS.md)'s call-site table. |

## 10. Merchant options: Zepto, Swiggy Instamart, Amazon, Flipkart, Blinkit

Each merchant uses the best official integration that actually exists — see the audit table in §1 and [docs/legacy/MERCHANT_CONNECTORS.md](MERCHANT_CONNECTORS.md) for the per-merchant contract, limits, and sources.

What was added:

- `connectors/remotemcp` — OAuth 2.1 account linking for remote MCP merchants (RFC 9728 → 8414 → 7591 dynamic registration → PKCE S256 + state + RFC 8707 resource → loopback redirect), AES-256-GCM session storage bound to merchant and endpoint, and an MCP client that refreshes tokens, never follows redirects with a bearer token, and turns 401/403 into "re-link the account".
- `cmd/merchant-login` — the user-driven linking CLI; also writes the live tool manifest for review.
- Connectors: `swiggyinstamart` (search/cart/quote/COD checkout/order status), `zepto` (link + live tool listing; capabilities off until its schemas are known), `amazon` and `flipkart` (catalog search), `blinkit` (handoff link only).
- `merchant.Status`, `merchant.HandoffLinker`, `merchant.Warmer`; `app.DescribeMerchants` shared by `GET /api/v1/merchants` and `commerce.list_merchants`, so both report the same readiness; handoff links in `commerce.search_products`.
- Config: `ENABLED_MERCHANTS`, `MERCHANT_SESSION_DIR`, `CONNECTOR_TIMEOUT`, per-merchant endpoints and credentials (`.env.example`).

Found and fixed while wiring these in:

| Found | Status now |
|---|---|
| Discovery sent every *searchable* connector through `CreateCart`. A search-only connector (Amazon, Flipkart) would fail on every intent and trip its circuit breaker — which would then also hide its search results. | Fixed. Quotes come only from connectors with Search **and** Cart; search-only merchants still serve `search_products`. `TestDiscovery_OnlySearchAndCartMerchantsProduceQuotes`. |
| The per-connector discovery timeout was hardcoded to 8s — fine for the in-memory mock, too short for a real merchant needing several round trips per intent. | Now `CONNECTOR_TIMEOUT`, default 20s. |

To take any of them live: set credentials (Amazon, Flipkart) or link an account (`go run ./cmd/merchant-login -merchant zepto`, `-merchant swiggy_instamart -alias shipping:home`), then check `GET /api/v1/merchants` — each merchant's `status.detail` says what's still missing.
