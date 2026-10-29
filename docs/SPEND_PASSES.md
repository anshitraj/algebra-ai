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

## Task and agent P&L

`internal/app/pnl.go`. Every task (Job Pass, sub-tasks included) and every agent (Spend Pass, last 30 days) has its money
worked out from what the chain and the coordinator recorded:

- **Paid**: payments committed. **Wasted**: the part of that which bought an answer that wasn't usable.
- **Accepted** answers, and **cost per accepted answer** (paid ÷ accepted).
- **Saved by reuse**: what answers handed back by purchase memory would have cost again (each reuse records the task and
  the pass that asked).
- **Value** is never guessed: the agent states it when it opens a task (`value_minor` on `POST /api/v1/jobs`, or later
  `POST /api/v1/jobs/{id}/value`), or the person does in the pass's task list. With a value, **P&L = value − paid**; an
  agent's P&L adds up its tasks that have one.

`GET /api/v1/me/passes/{id}/pnl?days=30` is an agent's; each task's is in its view (`pnl`).

## Replay: a task under chaos

`internal/app/replay_chaos.go`, `POST /api/v1/me/jobs/{id}/replay`, "Replay under chaos" in the task list. A finished
task's paid purchases run again, on paper, through the router's dry run under the task's own Spend Pass, with one
misbehaving seller added per scenario:

| Scenario | What the seller does | What normally answers it |
|---|---|---|
| Overcharge | lists the usual price, asks four times as much | `price_above_listing` |
| Honeypot | asks 25 USDC for the same work | the intent's ceiling, the outlier guard |
| Web lure | an endpoint found on the open web at half price | the pass's web-found rule (asks first by default) |
| Stranger | a seller never paid, at the same price | the pass's new-provider rule |
| Seller down | the seller the task paid doesn't answer | falling back to the next, or stopping with nothing paid |
| Loop | the same thing asked again and again | purchase memory, then the repeat-purchase rule |

The adversaries are never contacted (their answers are written into the dry run) and the task's real sellers are priced
from their listings, so nothing leaves Algebra and nothing is paid. Each finding says whether it was caught, waited for
the person, was ranked below a better offer, or would have been paid, with the rule that decided.

### Test this pass: the same scenarios before an agent has any task

`ExecutionService.ReplayPreset`, `POST /api/v1/me/passes/{id}/replay`, "Test this pass" in an open pass card. A pass with no
task has nothing to replay, so three kinds of work agents usually buy stand in for a task's purchases: `token.price`,
`solana.token-risk` and `web.search`. For each:

- the sellers are the ones the router would see now, kept to the networks the pass can pay on (a Testnet pass: the Testnet
  networks whose rail is ready; a demo account: the sandbox) and to what is priced in USDC;
- the cheapest one that lists a price stands in for "the seller the agent paid", and its price is the usual price the
  scenarios scale from (overcharge is four times it, the web lure half of it);
- the ask has the widest budget the pass lets one purchase have (its per-purchase limit, else its whole budget), so a pass
  that holds against it holds against a smaller ask, and a missing limit shows as `would_pay`;
- a kind of work nobody sells on those networks is listed in `skipped` with the reason instead of being tested.

It says so instead of returning a report where everything was refused for one reason: a pass that shops (INR), a frozen,
revoked or expired pass, and a Mainnet pass while Mainnet payments are off are refused with what to do. Same pass, same
sellers, same report: a tie in price goes to the first seller by name. `TestReplayPreset_*`,
`TestReplayPresetOverHTTP_APassIsTestedWithoutPayingAnyone`.

## Web tools with your own API key

`backend/providers/keyed`, `internal/app/tool_keys.go`, "Web tools" in Account. A person saves their own key for a vendor and
that vendor's tools become one more seller their agents can route to, priced and ranked beside the paid ones:

| Vendor | Tools | Class |
|---|---|---|
| TinyFish | Search · Fetch · Agent | `web.search` · `web.scrape` · `web.agent` |
| Exa | Search | `web.search` |
| Firecrawl | Scrape | `web.scrape` |
| Tavily | Search | `web.search` |

