# Integrating Algebra (B2B Agentic Payments)

Algebra is agentic-payments infrastructure: a card app, wallet, fintech, or stablecoin app integrates Algebra so its own end users can grant AI agents controlled spending authority over payment sources the business already holds. Your app keeps its customers, wallet, balance, and card infrastructure — Algebra adds agent identity, policy, delegated authorization, approval, execution, and audit around it.

**Non-negotiable:** Algebra never becomes an issuer or a payment network, never custodies funds, never sees a raw PAN/CVV/private key. It supplies authority, not credentials.

This is the *platform* integration — a business owning end users, agents, policy, and payment intents. If you only need a one-off policy decision for a transaction you already own end to end (no Algebra-side state at all), the lighter-weight [`docs/legacy/INTEGRATING.md`](INTEGRATING.md) surface (`Integrator` + `POST /api/v1/policy/evaluate-transaction`) may be all you need instead. Both are real, both stay supported — see the "Which surface do I want?" note at the end.

## The shape

```
Your app (KAST-like card app, wallet, fintech, ...)
         │
         │ your backend holds a Tenant token
         ▼
┌───────────────────────────────┐
│      ALGEBRA CONTROL PLANE    │
│  Agent identity · Policy      │
│  AgenticPaymentIntent          │
│  Approval · Audit             │
└──────────────┬────────────────┘
               │ paymentprovider.Provider
               ▼
        Payment rail (DemoProvider today — see docs/legacy/PROVIDER_STATUS.md)
               │
               ▼
            Merchant
```

## 1. Register as a tenant

```bash
curl -X POST http://localhost:8080/api/v1/tenants \
  -H 'Content-Type: application/json' \
  -d '{"name": "DemoWallet Inc"}'
# {"tenant_id":"tenant_...", "token":"alg_agent_..."}
```

The token is shown exactly once — store it like any other secret, on your own backend, never in a client app. Revoke with `POST /api/v1/tenants/{id}/revoke`.

## 2. Set your policy

Your policy is the same deterministic engine (`policy.Rules`) Algebra's own commerce flow uses — persisted and versioned per tenant, optionally overridden per end user.

```bash
curl -X POST http://localhost:8080/api/v1/tenants/$TENANT_ID/policy-sets \
  -H "Authorization: Bearer $TENANT_TOKEN" -H 'Content-Type: application/json' \
  -d '{
    "conditions": {
      "max_per_transaction_minor_units": 1000000,
      "max_per_day_minor_units": 10000000,
      "approval_threshold_minor_units": 500000,
      "blocked_categories": ["gambling"]
    }
  }'
```

`conditions` is required, same as the lightweight surface — Algebra never assumes a default budget on your behalf. Read the active policy back with `GET /api/v1/tenants/{id}/policy-sets/latest`. Every `SetPolicy` call supersedes the previous version rather than overwriting it — old versions are kept so a decision made under one stays explainable.

## 3. Provision an end user and an agent

```bash
curl -X POST http://localhost:8080/api/v1/users \
  -H "Authorization: Bearer $TENANT_TOKEN" -H 'Content-Type: application/json' \
  -d '{"email": "customer@yourapp.example"}'
# {"user_id":"user_...", ...}  — this user is now scoped to your tenant

curl -X POST http://localhost:8080/api/v1/agents \
  -H 'Content-Type: application/json' \
  -d '{"user_id":"user_...", "client_id":"your-app", "name":"Shopping Agent",
       "permissions":["payments.create_intent","payments.execute","payments.request"]}'
# {"agent_id":"agent_...", "token":"alg_agent_..."}
```

