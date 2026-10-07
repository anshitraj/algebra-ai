# Integrating Algebra Policy

Algebra's policy engine — deterministic ALLOW / DENY / REQUIRE_APPROVAL
decisions over WHO / WHAT / WHERE / HOW MUCH / WITH WHAT / UNDER WHAT
CONDITIONS — is usable standalone by a third-party application, without
going through Algebra's own commerce flow (`PurchaseIntent`, discovery,
merchant connectors). If you're a wallet, a checkout provider, or anything
else that needs to decide whether *your own* user's transaction should go
through, this is for you.

Two surfaces, same underlying engine:

- **REST**: `POST /api/v1/policy/evaluate-transaction` — any language.
- **Go package**: `github.com/project-algebra/algebra/policy` — `go get` it
  into your own Go backend and call `policy.NewLocalProvider(rules).EvaluatePurchaseIntent(ctx, input)`
  directly, zero network hop. The REST endpoint is a thin wrapper over the
  exact same types.
- **MCP tool**: `policy.evaluate_transaction`, for an agent/chat/voice
  integration — same request/response shape as the REST endpoint, an
  `integrator_token` field in place of a bearer header.

A worked, runnable example: [examples/kite-wallet](../../examples/kite-wallet).

## The trust boundary

Algebra never sees your users' payment credentials or transaction history.
`spend_today_minor_units` is **you** telling Algebra what your user has
already spent — Algebra keeps no ledger for transactions it doesn't
process. `user_ref` and `payment_ref` are opaque identifiers you assign;
Algebra never resolves, stores, or interprets them. You send the inputs to
a decision, Algebra sends back the decision — nothing else crosses that
line.

## 1. Register as an integrator

```bash
curl -X POST http://localhost:8080/api/v1/integrators \
  -H 'Content-Type: application/json' \
  -d '{"name": "Kite"}'
# {"integrator_id":"integrator_...", "token":"alg_agent_..."}
```

The token is shown exactly once — store it like any other secret. Revoke
with `POST /api/v1/integrators/{id}/revoke`.

## 2. Evaluate a transaction

```bash
curl -X POST http://localhost:8080/api/v1/policy/evaluate-transaction \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "who": {"user_ref": "kite-user-8891"},
    "what": {"category": "retail"},
    "where": {"merchant": "Target", "international": false},
    "how_much": {"amount_minor_units": 8999, "currency": "USD", "spend_today_minor_units": 4200},
    "with_what": {"payment_ref": "kite-card-visa-4242"},
    "conditions": {
      "max_per_transaction_minor_units": 20000,
      "approval_threshold_minor_units": 5000,
      "blocked_categories": ["gambling"]
    }
  }'
# {"decision":"REQUIRE_APPROVAL","reason_codes":["AMOUNT_AT_OR_ABOVE_APPROVAL_THRESHOLD"],"policy_version":"external-inline-v1"}
```

`conditions` is **required** on every call — Algebra does not assume a
default budget policy on your behalf. Send `{}` if you genuinely want to
allow everything through (still evaluated, still audited — just no caps).

### Request fields

| Field | Required | Notes |
|---|---|---|
| `who.user_ref` | recommended | Your own opaque user identifier. |
| `what.category` | no | Free text — matched against `conditions.blocked_categories`. |
| `where.merchant` | yes | Free text — matched against `conditions.allowed_merchants` / `blocked_merchants`. |
| `where.international` | no | Triggers `conditions.international_requires_approval` if true. |
| `how_much.amount_minor_units` | yes | Integer, smallest currency unit (cents, paise, ...). |
| `how_much.currency` | yes | Any ISO 4217 code — Algebra doesn't convert or validate against a live rate. |
| `how_much.spend_today_minor_units` | no | Your own running total for this user today, same currency. Checked against `conditions.max_per_day_minor_units`. |
| `with_what.payment_ref` | no | Your own opaque payment-method reference. |
| `conditions` | **yes** | See below — your own `policy.Rules`. |

### `conditions` (your own `policy.Rules`)

| Field | Effect |
|---|---|
| `max_per_transaction_minor_units` | Hard cap — DENY above it, no approval can override it. |
| `max_per_day_minor_units` | Hard cap against `spend_today_minor_units` + this amount. |
| `approval_threshold_minor_units` | At or above this amount → REQUIRE_APPROVAL instead of ALLOW. |
| `allowed_merchants` / `blocked_merchants` | Allow-list (if non-empty, only these pass) / block-list (always wins). |
| `blocked_categories` | DENY if `what.category` matches. |
| `allowed_payment_profiles` | Allow-list over `with_what.payment_ref`. |
| `international_requires_approval` | REQUIRE_APPROVAL when `where.international` is true. |

### Response

```json
{"decision": "ALLOW", "reason_codes": ["MERCHANT_OK", "AMOUNT_OK", "..."], "policy_version": "external-inline-v1"}
```

`decision` is always exactly one of `ALLOW`, `DENY`, `REQUIRE_APPROVAL`.
What you do with a `REQUIRE_APPROVAL` — surface it in your own app, ask the
user, whatever your UI is (voice, chat, a push notification) — is entirely
yours; Algebra doesn't track or resume the decision for you in this build.

## What this isn't (yet)

This is a stateless decision function today: no persisted record of the
transaction, no callback when your app later resolves a REQUIRE_APPROVAL.
If you need Algebra to track that lifecycle for you, that's a real,
larger feature this build doesn't include — see the codebase's `Approval`
domain type for the shape Algebra's own commerce flow already uses
internally, which a v2 of this surface would extend to integrators.