- **The vendor bills the person**, so Algebra moves no money. The call still goes through the coordinator: the Spend Pass's
  budget, its rules and its ask-first gate apply, the quote is the vendor's list price, and the call settles on the `keyed`
  rail for **what the vendor reported charging** (TinyFish Agent: steps × 1.5 cents; Exa: its `costDollars`; Firecrawl and
  Tavily: their list price). A free call commits nothing; a call the vendor refused or failed is not billed and not counted.
  The receipt says "Your own API key" and names the vendor's reference, never a chain transaction.
- **The key** is sealed under a key derived from the master key (`algebra:tool-keys:v1`), bound to its owner and vendor, never
  returned (only its last four characters are shown), never logged, never put in a URL, and sent only to the vendor's own
  address: the runner looks the tool up by provider and class and refuses a quote that names any other endpoint. It is read
  only for the owner of the Spend Pass making the call. Erasing an account deletes its keys. A demo account can't save one.
- **Whose tools**: `CandidateResolver.ResolveFor` adds a person's keyed tools to their own requests only, first in the order
  (they need no network to be priced). Naming `keyed:exa` in `providers` selects it; endpoints an agent supplies itself are
  never keyed.
- Environments: keyed calls are exempt from the network limit (no chain is used), so they work in Testnet and Mainnet alike.

`GET /api/v1/me/tool-keys` lists the vendors and which keys are saved; `PUT /api/v1/me/tool-keys/{vendor}` with `{"key": "…"}`
saves or replaces; `DELETE` removes.

**The operator's own key.** An operator can keep a vendor key in the server's environment instead (`TINY_FISH_API`, `EXA_API`,
`FIRECRAWL_API`, `TAVILY_API`; see `backend/.env.example`). That key is the operator's own account with the vendor, so every call
made with it is billed there, and it is offered only to the accounts at the addresses in `ALGEBRA_OPERATOR_EMAILS`, and to
nobody else (`internal/app/operator_keys.go`). An account counts only when it is a first-party account (not a B2B tenant's
person), its person has proved the address (a sign-in code or a verified provider), it is not a demo, and it has **no
password**: proving an address now clears a password set before it (`AccountService.claimAddress`), but an account verified
before that change may still carry one that is somebody else's, so an operator account is one with none. The set is looked up
from the addresses at most every 30 seconds, so a first sign-in counts within that.

- A key the person saves themselves always wins, and removing it hands back to the server's. The server's key can't be saved,
  replaced or removed from the app, and is never returned: the listing only says `server_key: true` and Account → Web tools
  shows "Using the server's key".
- It lives in the process, read from the environment at start. Nothing about it is stored in the database, and it goes only to the
  vendor's own address, checked by the runner like any saved key. Keys set with no address listed: no account can use them, and the
  API logs a warning at startup. A key that is not the shape of one stops the start, naming the vendor and never the key.

**Checked live.** `providers/keyed/live_test.go` runs the real adapters against the real vendors, only when a key is in the
test process's environment (`KEYED_LIVE_TINYFISH_KEY`, `_EXA_`, `_FIRECRAWL_`, `_TAVILY_`; an Agent run costs credits and
also needs `KEYED_LIVE_AGENT=1`): `KEYED_LIVE_TINYFISH_KEY=… go test ./providers/keyed -run Live -v -count=1`. TinyFish
Search (10 results), Fetch (a page as markdown) and one small Agent run (4 steps in 23 s, charged 4 x 1.5 cents = 0.06 USD)
passed against the live API on 2026-10-29. TinyFish's answer to an Agent run reports its steps and not a dollar amount, so
the charge is the steps at the pay-as-you-go step price: a plan with cheaper credits is billed less than Algebra records.
The Exa, Firecrawl and Tavily adapters are so far tested against fake vendors only.

`internal/platform/postgres/keyed_live_test.go` runs the whole path (router, coordinator, `keyed` rail and runner, Postgres)
against the real TinyFish, with the key only in the test process's environment and nothing saved: Search and Fetch settle
at 0 and count as accepted answers in the agent's P&L; an Agent run is refused as over budget under a 0.30 ceiling and, with
a higher one, waits for the person. It never runs an Agent task (the test's HTTP client refuses that host), because one
costs credits.

A free call moves no money, so it does not make a vendor a known provider: the new-provider gate counts only commitments
with a hold above zero. The first call that costs more than a never-paid provider may take (0.05 by default) is held for a
yes however many free ones came before it, and an agent needs a ceiling of at least 0.30 USDC to ask for an Agent run,
which is quoted at about twenty steps.

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