`POST /api/v1/users` is the same endpoint the first-party reference console uses — presenting your tenant token is what scopes the new user to your tenant instead of leaving it unscoped. Agent tokens are the credential your AI agent (or your backend, acting on the agent's behalf) uses for every subsequent call.

## 4. Register a payment source

Same flow the reference console already uses — `POST /api/v1/payment-sources` (see the REST reference in `openapi/v1.yaml`) tokenizes a card via the configured `CardVaultProvider` and stores only an alias-scoped reference (`payment:personal`), never a PAN.

## 5. Create and evaluate an agentic payment intent

Unlike the commerce flow's create-then-discover-then-request-purchase sequence, an `AgenticPaymentIntent` already knows its merchant and amount — one call creates *and* evaluates it:

```bash
curl -X POST http://localhost:8080/api/v1/payment-intents \
  -H "Authorization: Bearer $AGENT_TOKEN" -H 'Content-Type: application/json' \
  -d '{
    "merchant": "Acme Electronics", "amount_minor_units": 50000, "currency": "USD",
    "payment_source_alias": "payment:personal", "purpose": "new laptop"
  }'
# {"payment_intent_id":"pay_...", "status":"AUTHORIZED"|"APPROVAL_REQUIRED"|"DENIED", "decision": {...}}
```

- **`AUTHORIZED`** — policy allowed it outright. Go straight to execute.
- **`APPROVAL_REQUIRED`** — a human must approve. There is deliberately **no** agent-callable approve tool, REST or MCP — an agent can request a payment, it can never grant its own approval. Your app's own UI shows the pending approval and calls `POST /api/v1/payment-intents/{id}/approve` (or `/reject`) with the end user's own authenticated session — never an agent token, which every approval endpoint rejects. (Algebra's first-party app uses its HttpOnly session cookie; see `docs/LOCAL_DEVELOPMENT.md` → Accounts and sessions.)
- **`DENIED`** — terminal. `decision.reason_codes` says why.

Full state machine and field reference: [`docs/legacy/AGENTIC_PAYMENT_INTENT.md`](AGENTIC_PAYMENT_INTENT.md).

## 6. Execute

```bash
curl -X POST http://localhost:8080/api/v1/payment-intents/$PI_ID/execute \
  -H "Authorization: Bearer $AGENT_TOKEN" -H 'Idempotency-Key: exec-1'
# {"status":"SUCCEEDED", "provider_transaction_id":"demo_txn_..."}
```

Only legal from `AUTHORIZED`. Internally this walks the full `paymentprovider.Provider` chain (register source → delegate → authenticate → scoped credential → execute) — see [`docs/legacy/PAYMENT_PROVIDER_INTERFACE.md`](PAYMENT_PROVIDER_INTERFACE.md). `Idempotency-Key` is required discipline, not optional — a retried call with the same key replays the exact first outcome rather than charging twice.

## 7. Webhooks

Register an endpoint to be notified asynchronously instead of polling:

```bash
curl -X POST http://localhost:8080/api/v1/tenants/$TENANT_ID/webhook-endpoints \
  -H "Authorization: Bearer $TENANT_TOKEN" -H 'Content-Type: application/json' \
  -d '{"url": "https://yourapp.example/webhooks/algebra"}'
# {"webhook_endpoint_id":"whep_...", "secret":"whsec_..."}  — shown exactly once
```

Every delivery is signed: `X-Algebra-Signature: sha256=<hex hmac>` over the raw body with your `whsec_...` secret (verify with the same construction `internal/app.VerifyHMACSignature` uses — HMAC-SHA256, constant-time compare), plus `X-Algebra-Event-ID` and `X-Algebra-Event-Type`. Events fired today: `payment_intent.created`, `payment_intent.policy_allowed`, `payment_intent.policy_denied`, `payment_intent.approval_required`, `payment_intent.succeeded`, `payment_intent.failed`, `payment_intent.authentication_required`, `payment_intent.provider_unavailable`. Delivery is fire-and-forget with a bounded retry (3 attempts, exponential backoff) and **no durable queue** in this build — an attempt that exhausts its retries while your endpoint is down is lost; poll `GET /api/v1/payment-intents/{id}/status` if you need a fallback.

## 8. List transactions and audit

`GET /api/v1/transactions` (tenant-authenticated) lists everything under your tenant. `GET /api/v1/payment-intents/{id}/audit` (agent-authenticated, `payments.request` permission) returns the full ordered event trail for one intent — the same append-only audit log backing the rest of Algebra.

## MCP

Everything above has an MCP-tool equivalent for an agent that speaks MCP directly rather than calling REST: `payments.create_intent`, `payments.get_intent`, `payments.execute`, `payments.get_status`. Same permission model (`agent_token` field in place of a bearer header), same absence of an approve/reject tool. See `internal/mcpserver/payment_intent_tools.go`.

## Which surface do I want?

| | `docs/legacy/INTEGRATING.md` (`Integrator`) | This doc (`Tenant`) |
|---|---|---|
| State Algebra keeps | None — one audit event per call | Full: users, agents, policy versions, payment intents, approvals, orders |
| You own | The entire transaction lifecycle yourself | Algebra owns intent/approval/execution lifecycle |
| Use it when | You just want a fast ALLOW/DENY/REQUIRE_APPROVAL decision, nothing else | You want Algebra to run the whole agentic-payment loop, including execution and webhooks |

## What this isn't (yet)

No TypeScript SDK (raw REST/MCP only — see `docs/legacy/BUILD_PLAN.md` for what's deferred to Phase 2). No real card-network/processor adapter wired up — every execution in this build runs through `providers/paymentdemo`, a deterministic mock; see `docs/legacy/PROVIDER_STATUS.md` for exactly which real rails are `NOT_IMPLEMENTED` vs. `PARTNER_REQUIRED` and why. No fictional reference "DemoWallet" app demonstrating this from a consumer's point of view yet — this doc and `examples/kite-wallet` (the lighter-weight surface) are the runnable references today.
