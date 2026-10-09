# Provider Status

Every external payment/card integration Algebra could plug into, and its actual current status — not aspirational. Statuses:

- **REAL** — live, credentialed, talking to the real service.
- **SANDBOX** — real code path, against a provider's own sandbox/test environment.
- **MOCK** — deterministic, fully in-process, no external dependency at all (used for local dev/tests, never mistaken for real — every response is labeled).
- **PARTNER_REQUIRED** — no public API exists for what we'd need; requires a signed partnership/API access grant from the provider.
- **NOT_IMPLEMENTED** — architecturally scaffolded (an enum value, an interface the adapter would satisfy) but no adapter code exists yet.

## `backend/internal/domain/paymentprovider.Provider` (agentic payment execution)

| Provider | Mode | Status | Detail |
|---|---|---|---|
| `backend/providers/paymentdemo` | `DEMO` | **MOCK** | The only provider wired live in this build (Phase 1). Fully deterministic: registration, delegated authorization, scoped credentials with real expiry, and explicit success/decline/insufficient-funds/outage/authentication-required/duplicate-idempotency-key scenarios via `ExecutePaymentRequest.Metadata["demo_scenario"]`. See `docs/legacy/PAYMENT_PROVIDER_INTERFACE.md`. |
| Visa Intelligent Commerce | `NETWORK_TOKEN` | **NOT_IMPLEMENTED** | Public sandbox exists per Visa's own developer platform (agent-specific tokens, passkey step-up, scoped credential retrieval) — closest architectural fit to this whole interface. No adapter built; needs Visa developer credentials. |
| Mastercard Agent Pay | `NETWORK_TOKEN` | **NOT_IMPLEMENTED** | Same shape as Visa (registered agents, network tokens, verifiable consumer intent). No adapter built; needs Mastercard partner access. |
| Stripe (Shared Payment Tokens / Link agent wallet) | `PROCESSOR_AGENTIC` | **NOT_IMPLEMENTED** | Stripe's Agentic Commerce product is US-only per their own docs — real, but a partial-coverage path, not a default. No adapter built. |
| Razorpay agentic payments | `PROCESSOR_AGENTIC` | **NOT_IMPLEMENTED** | Razorpay advertises agentic commerce across UPI/cards/net banking with passkey card auth. No adapter built; needs Razorpay merchant/API access. |
| Cashfree agentic payments | `PROCESSOR_AGENTIC` | **NOT_IMPLEMENTED** | Same category as Razorpay. No adapter built. |
| KAST (consumer card control) | `ISSUER_NATIVE` | **PARTNER_REQUIRED** | KAST has stablecoin-funded Visa cards and spending controls, but no public developer API was found that lets a third party programmatically control a user's card. Needs KAST/its issuer or processor to grant partner access. |
| RedotPay (consumer card control) | `ISSUER_NATIVE` | **PARTNER_REQUIRED** | Same gap as KAST — no public API for third-party control of a consumer's RedotPay card. |
| RedotPay Connect (merchant-side stablecoin acceptance) | `MERCHANT_MANAGED` | **NOT_IMPLEMENTED** | A *different* product from the row above — a real, public merchant-acceptance API (a business creates an order, accepts stablecoin payment). Not built; would slot in as a merchant-side rail, not an agentic-spending rail — see the distinction called out in `docs/legacy/B2B_INTEGRATION.md`. |
| Non-custodial stablecoin wallet | `STABLECOIN` | **NOT_IMPLEMENTED** | `policy.Rules`' crypto-rail fields (`AllowedCryptoRecipients`, `MaxCryptoTxUSDC`, ...) are real, tested, and evaluated — but there is no execution path anywhere in the Go codebase. The vendored `third_party/omniclaw` Python reference has real wallet/signing code; nothing calls it. Wiring a real non-custodial `STABLECOIN` provider is separable Phase 2 work. |
| x402 / machine payments | `MACHINE_PAYMENT` | **NOT_IMPLEMENTED** | Enum value scaffolded for completeness; no adapter. |

## `backend/internal/domain/payment.CardVaultProvider` (card tokenization — separate, older abstraction)

| Provider | Status | Detail |
|---|---|---|
| `backend/providers/vault.SandboxProvider` | **SANDBOX** | Real, functioning, in-memory. Never receives/stores/derives a real PAN — `ProviderNonce` is treated as an opaque client-side reference. Wired live for the consumer console's "add card" flow. |
| `backend/providers/vault.SpreedlyProvider` | **NOT_IMPLEMENTED** | Every method returns `shared.ErrNotImplemented`. No Spreedly credentials configured in this environment. |

## `backend/internal/domain/confidential.Provider` (optional confidential-compute layer)

| Provider | Status | Detail |
|---|---|---|
| `backend/providers/arcium.LocalEncryptedProvider` | **REAL** (as what it claims to be) | Real AES-256-GCM encrypt/decrypt/threshold-evaluate. Its actual guarantee is "encrypted at rest, decrypted server-side to evaluate" — not genuine confidential/multi-party computation. Wired live. |
| `backend/providers/arcium.ArciumProvider` | **NOT_IMPLEMENTED** | Every method returns `shared.ErrNotImplemented`. No real Arcium program/cluster credentials exist in this environment. |
