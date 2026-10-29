# Spend Passes, the spend firewall and signed receipts

Algebra lets an AI agent spend a person's USDC **without holding it**. The person gives the agent a *Spend Pass* instead of a key or a card; the agent's whole authority is
that pass, and every outcome it buys comes with a *signed receipt* anyone can verify.

- **A person** gives Claude, ChatGPT or any bot a pass ("$5 a week for APIs, ask me above $0.50, only these providers, 7 days") and revokes it, or freezes everything, with one click.
- **A business running agents** issues a pass per agent, team or customer: budgets, allow-lists and approvals come built in, plus a receipt trail for audit.
- **A provider or auditor** verifies a receipt to know that the person really authorized that exact request, and what the chain showed was paid.

## A pass

Create one in the console (**Spend passes**) or with `POST /api/v1/me/passes` (session only). The response carries the agent's token, **shown once**; Algebra keeps only its hash.
The agent authenticates with `Authorization: Bearer <token>`.

| Field | Meaning |
|---|---|
| `currency` | `USDC`. Amounts are micro-USDC (1,000,000 = 1 USDC). |
| `budget_minor_units`, `budget_period` | The budget for the window: `total`, rolling `week` or rolling `month`. Live holds count as spent until they settle or are released. |
| `max_per_purchase_minor_units` | The most one call may cost. |
| `approve_above_minor_units` | At or above this the person must approve first (0 = every call). |
| `allowed_merchants` | Allowed providers (empty = any), by provider ID such as `circle:birdeye` or `cdp:api-exa-ai`. |
| `allowed_categories` | `digital_services` is what API calls are. |
| `expires_at` | When it stops working. |

A pass never loosens anything: every call must clear the person's own guardrails and the pass, and the stricter answer wins (`DENY` > `REQUIRE_APPROVAL` > `ALLOW`, with every reason kept).
It is checked when the agent asks, and **again right before the wallet signs**, so a pass revoked or frozen after approval, or two calls approved side by side, can't spend past it. Spend comes from
the coordinator's own reservations and commitments, never from anything the agent reports.

| Rule | When broken | Reason code |
|---|---|---|
| Revoked / expired | Deny | `PASS_REVOKED` / `PASS_EXPIRED` |
| Provider outside `allowed_merchants` | Deny | `PASS_MERCHANT_NOT_ALLOWED` |
| Over `max_per_purchase` | Deny | `PASS_PER_PURCHASE_LIMIT` |
| Spent + this > budget for the window | Deny | `PASS_BUDGET_EXCEEDED` |
| At or above `approve_above_minor_units` | Ask the person | `PASS_APPROVAL_REQUIRED` |

## The spend firewall: controls on how an agent spends

