# Economic coordination: implementation map and design

Algebra's job here: **a timeout is not proof that money moved or didn't.**
Many agents, retries and fallbacks can chase one outcome. Algebra lets at
most one of them hold commit authority at a time. It freezes the outcome
when it's ambiguous, reconciles from evidence, and signs what actually
happened.

This file records the Phase 0 audit (what exists and what changes) and the
design that later phases implement.

## Phase 0: repository audit

| Existing component | Responsibility today | Reuse | Change |
|---|---|---|---|
| `internal/domain/spendpass`, `SpendPassService` | Per-agent budget, per-purchase cap, ask-above line, allowed categories and merchants, expiry, revocation. `Combine` makes DENY > REQUIRE_APPROVAL > ALLOW. | Yes — it is the authority | Adds a `digital_services` category. The budget counts economic holds and commitments as well as orders. |
| `policy` (public package) | Deterministic rule engine | Yes | None |
| `internal/domain/receipt` | Ed25519 compact JWS, HKDF-derived key, public JWKS, verify | Yes — same key and JWKS | Adds `IntentReceipt` (typ `algebra-intent-receipt+jwt`) |
| `internal/domain/audit` | Append-only events with a secret deny-list | Pattern reused | Lifecycle telemetry goes to its own append-only `economic_events` table, with the same secret rules |
| `internal/app/idempotency.go` | HTTP replay per (key, scope) | Kept for requests | Economic idempotency is a separate thing — it covers one *outcome* across agents, providers and rails |
| `OrderService` + Redis `Locker` | Commerce execution under a best-effort lock and CAS | Kept for commerce | Reservations use Postgres row locks and partial unique indexes, not a cache lock |
| `internal/domain/paymentintent` | B2B "pay merchant X amount Y" state machine | Kept | It's shaped around one payment request, not one outcome with many executors. Economic intents are a new entity rather than a rename |
| `internal/domain/intent` (PurchaseIntent) | Consumer shopping flow | Kept (reference app) | None |
| `internal/mcpserver`, REST `/api/v1` | Agent surfaces with bearer tokens | Yes | Adds `economic.*` tools and `/api/v1/economic-intents` |
| `docs/SOLANA_DEVNET_USDC.md` | Devnet design only, no code | Partly | The real rail is x402 exact on Solana mainnet, behind the `Rail` interface; devnet is never labelled real |
| Ecommerce connectors, deals, plugins, scam shield | Reference shopping app | Kept untouched | They become a future execution rail under the same model |

Demo-only paths stay labelled as demo: `demo_checkout`, `mock`, and the
sandbox rail used by tests and local runs.

## Model

```
INTENT → AUTHORITY → RESERVATION → EXECUTION → OBSERVATION / RECONCILIATION → FINAL STATE
```

**Economic intent** (`internal/domain/econ`): one outcome a principal wants.
Its identity is deterministic:

```
effect_key = capability : sha256(canonical_input)[:24] : window : quantity
```

`(principal, effect_key)` is unique. Creating the same outcome twice returns
the same intent, whichever agent asks. Algebra never uses an LLM to decide
whether two outcomes are the same.

Three states are kept separately, never folded into one status:

| Axis | Values |
|---|---|
| Lifecycle | `AWAITING_APPROVAL` `OPEN` `RESERVED` `EXECUTING` `UNKNOWN` `RECONCILING` `COMMITTED` `CANCELLED` `EXPIRED` |
| Commitment (money) | `NONE` `RESERVED` `SETTLED` `UNKNOWN` `REVERSED` |
| Fulfilment (result) | `NONE` `FULFILLED` `UNKNOWN` `NOT_FULFILLED` |

**Reservation**: an executor's exclusive right to attempt the commitment.

```
RESERVED ──begin──▶ EXECUTING ──evidence──▶ COMMITTED
   │                   │
   │ release / lease   ├─ proven no commitment ──▶ RELEASED (intent back to OPEN)
   ▼                   └─ timeout / no evidence ─▶ UNKNOWN ─▶ RECONCILING ─▶ COMMITTED | RELEASED
RELEASED / EXPIRED                                               (unresolved: stays RECONCILING, blocked)
```

Invariants, enforced in Postgres rather than in application memory:

1. **At most one live reservation per intent.** A partial unique index
   covers `RESERVED`, `EXECUTING`, `UNKNOWN` and `RECONCILING`. The
   reservation transaction takes `SELECT … FOR UPDATE` on the intent row.
2. **At most one committed reservation per intent** (a partial unique index).
3. **Pass budgets can't be overspent by concurrent reservations.** The
   executor's pass row is locked, and live holds count as spent.
4. **UNKNOWN is not FAILED.** An `EXECUTING` reservation that times out, or
   whose executor stops reporting, moves to `UNKNOWN`, never back to `OPEN`.
   No new reservation is granted until reconciliation proves either a
   commitment or its absence.
5. **Agents can't self-certify.** `COMMITTED` needs payment evidence that the
   rail verifies. "Nothing happened" is only accepted without proof if
   Algebra never released payment authority.
6. **Before payment authority is released** (`begin`), the pass is
   re-evaluated: revoked, expired, over budget.

**Authority**: the executor's own Spend Pass, intersected with the intent's
budget. The pass decides what this agent may spend. The intent decides what
the money is for and its ceiling. The hold (the intent maximum, or the quote
when lower) counts against the executor's pass until it settles or is
released. Only a human session can approve an intent that needs approval.

**Rails** (`Rail` interface): `Settlement(evidence)` returns one of:
- `SETTLED` (with amount and transaction)
- `NOT_SETTLED` (provably final — for example, a Solana transaction whose
  blockhash has expired and which never landed)
- `PENDING`
- `UNKNOWN`

Settlement semantics are recorded per attempt: `PREPAID_EXACT`,
`METERED_CAPTURE`, `DEFERRED_CHANNEL` or `ESCROWED_CONDITIONAL`. The
`sandbox` rail is for tests and local runs, and receipts mark it `test`.

**Reconciliation**: for an `UNKNOWN` attempt, ask the rail about settlement,
then ask the provider about the operation (idempotency key, operation ID,
status endpoint, result replay). Report exactly what's proven, for example
"payment confirmed, result unknown". Nothing is invented.

**Intent Receipt v2**: signed with the same key and JWKS as spend receipts.
It binds principal (pseudonym), intent and intent hash, authority (pass) and
policy version, reservation (executor and attempt), provider and quote,
execution (protocol, scheme, request and result hashes), settlement
(network, asset, amount, transaction), and coordination (duplicate attempts
blocked, whether reconciliation was needed). It proves what Algebra
observed, not that the provider's data is true.

**Telemetry**: `economic_events` is an append-only table covering
`intent.*`, `authority.*`, `reservation.*`, `execution.*`, `payment.*`,
`reconciliation.*` and `receipt.*`. It never holds tokens, keys or result
payloads.

## Phases

| Phase | What |
|---|---|
| 0 | This audit |
| 1–4 | Intent, reservation state machine, authority, concurrency tests |
| 5 | x402 exact + Solana mainnet USDC rail + a real provider (needs a small project-funded wallet) |
| 6 | Reconciliation engine and sweeper |
| 7 | Intent Receipt v2 |
| 8 | Telemetry and `cmd/verify-intent` |
| 9+ | More providers, MCP and SDK polish, dashboard, external users |
