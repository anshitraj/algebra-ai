# The developer platform

What a business uses to put Algebra behind its own application: an
organization, projects, API keys per environment, and the Provider Intelligence
registry the router consults. Everything here is built and tested unless a line
says otherwise.

## Organizations, projects and environments

| | |
|---|---|
| Organization | The customer. Created on first sign-in (a personal workspace) or with `POST /api/v1/orgs`. Members have a role: `owner`, `admin`, `developer`, `viewer`. Owners and admins manage; everyone reads. |
| Project | One application: its keys, Spend Passes, executions and audit events. A project may set a default routing strategy (`cheapest`, `fastest`, `balanced`) used when a request names none. |
| Environment | Where money may move: `sandbox` (simulated rail, no chain), `devnet` (Solana devnet, test tokens), `mainnet` (real money). Each maps to exactly one payment network. |

Code: [`backend/internal/domain/tenancy`](../backend/internal/domain/tenancy/tenancy.go),
[`backend/internal/app/tenancy_service.go`](../backend/internal/app/tenancy_service.go),
[`backend/migrations/0021_platform_tenancy.sql`](../backend/migrations/0021_platform_tenancy.sql).
Console: **Projects & API keys** (`/console/projects`).

## API keys

A key authenticates an application inside one project and one environment. It
never authorises spending on its own: every paid request made with a key is
executed through a **Spend Pass** of the same project and environment, and the
pass's limits decide. Two authorities, kept apart.

- Format: `alg_sk_<environment>_<43 characters>`, 32 bytes from the system
  CSPRNG. The secret is shown once; only its SHA-256 hash is stored.
- Scopes: `read` (discover, read executions and receipts), `simulate` (dry
  runs), `execute` (paid requests, still only inside a pass).
- Revoke and rotate from the console or the API. A rotated key names the key
  it replaced; the old one stops at once.
- `last_used_at` is recorded (at most once a minute), and every create, rotate
  and revoke is written to `platform_events` with the key's ID and prefix,
  never its secret.
- Rate limited per key, like every other bearer token (120 requests a minute
  when Redis is configured).

```bash
curl -s $ALGEBRA/api/v1/execute \
  -H "Authorization: Bearer $ALGEBRA_API_KEY" -H "Content-Type: application/json" \
  -d '{"capability":"token.price","input":{"mint":"So11111111111111111111111111111111111111112"},
       "budget_max_minor":10000,"provider_policy":{"strategy":"cheapest"}}'
```

Add `"spend_pass_id"` to choose a pass; without it the newest active pass of
the key's project and environment is used. With no such pass the answer is
`409`, naming what to create.

What the tests prove (`backend/test/e2e/tenancy_http_test.go`, against real
Postgres over HTTP):

- A sandbox key runs in its own project and the sandbox only; asking for
  devnet or mainnet with it is refused.
- A key without `execute` cannot pay; one without a pass gets the 409.
- Nothing crosses between people: another account's organization, project,
  keys, passes and events all answer 403 or 404.
- No mainnet payment is signed unless `MAINNET_EXECUTION=on`
  (`backend/internal/app/economic_environment_test.go`); the refusal code is
  `MAINNET_EXECUTION_DISABLED`.

| Route (signed-in session only) | |
|---|---|
| `GET/POST /api/v1/orgs` | list, create |
| `GET/POST /api/v1/orgs/{id}/projects` | list, create |
| `PATCH /api/v1/projects/{id}` | rename, default strategy |
| `GET/POST /api/v1/projects/{id}/keys` | list (never secrets), issue |
| `POST /api/v1/keys/{id}/rotate`, `/revoke` | |
| `GET /api/v1/projects/{id}/events` | the project's audit trail |
| `GET /api/v1/projects/{id}/passes?environment=` | its Spend Passes |

## Provider Intelligence

A registry of every paid endpoint the four catalogs list, what changed about
each between refreshes, and how far Algebra trusts it.
Code: [`backend/internal/domain/intel`](../backend/internal/domain/intel/intel.go),
[`backend/internal/app/intel_service.go`](../backend/internal/app/intel_service.go),
[`backend/migrations/0022_provider_intelligence.sql`](../backend/migrations/0022_provider_intelligence.sql).
Console: **Provider intelligence** (`/console/intelligence`).

### Refresh

Every six hours on the hour, UTC (`0 */6 * * *`), outside any user request.
The schedule is a row in `scheduled_jobs`; an instance takes the job's lease
with one conditional `UPDATE`, so with several instances exactly one refreshes.
The next run time and each catalog's checkpoint are stored, so a restart
neither loses nor repeats a run. Each catalog has a timeout (4 minutes), three
tries with backoff, and its own result: when one is down, its last good listing
is kept, the source is marked stale and the error is recorded
(`catalog_sync_runs`). `POST /api/v1/intel/refresh` runs it on demand.

Sources are the existing adapters: Pay.sh, Circle's Agent Marketplace
(`api.circle.com/v2/x402/discovery/resources`), PayAI and Coinbase's x402
Bazaar, each through its documented API. Nothing is scraped.

### What is recorded

An endpoint's identity is its provider ID, URL and network together, hashed,
so the same service on two networks is two endpoints and a renamed listing is
still the same one. For each: source, method, capability, protocol, network,
asset, listed price, the payee named, the payee pinned, whether Algebra can
call it, when it was first and last seen, last changed, quoted, tested and
verified. Changes go to `provider_changes` and are never deleted: `added`,
`removed`, `restored`, `price_changed`, `payee_changed`, `callable_changed`,
`unreachable`, `trust_changed`. A removed endpoint keeps its row and history.