Beyond *how much*, a pass has controls on *how* (`PUT /api/v1/me/passes/{id}/controls`; the console's **Spend firewall** page):

| Control | Default | What it does |
|---|---|---|
| `max_calls_per_minute` | 60 | A looping agent is stopped by this long before it reaches its budget (`PASS_RATE_LIMITED`). |
| `max_calls_per_provider_per_minute` | 20 | The same, per provider (`PASS_PROVIDER_RATE_LIMITED`). |
| `new_providers` | `cap` | What to do about a provider the person has never paid. `allow`: paid like any other. `cap`: paid, but no more than `new_provider_cap_minor_units` per call (default $0.05) until it has been paid once (`PASS_NEW_PROVIDER_OVER_CAP`). `approve`: waits for the person (`PASS_NEW_PROVIDER_NEEDS_APPROVAL`). A request over the cap moves its intent `OPEN → AWAITING_APPROVAL`, so the person's approval is the same human-only step as for the approval line. |
| Freeze (`POST …/freeze`) | off | Releases nothing, including a payment about to be made. |
| Kill switch (`POST /api/v1/me/killswitch`) | off | Freezes every pass of the person at once. |

The catalogs list thousands of endpoints and anyone can publish one, so the first payment to a stranger is where an agent is most easily misled: hence the default cap.

**Dry run.** `POST /api/v1/policy/simulate` (MCP `algebra_simulate`) takes what `POST /execute` takes, plus `live_quotes`, and answers whether it would be allowed (`ALLOW`, `REQUIRE_APPROVAL`, `DENY`), why,
the plan the router would follow, and what each provider would meet (the pass's remaining budget, the controls, the guards). It creates no intent, reserves nothing and pays nothing; with `live_quotes` the only thing
that leaves Algebra is each provider's free unpaid `402`.

## Guards that hold for everyone

Independently of any pass, Algebra refuses providers that are down (`provider_down`), that ask more than they list (`price_above_listing`, more than 5% over), or that ask many times what the same work usually
costs (`price_outlier`, more than 10× the class median and over $0.05; $1,000 a call is never paid). The rails have hard per-payment ceilings of their own (1 USDC for x402, 5 USDC for a swap by default) that no
pass can raise.

## Job Passes: a task's budget inside a pass

The hierarchy is **Vault → Spend Pass → Job Pass**. A Job Pass is a temporary budget for one task, set aside from a
Spend Pass and shared by every agent that works on the task (code: `internal/domain/jobpass`,
`internal/app/job_passes.go`, migration `0031_job_passes.sql`).

- **Set aside at once, or not at all.** Opening a job counts its whole budget against the pass immediately, under the
  same row lock every payment of the pass takes. When the pass hasn't that much left the job is refused
  (`JOB_PARENT_SHORT`, with `left_minor`), so a task never starts on money another task may take. The chat opens one
  for every turn that pays: the most the chosen cards could cost, together. Two cards that each fit but not together
  pay nothing, instead of half the task.
- **Shared by sub-agents.** The agent that opens a job gets the job's token once (`ajob_…`, stored hashed). A sub-agent
  holding it can pay under the job, open a smaller job inside it, and read or close what it reaches; nothing else. It
  can't read the pass, other jobs, the account, or approve anything. Three sub-agents on a 3 USDC job can spend 3 USDC
  together, not 9.
- **Parent-child limits.** A sub-job comes out of what its parent has left, at most three levels deep, and never
  outlives its parent or the pass. While a sub-job is open its whole budget counts in the parent; once it ends, only
  what it paid or still holds does.
- **Every rule of the pass still applies** to each payment: per-call cap, providers, ask-me line, new-provider and
  web-found rules, calls per minute, the kill switch. A job narrows a pass, never widens it.
- **Ends three ways**, each giving back what it didn't use at once: closed when the task is done (the agent, a
  sub-agent for its own job, or the chat), stopped by the person (`Stop this task` in the pass, which stops every job
  beneath it too), or expired (an hour by default, a day at most). An attempt already under way keeps its hold until
  it settles. A job stopped between reserving and paying releases nothing: `Begin` and the payment authority both
  check the job again.
- **Ledger only.** A job is an allocation in Algebra's ledger, never separate money, a wallet or a deposit. On Testnet
  the credits pay; with a vault allowance the vault pays; the job decides only whether a payment may happen.

API: `POST /api/v1/jobs` (agent token, or a job token for a sub-job) with `label`, `budget_minor`,
`expires_in_seconds`, `parent_id`; `GET /api/v1/jobs/{id}`; `POST /api/v1/jobs/{id}/close`. `POST /api/v1/execute`
takes `job_id`, or pays under the job of the job token it is called with. The console: `GET|POST /api/v1/me/jobs`,
`GET /api/v1/me/jobs/{id}`, `POST /api/v1/me/jobs/{id}/close|revoke`. The pass's view adds
`set_aside_minor_units`: what running jobs hold and haven't used.

## Connecting an agent

- **MCP** at `/mcp`: send the token as an HTTP header, so it never passes through the model: see [MCP.md](MCP.md). `algebra_spend_pass` reads the pass; `algebra_execute` spends under it.
- **REST**: `POST /api/v1/execute`; `GET /api/v1/pass` returns the pass's limits and remaining budget. Details: [EXECUTION.md](EXECUTION.md) and [backend/openapi/execution.yaml](../backend/openapi/execution.yaml).
- An agent can read its pass and spend under it. It cannot approve, change a limit, create a pass or touch a wallet: those are human-only and accept only a session cookie.

## Receipts

Every committed outcome gets an **Intent Receipt**: a compact JWS, `alg: EdDSA` (Ed25519), `typ: algebra-intent-receipt+jwt`, signed with a key derived from the deployment's master key (HKDF, so there is
nothing new to store, and old receipts keep verifying). Claims, abbreviated:

```jsonc
{
  "iss": "https://app.example", "jti": "rcpt_…", "iat": 1790647501,
  "sub": "person_c75e20522639a917f8f77db4",                  // a stable pseudonym: links one person's receipts without saying who
  "intent":      { "id": "eint_…", "hash": "sha256:…", "capability": "token.price", "effect_key": "…", "budget_max": { "minor_units": 10000, "currency": "USDC" } },
  "authority":   { "spend_pass_id": "pass_…", "method": "policy" },        // "human" when the person approved this request
  "reservation": { "id": "rsv_…", "executor": { "id": "agent_…" }, "attempt": 1 },
  "provider":    { "id": "circle:birdeye", "quote": { "minor_units": 3000, "currency": "USDC" }, "settlement_semantics": "PREPAID_EXACT" },
  "routing":     { "mode": "CHEAPEST", "...": "who was priced, ranked, and why" },
  "execution":   { "protocol": "x402", "scheme": "exact", "request_hash": "sha256:…", "result_hash": "sha256:…", "status": "fulfilled" },
  "settlement":  { "rail": "x402-solana", "network": "solana", "asset": "EPjFWd…", "amount": { "minor_units": 3000 }, "transaction": "5Gx…" },   // what the chain showed
  "coordination": { "attempts": 1, "duplicate_commit_attempts_blocked": 0, "reconciliation_required": false },
  "final_state": { "lifecycle": "COMMITTED", "commitment": "SETTLED", "fulfillment": "FULFILLED" },
  "test": false                                               // true on the sandbox: genuine receipt, no real money
}
```

It proves what Algebra observed, not that the provider's answer is true. **Verify** with the public key set at `/.well-known/jwks.json` (standard JWS libraries handle Ed25519), with
`POST /api/v1/receipts/verify {"receipt": "…"}` (which also says whether Algebra has it on record and whether the pass has since been revoked), at `/verify` in the web app, or offline with
`go run ./cmd/verify-intent`. No account is needed. The original shopping flow's order receipts (`algebra-spend-receipt+jwt`) verify the same way and are described in [legacy/](legacy/README.md).
