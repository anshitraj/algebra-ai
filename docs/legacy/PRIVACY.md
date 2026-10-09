# Privacy

## The alias boundary

An agent never sees a real address, phone number, or card detail — only aliases:

```json
{ "shipping_profile": "home", "payment_profile": "personal" }
```

The full alias strings Algebra actually uses are namespaced (`"shipping:home"`, `"payment:personal"`), matching `payment_sources.alias` and `private_profiles.alias`. `backend/internal/domain/privacy.Resolver` is the **only** code path that turns one of these into a real value — `ResolveShipping` / `ResolveBilling` — and every resolution is audited (`ResolveAuthorization{Purpose, IntentID, RequestedBy}` is required on every call, no exceptions).

Nothing in `backend/internal/mcpserver` or `backend/internal/api/v1` calls `Resolver.Resolve*` — by construction, not by convention. There is exactly **one** call site in the entire system: `OrderService.resolveFulfillment`, invoked from `Execute` immediately before `connector.ExecuteCheckout` and nowhere else.

What that call produces is a `merchant.Fulfillment` — a struct that exists only for the duration of that one checkout call. It is never persisted, never logged, never returned from a service method, and never serialized into an MCP or REST response. The merchant connector receives the real address; the agent that started the whole thing only ever saw the string `"shipping:home"`.

This is enforced by test, not just by review: `TestOrchestration_PrivacyBoundary_MerchantGetsAddressAgentNeverDoes` (`backend/internal/app/orchestration_test.go`) runs a full purchase with a real `privacy.Resolver` (real AES-256-GCM), then asserts three things — the merchant connector actually received the address, the JSON of every agent-visible object (intent, quote, order, receipt) contains neither the street line nor the phone number, and a `PrivacyProfileResolved` audit event was recorded for the intent.

One deliberate detail: `GetDeliveryOptions` is called during discovery with the **alias** (`"shipping:home"`), not a resolved address — a merchant can price delivery for a saved location before Algebra has handed over where that location actually is.

## Storage: envelope encryption

`backend/internal/domain/privacy.AESGCMEncryptor` implements real AES-256-GCM encryption for shipping/billing profile payloads:

- Each encrypted value is bound (via AEAD "additional data") to its own row's `(id, user_id, type)` — ciphertext from one profile cannot be swapped into another's row and decrypt successfully. Tested in `encryption_test.go` (`TestAESGCM_WrongAADFails`).
- A GCM authentication failure (wrong key, tampered ciphertext, wrong AAD) returns a single generic error — no information about *why* it failed, which is what keeps this from becoming a padding-oracle-style side channel.
- The key (`ALGEBRA_MASTER_KEY`) is a static local-dev env var in this build. **Production target**: a Cloud KMS-wrapped data-encryption key, unwrapped once at process start — see [GCP_DEPLOYMENT.md](../GCP_DEPLOYMENT.md). No GCP project is configured in this environment, so that step isn't built, only designed for.

## What's classified as private-profile data

`backend/internal/domain/privacy.ProfileType`: `SHIPPING`, `BILLING`. Phone, email, and street address live inside these encrypted payloads — never as plain columns elsewhere, never in `purchase_intents.metadata` (which `intent.PurchaseIntent`'s own doc comment explicitly forbids), never in a log line (`backend/internal/platform/logging` redacts `phone`, `email`, and `address`-keyed fields as a second layer of defense regardless).

Card data is **not** privacy-profile data — it never reaches Algebra's backend as plaintext at all (see [PAYMENT_SECURITY.md](PAYMENT_SECURITY.md)), so there's nothing about a card for `PrivacyResolver` to protect.

## Confidential compute (optional, mandate §26)

`backend/internal/domain/confidential.Provider` is a separate, optional abstraction for computing over sensitive attributes without the caller needing the plaintext — e.g. "is this purchase over the user's (encrypted) daily threshold" without decrypting the threshold into the caller's process. `backend/providers/arcium.LocalEncryptedProvider` is the real, functional default (server-side decrypt-then-compare — a modest guarantee: encryption at rest, not genuine confidential computation). `backend/providers/arcium.ArciumProvider` is a stub pending a real Arcium program/cluster configuration this environment doesn't have. Algebra runs completely correctly with only the local provider; Arcium is never required.
