# AgenticPaymentIntent

`backend/internal/domain/paymentintent.AgenticPaymentIntent` is what a tenant's agent creates to request a single agentic payment — the core primitive of the B2B product. See `docs/legacy/B2B_INTEGRATION.md` for the integration guide and `ARCHITECTURE.md` for how this fits into the whole system.

## Why this isn't `PurchaseIntent`

`backend/internal/domain/intent.PurchaseIntent` (Algebra's original, commerce-flow primitive) is discovery-shaped: items, merchant search, quotes, a `SelectedQuoteID`. That's right for the consumer reference app's "find me headphones under ₹3,000" flow. A tenant's agent usually already knows the merchant and amount — "spend $73 at this specific merchant" — with no discovery step at all. Forcing that through a discovery-shaped state machine would be a worse fit than a purpose-built object.

What *is* shared is the pattern: `backend/internal/domain/intent/state.go` + `state_machine.go` is the most rigorously-enforced state machine in the codebase — a single transition-table allow-list, one enforcement function (`Transition`), one call path (`backend/internal/app`'s `transitionIntent`). `AgenticPaymentIntent`'s state machine (`backend/internal/domain/paymentintent/state.go` + `state_machine.go`) copies that pattern exactly, enforced through the parallel `transitionPaymentIntent` helper (`backend/internal/app/payment_intent_transition.go`).

## State machine

```mermaid
stateDiagram-v2
    [*] --> DRAFT
    DRAFT --> POLICY_EVALUATING
    DRAFT --> CANCELLED
    DRAFT --> EXPIRED
    POLICY_EVALUATING --> DENIED
    POLICY_EVALUATING --> APPROVAL_REQUIRED
    POLICY_EVALUATING --> AUTHORIZED
    POLICY_EVALUATING --> CANCELLED
    APPROVAL_REQUIRED --> AUTHORIZED
    APPROVAL_REQUIRED --> CANCELLED
    APPROVAL_REQUIRED --> EXPIRED
    APPROVAL_REQUIRED --> REVOKED
    AUTHORIZED --> CREDENTIAL_PREPARING
    AUTHORIZED --> CANCELLED
    AUTHORIZED --> REVOKED
    CREDENTIAL_PREPARING --> READY_TO_EXECUTE
    CREDENTIAL_PREPARING --> PROVIDER_UNAVAILABLE
    CREDENTIAL_PREPARING --> FAILED
    READY_TO_EXECUTE --> PROCESSING
    READY_TO_EXECUTE --> CANCELLED
    READY_TO_EXECUTE --> REVOKED
    PROCESSING --> SUCCEEDED
    PROCESSING --> FAILED
    PROCESSING --> AUTHENTICATION_REQUIRED
    PROCESSING --> MERCHANT_ACTION_REQUIRED
    PROCESSING --> PROVIDER_UNAVAILABLE
    AUTHENTICATION_REQUIRED --> PROCESSING
    AUTHENTICATION_REQUIRED --> FAILED
    AUTHENTICATION_REQUIRED --> EXPIRED
    MERCHANT_ACTION_REQUIRED --> PROCESSING
    MERCHANT_ACTION_REQUIRED --> FAILED
    MERCHANT_ACTION_REQUIRED --> CANCELLED
    PROVIDER_UNAVAILABLE --> PROCESSING
    PROVIDER_UNAVAILABLE --> FAILED
    PROVIDER_UNAVAILABLE --> CANCELLED
    DENIED --> [*]
    SUCCEEDED --> [*]
    FAILED --> [*]
    EXPIRED --> [*]
    REVOKED --> [*]
    CANCELLED --> [*]
```

`POLICY_EVALUATING -> AUTHORIZED` directly (skipping `APPROVAL_REQUIRED`) is policy `ALLOW` — auto-authorized, no human in the loop, same as `PurchaseIntent`'s `POLICY_CHECK -> APPROVED` direct edge. `DENIED` is terminal: nothing — not the agent, not a human, not this code — can override a policy denial after the fact; a new intent is required.

## Approval reuse, not reinvention

`backend/internal/domain/approval.Approval` (built for the commerce flow) is reused rather than duplicated. A nullable `agentic_payment_intent_id` column sits alongside the existing `intent_id` — exactly one is set per row. This keeps `CanonicalHash`/`Matches`/expiry, and — most importantly — the DB-level compare-and-swap on `APPROVED -> CONSUMED` (`backend/internal/platform/postgres/approval_repo.go`'s `MarkConsumed`), which is the actual double-execution guard in the system. For a payment intent, `ItemsHash` is computed over an empty item list (there's no cart) — still binding merchant + currency + payment-source alias; `QuoteID` is left empty.

There is deliberately no `approve`/`reject` tool exposed to an agent (REST or MCP) — approval is always a human action taken in the tenant's own console via `POST /api/v1/payment-intents/{id}/approve`, the same safety boundary `commerce.approve_purchase` already carries for the consumer flow.

## Field reference

| Field | Notes |
|---|---|
| `tenant_id` | The business that owns this — see `backend/internal/domain/tenant`. |
| `user_id` / `agent_id` | The tenant's end user this spend is on behalf of, and the requesting agent. |
| `purpose` | Free-text, agent-supplied — audit/UI context only, **never** read by policy. |
| `merchant` / `merchant_domain` | What policy evaluates against (`allowed_merchants`/`blocked_merchants`). |
| `category` / `international` | Same policy dimensions `PurchaseIntent.Constraints` already has. |
| `amount_minor_units` / `currency` / `tolerance_minor_units` | Integer minor units, no floats — same convention as every other money field in the codebase. |
| `payment_source_alias` | A reference into `backend/internal/domain/payment.PaymentSource` — never the credential itself. |
| `policy_version` | Which `PolicySet` version evaluated this — see `docs/legacy/B2B_INTEGRATION.md`'s policy section. |
| `provider_transaction_id` / `provider_status` / `final_amount_minor_units` / `final_currency` | Populated only after execution, from what the provider actually confirmed — never fabricated locally. |

No field on this struct can hold a raw payment credential — there is no such field to begin with, the same non-custodial guarantee `backend/internal/domain/payment.PaymentSource`'s package doc already states.
