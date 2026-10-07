# Payment Security

## Non-negotiables (mandate §21/§69)

Algebra does not, and will not:

- Build its own PAN/CVV vault.
- Store PAN, CVV/CVC, track data, or PIN, anywhere, ever.
- Give payment credentials to an LLM.
- Let an LLM calculate the authoritative payment total (that's always `quote.CheckoutQuote.FinalPayable`, computed in `internal/domain/quote`, never re-derived by an agent).

## How a card actually flows through this system

```
Browser (hosted field / vault SDK)
        │  raw PAN/CVV — never leaves the browser, never touches Algebra
        ▼
PCI-compliant vault (Spreedly, in the target design)
        │  returns a single-use provider_nonce
        ▼
POST /api/v1/payment-sources { provider_nonce, alias, nickname }
        │
        ▼
PaymentService.AddCard → CardVaultProvider.Tokenize(ctx, req)
        │  vault exchanges the nonce for a durable token server-to-server
        ▼
payment.PaymentSource{ ProviderTokenRef, Network, Last4, ... }
        │  persisted — ProviderTokenRef is opaque and treated as a secret
        ▼
payment_sources table (Postgres)
```

`internal/domain/payment.PaymentSource` has no field that could hold a PAN or CVV — this isn't a policy an engineer has to remember to follow, it's a struct that structurally cannot carry one. `Safe()` additionally strips `ProviderTokenRef` and `BillingProfileID` before a `PaymentSource` is allowed to reach an MCP tool result or a REST response.

## What's real vs. not in this build

- **Real, functional**: the `CardVaultProvider` interface, the tokenize → persist → list → revoke flow, `.Safe()` redaction, the sandbox implementation.
- **Sandbox**: `providers/vault.SandboxProvider` — generates a token and a "last4" that are deterministic functions of the input nonce's hash, never a real card. Always reports `Mode() == "sandbox"`. Used for local dev and tests; must never be shown as a real payment method in any UI (mandate §54).
- **Not yet implemented**: `providers/vault.SpreedlyProvider` — a real Spreedly account and API keys are required and don't exist in this environment. Every method returns `shared.ErrNotImplemented`. Wiring it up means: swap the browser's card-entry UI to Spreedly's hosted iframe fields, and implement the actual Spreedly API calls in that file.

## Non-custodial crypto (mandate §20)

For any `WALLET` / `STABLECOIN_ACCOUNT` payment source, Algebra never holds a private key or seed phrase — there is no field for one anywhere in `internal/domain/payment`. The flow is: Algebra prepares the operation → `PolicyProvider` evaluates it → the user's own wallet signs it → the network executes it. Algebra facilitates; it never custodies.

The policy-evaluation half of that flow is native, in-process Go: `internal/domain/policy.LocalProvider.EvaluatePurchaseIntent`/`EvaluatePayment` apply a deterministic recipient allow/block-list, a hard per-transaction USDC cap, and a confirm-threshold-triggers-approval rule (`rules.go`'s `AllowedCryptoRecipients`/`BlockedCryptoRecipients`/`MaxCryptoTxUSDC`/`CryptoConfirmThresholdUSDC`) whenever a `PaymentSource` resolves to a crypto rail (`Input.CryptoRecipient`/`AmountUSDC` set). That logic is adapted from [OmniClaw](https://www.omniclaw.ai)'s MIT-licensed guard implementations (`recipient.py`/`single_tx.py`/`confirm.py`, still vendored for reference at `third_party/omniclaw`) rewritten as first-party Algebra code — no external service, no network call, no OmniClaw dependency at runtime. Whoever ultimately signs the payment (the agent's own wallet, non-custodial) is outside Algebra's process entirely, same as before — Algebra only ever produces the ALLOW/DENY/REQUIRE_APPROVAL decision, never a private key or a transaction result.

## 3DS / OTP / UPI (mandate §23)

Authentication is a state, not a failure: `intent.StateAuthenticationRequired`. `OrderService.Execute` returns an `AuthorizationChallenge` (`internal/domain/payment/source.go`) when a connector reports `ExecutionAuthenticationRequired`; the agent gets the challenge's existence and a redirect URL if applicable, never the OTP/challenge secret itself. `OrderService.CompleteAuthentication` resumes the flow afterward by asking the merchant connector for its own confirmed order (`GetOrder`) — Algebra never fabricates a "success" locally without that confirmation.

## Billing address (mandate §27)

`payment.PaymentSource.BillingProfileID` references a billing profile resolved only through `PrivacyResolver` (`internal/domain/privacy`), at the point of merchant execution — never earlier, never into an LLM-visible structure. See [PRIVACY.md](PRIVACY.md).