### Trust

Trust comes from evidence Algebra collected, never from what a catalog or a
provider says about itself.

| State | Means |
|---|---|
| `unverified` | Listed. Nothing more is known. Every new listing starts here. |
| `verified` | Algebra paid it, it delivered, and it asked to be paid at the payee pinned for it (`PAID_DELIVERY_MATCHED`), or a person marked it. A fact about past calls, not a guarantee. |
| `blocked` | It named a different payee than the pinned one (`PAYEE_CHANGED`), or a person blocked it. Nothing is paid to it until a person unblocks it. |

The payee is pinned the first time Algebra sees one. A different payee later,
in a listing or in a live quote, is refused at payment time and blocks the
endpoint; its earlier verification is void. A listed price that more than
doubles is marked suspicious for a person to look at (the live price is still
checked against the listing on every call). Trust decisions by a person need a
signed-in session (never an API key or an agent token; in production the
operator token as well) and are written to the change history with who decided.

The console shows five separate facts per endpoint, because a listing is not a
quote, a quote is not a test and a test is not verification:

| Listed | Quoted | Tested | Verified | Executable |
|---|---|---|---|---|
| a catalog lists it now | an unpaid request returned payment terms | Algebra paid it and it delivered | tested, to the pinned payee, not blocked since | the router can pay and call it today |

"Executable" counts mainnet endpoints too. Mainnet payments are off by default
(`MAINNET_EXECUTION`), and the page says so.

Tests: `backend/internal/domain/intel/intel_test.go` (merge, pinning, payee
change, price change, removal and restore, six-hour slots, stable IDs) and
`backend/test/e2e/provider_trust_http_test.go` (a sandbox seller that changes
its payee is refused with `PAYEE_CHANGED`, then blocked, over real HTTP).

| Route | |
|---|---|
| `GET /api/v1/intel/status` | the job, recent runs, each catalog's freshness, counts |
| `GET /api/v1/intel/endpoints` | the registry: `source`, `network`, `protocol`, `trust`, `level`, `capability`, `q`, `sort`, `limit`, `offset` |
| `GET /api/v1/intel/endpoints/{id}` | one endpoint and its history |
| `GET /api/v1/intel/changes` | what changed, newest first: `kind`, `suspicious`, `endpoint` |
| `POST /api/v1/intel/refresh` | refresh now (a person) |
| `PUT /api/v1/intel/endpoints/{id}/trust` | `{"trust","note"}` (a person) |

### Parallel (optional discovery): not built

The brief asks for an optional Parallel Search/Monitor integration to find
newly announced providers. It is not built. What exists for open-web discovery
is `POST /api/v1/discover/web` (Gemini search, each find probed for free and
returned as an unverified candidate). If Parallel is added it must follow the
same rule: a search result may add an *unverified* candidate, and can never
verify a provider, change a pinned payee, lift a block or cause a payment.

## Plans and the trial

| Plan | A month | Projects | On-chain executions a month |
|---|---|---|---|
| Free | $0 | 1 | 100 |
| Builder | $19 | 3 | 2,000 |
| Startup | $59 | 10 | 10,000 |
| Business | $199 | 50 | 100,000 |
| Enterprise | custom | no limit | no limit |

The catalog is code ([`backend/internal/domain/tenancy/plans.go`](../backend/internal/domain/tenancy/plans.go)),
served at `GET /api/v1/plans` and shown at `/pricing`.

What works today:

- **The project limit is enforced** when a project is created (409, naming the
  limit).
- **The seven-day Startup trial**: `POST /api/v1/orgs/{id}/trial`, by an owner
  or admin, once per organization (the condition is in the `UPDATE`, so two
  requests cannot both start it). No card. The plan in force is worked out
  when it is read, so when the trial ends the organization is on its own plan
  again with nothing to undo, and nothing it made is deleted.
- **Usage is metered** per project for the calendar month:
  `GET /api/v1/projects/{id}/usage` returns requests by state and, per
  network, committed executions and what providers were paid. Executions on
  devnet and mainnet count against the plan's allowance; sandbox runs never do.

What does not:

- **No payment processor is connected for these plans.** `GET /api/v1/plans`
  answers `"billing_live": false` and the page says paid plans cannot be
  bought online. (The Razorpay code in `providers/razorpay` bills the original
  product's two plans and is not wired to these.)
- **Nothing is refused at the execution allowance.** It is counted and shown.
- **Nobody can be invited into an organization**, so the member limits in the
  catalog are not offered on the pricing page.

Provider charges and network fees are paid from the customer's own pass in
USDC, straight to the provider, and are separate from any Algebra fee.
Algebra never pays a provider for a customer.

## Book a demo

`POST /api/v1/leads` validates a business enquiry (business name, work email,
website, use case, estimated usage, message) and stores it in `demo_requests`
(Postgres, migration 0023); the form at `/book-demo` posts to it and confirms
only after the row is written. Nothing is forwarded to a third party.

- Public, so it is bounded: five enquiries an hour per address (with Redis),
  every field length-checked in the service and again by the table, control
  characters stripped, and a hidden field that only a form-filling bot
  completes (answered like a success, stored nowhere).
- Read with `GET /api/v1/leads` and the operator token
  (`X-Algebra-Operator-Token`). With no operator token configured, nobody can
  read them: there is no open development fallback for people's contact
  details.
- There is no email notification and no calendar: someone has to read the
  list. The page promises a reply by email and nothing more.
