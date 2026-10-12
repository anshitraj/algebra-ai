# Algebra

**The router and spend firewall for AI agents that pay for APIs on Solana.**

An agent says what it wants done and the most it will pay. Algebra finds every provider that can do it, asks each for its real price, drops the ones that are down or overcharge, pays the best one in USDC from a wallet the agent never sees, checks the answer, and hands it back with a signed receipt. All of it inside limits a person set, and can freeze with one switch.

Think OpenRouter, but for paid APIs, with a firewall in front of the wallet.

For a business it is one API: create a project, issue an API key for the sandbox, devnet or mainnet, and call it over REST or MCP. The key says who is calling; a Spend Pass says what may be spent. Algebra sits above the catalogs, Jupiter and the payment rails. It replaces none of them.

```bash
curl -s localhost:8080/api/v1/execute \
  -H "Authorization: Bearer $SPEND_PASS_TOKEN" -H "Content-Type: application/json" \
  -d '{"capability":"token.price","input":{"mint":"So11111111111111111111111111111111111111112"},
       "budget_max_minor":10000,"provider_policy":{"strategy":"cheapest"}}'
```

```jsonc
{
  "delivered": true,
  "response": { "price_usd": 142.31, "...": "..." },          // the provider's answer: untrusted data
  "routing": { "mode": "CHEAPEST", "offers": [                 // who was priced, and why this one
    { "rank": 1, "provider": "circle:birdeye", "cost_minor": 3000, "score": 0.97, "notes": ["14 of 14 calls delivered"] },
    { "rank": 2, "provider": "payai:alpha",     "cost_minor": 4000, "score": 0.92 } ] },
  "rejected": [ { "provider": "payai:greedy", "code": "price_above_listing", "detail": "asks $0.004; its listing says $0.001" },
                { "provider": "payai:trap",   "code": "price_outlier",       "detail": "$25 for one call, 8333× the usual" } ],
  "intent": { "state": "COMMITTED", "committed_minor": 3000 },   // 0.003 USDC
  "receipt": "eyJhbGciOiJFZERTQSIs..."                          // Ed25519 JWS: anyone can verify it
}
```

The bearer token is a Spend Pass token or an Algebra API key (`alg_sk_…`, which spends through a pass of its own project and environment). Amounts are micro-USDC (3000 = 0.003 USDC). `token.price` is a *class* of work: Algebra prices every provider of it and routes to the best for your strategy (`cheapest`, `fastest` or `auto`), falling back to the next if one fails.

## Why

An agent that can pay for things has three problems, and none is the model's to solve:

1. **Which provider?** Thousands of paid endpoints are listed across four public catalogs, under different names, prices and input shapes. Some are down. Some ask more than they list. Some are honeypots priced to trap agents that pay whatever a `402` says.
2. **How do I stop it overspending?** Not "ask the model nicely": a budget, a per-call ceiling, an approval line, an allow-list, a velocity limit, a rule for providers it has never paid, a kill switch, and a dry run to ask "would this be allowed?" before anything moves.
3. **Was it done, and paid exactly once?** A timeout is not proof that money moved or didn't. Retries and fallbacks must never pay twice, and an ambiguous outcome must be settled from the chain, not from anyone's word.

## What it does

| | |
|---|---|
| **Router** | Quotes every candidate with a free unpaid request, all at once (the call waits 1.5 s past the first price, not for the slowest of twelve). Ranks for `cheapest`, `fastest` or `auto` using live price, measured latency, and Algebra's own record of each provider (success rate, quality, whether it charged what it listed). Falls back down the ranking only when the coordinator proves the earlier attempt moved no money. |
| **Classes** | 14 kinds of work (token price, token risk, wallet balances, web search, LLM chat, …) grouped across every catalog with one input shape and an adapter per provider: 661 endpoints, 280 routable today. The agent asks for `token.price`, not for Birdeye. |
| **Guards** | A free health probe every 15 minutes (`provider_down`), and two checks on the live price: more than 5% above the provider's own listing (`price_above_listing`), or more than 10× the class's usual price (`price_outlier`; $1,000 a call is never paid). |
| **Spend firewall** | A *Spend Pass* per agent: USDC budget, per-call cap, approval line, allowed providers, calls per minute (per pass and per provider), a rule for new providers (`allow`, `cap` at $0.05 by default, or `approve`), a kill switch that stops even a payment in flight, and `POST /api/v1/policy/simulate` for a dry run. Enforced server-side, again right before money moves. |
| **On-chain Spend Pass** | The same limits, enforced by Algebra's own program on Solana ([`46gQ…9tdS`](https://explorer.solana.com/address/46gQDUuJCt6VdDMaguGPoD8SKEtFqvpG7ECtpC2F9tdS?cluster=devnet), written on Anchor, live on devnet): the person's wallet funds a vault that Algebra can only pull from for one payment at a time, only into its payer, only within the per-call cap, budget, window and expiry the program checks, and never once the owner freezes it from their wallet. Unspent pulls are refunded to the vault from chain proof. Even a compromised Algebra server can't take more. [docs/SPEND_PASS_PROGRAM.md](docs/SPEND_PASS_PROGRAM.md) |
| **Never twice** | An economic coordinator moves an intent through reserve → begin → authorize → complete → reconcile. The database allows at most one live attempt and one commitment. An ambiguous outcome freezes, and is settled from rail evidence (the chain). |
| **Rails** | x402 `exact` and `upto` (usage-based, through the Solana payment-channels program) in USDC on Solana mainnet and devnet, side by side, one wallet each. Settlement is proven from chain state; a payment is "not settled" only when it can never land. Mainnet and devnet payments never cross. |
| **Catalogs** | Pay.sh (75 providers), Circle's Agent Marketplace (27), PayAI's bazaar (1,152) and Coinbase's x402 Bazaar (368 of its most-used Solana endpoints, with how many payers each had in 30 days): 1,622 in all. Provider IDs are what passes and receipts name; in open directories they are derived from the host, not the name a stranger gives itself. |
| **Web discovery** | For work no catalog lists, Gemini searches the open web for endpoints; each is asked for its price with a free probe (never with your input) and returned as an unverified candidate the pass still has the last word on. |
| **Swaps** | Buy a token with USDC through Jupiter (`solana.swap`, off by default). The wallet signs only a transaction that, *simulated*, spends no more than the amount, delivers at least the quote less the slippage, and changes nothing else about the wallet: no new delegate, no new owner, no closed account. |
| **Receipts** | Every outcome gets an Ed25519 receipt with the routing decision, the execution and the settlement. Anyone can verify it against `/.well-known/jwks.json`, at `/verify` or with `backend/cmd/verify-intent`. |
| **Replay** | The provider's answer is kept sealed (AES-256-GCM, 24 hours by default, opt out per request) so asking again returns it with `replayed: true` instead of "already committed". |
| **Developer platform** | Organizations, projects and API keys scoped to one project and one environment (`sandbox`, `devnet`, `mainnet`). A key authenticates; a Spend Pass authorises: two authorities, kept apart. Keys are shown once, stored as a hash, scoped (`read`, `simulate`, `execute`), rotated and revoked, with an audit trail and per-project usage. A sandbox key can never reach a chain. [docs/PLATFORM.md](docs/PLATFORM.md) |
| **Provider Intelligence** | A registry of every paid endpoint the catalogs list (6,131 on 2026-10-12), refreshed every six hours by one instance under a database lease, with a history of what changed. The payee of an endpoint is pinned; one that asks to be paid somewhere else is refused at payment time and blocked. Listed, Quoted, Tested, Verified and Executable are five separate facts. [docs/PLATFORM.md](docs/PLATFORM.md#provider-intelligence) |
| **Interfaces** | REST (`/api/v1`), an MCP server at `/mcp` (stateless streamable HTTP, the token as a bearer header, so it never passes through the model) whose default tools are `algebra.discover`, `quote`, `simulate`, `execute`, `status`, `receipt` and `spend_pass`, and a console: quickstart, projects and keys, the agent chat, passes, providers, provider intelligence, routing, the firewall and executions. |

## How a request flows

```
agent ──► POST /api/v1/execute   (or MCP algebra.execute)
            │
            ▼
   Spend Pass  ── is this agent, this provider, this amount, this fast, this new, allowed? ── no ─► refused, with reasons
            │ yes
            ▼
   candidates ── operator's providers · catalogs (as a class) · web discovery · endpoints the agent found
            │      health + listing + outlier guards
            ▼
   quotes ──── free unpaid 402 to each, in parallel ─► Rank(cheapest | fastest | auto) ─► plan
            │
            ▼
   attempt ─── reserve ─► begin ─► authorize payment (wallet signs; agent never sees a key) ─► call ─► verify
            │                                     │
            │                             rail proves settlement from the chain
            ▼
   receipt (signed) + the answer (kept, sealed) ─► routing history learns from it
```

Six diagrams of the architecture, routing, the Solana payment, policy, provider verification and receipts, as Mermaid: [docs/DIAGRAMS.md](docs/DIAGRAMS.md).

## Sandbox, devnet and mainnet

| | Money | Chain | What it is for |
|---|---|---|---|
| **Sandbox** | simulated USDC | none | onboarding and tests. Simulated sellers, including ones that misbehave. Receipts are marked `test` and are never evidence of a payment |
| **Solana devnet** | test USDC | real transactions, real program | the demonstration: explorer-verifiable payments under on-chain limits |
| **Solana mainnet** | real USDC | real | **read-only today**: discovery, quotes and dry runs. Every payment is refused until `MAINNET_EXECUTION=on`. [docs/MAINNET_READINESS.md](docs/MAINNET_READINESS.md) |

A key or a pass belongs to one of the three and can never pay in another.

## Quick start

You need Go 1.26+, Node 20+ with pnpm, and Postgres. On Windows without Docker, `scripts/dev-native.ps1` runs a throwaway Postgres (`:5433`) and Redis (`:6380`) from the installs already on the machine; with Docker, `make infra-up`.

```bash
cp backend/.env.example backend/.env   # then set ALGEBRA_MASTER_KEY: openssl rand -base64 32
make infra-up                          # Postgres + Redis (or: scripts\dev-native.ps1 up)
make api                               # the backend: REST + MCP on :8080; migrations run on start
make web                               # the frontend, the console on :3000 (first time: pnpm --dir frontend install)
```

The parts are separate. [backend/](backend/README.md) is the Go server (the API, the MCP server, the demo providers and the wallet tools), [frontend/](frontend/README.md) is the Next.js console, and [deploy/](deploy/README.md) says how to run and deploy each one on its own.

Open `http://localhost:3000`, create an account, and follow **Quickstart** in the console: it makes a sandbox Spend Pass and an API key, runs a dry run and a sandbox execution, and shows the answer and its receipt, with no wallet and no money. To connect an agent instead, issue a Spend Pass (Console → Spend passes) and give its token to the agent as `Authorization: Bearer …`; the **Connect** page has the snippet for Claude Code, Claude Desktop, Cursor and the OpenAI Agents SDK.

With `ECONOMIC_SANDBOX=on` (the default for local runs) Algebra hosts a simulated paid provider and a simulated rail, so you can run the whole flow with no wallet and no money; sandbox receipts are marked `test`.

**Real payments on devnet.** The router and firewall are best seen against providers that behave like the real ones. Run these from `backend/`; `ALGEBRA_DATA_DIR` keeps the keys in the repository's `.data/` (the `make` targets and the dev script set it for you):

```bash
cd backend && export ALGEBRA_DATA_DIR="$(pwd)/../.data"
go run ./cmd/solana-wallet -new -out ../.data/solana-devnet.json   # fund it at faucet.circle.com (Solana Devnet)
go run ./cmd/demo-provider                                          # alpha, beta (slow), flaky (down), greedy (overcharges), trap ($25), meter (usage-billed)
ECONOMIC_PROVIDERS="$(go run ./cmd/demo-provider -print-config)" SOLANA_DEVNET_KEYPAIR_FILE=../.data/solana-devnet.json go run ./cmd/api
curl -s localhost:8080/api/v1/policy/simulate ... -d '{"capability":"token.price","live_quotes":true}'   # ALLOW: would pay demo:beta; flaky, greedy and trap are refused, with reasons
```

A three-minute script for the router, the firewall and payment channels against those providers, with the funding steps and the curl and MCP equivalents: [docs/DEMO.md](docs/DEMO.md).

Wallet and rail details, mainnet, the first-payment runbook and `x402-dryrun` (prices a real provider and has a node *simulate* the payment, sending nothing): [docs/EXECUTION.md](docs/EXECUTION.md).

## Demonstrations

| | Shows | State |
|---|---|---|
| **A** | Sign up, project, sandbox pass, API key, dry run, sandbox execution, receipt | Works; run in a browser on 2026-10-12 |
| **B** | A devnet payment from an on-chain Spend Pass, with explorer links and a signed receipt | Works on devnet; three finalized transactions. The seller is Algebra's own demo provider |
| **C** | A provider that changes its payee is refused, then blocked, with the reason in the UI | Works; tested over HTTP |
| **D** | The same request twice pays once | Works; tested over HTTP and shown on devnet |
| **E** | The registry refresh detects new, re-priced and re-payee'd endpoints | Works; tested on Postgres against a stand-in catalog, including sixteen instances racing for the job; runs against the four live catalogs |
| **F** | A Jupiter swap: quote, policy, simulate before sign | Tested on a fake cluster only. No swap has landed on a chain |

How to run each, and the evidence: [docs/DEMO.md](docs/DEMO.md#the-six-demonstrations). Scripts for a video: [docs/HACKATHON_CHECKLIST.md](docs/HACKATHON_CHECKLIST.md).

## Status, honestly

Every claim below has a test or a transaction behind it, listed in [docs/CLAIMS.md](docs/CLAIMS.md).

- **Built and tested:** everything in the table above. The Go suite (unit, Postgres integration, and end-to-end over real HTTP and MCP) passes; the web app typechecks, lints and builds.
- **Real payments, on devnet:** x402 `exact` calls, a metered `upto` call through a Solana payment channel, and paid requests funded by on-chain Spend Pass pulls, including one made with a project's API key. Transactions in [docs/DEMO.md](docs/DEMO.md) and [docs/SPEND_PASS_PROGRAM.md](docs/SPEND_PASS_PROGRAM.md).
- **On-chain Spend Pass, on devnet:** the program is deployed and tested (13 unit tests, 11 LiteSVM tests of the built program). Not deployed on mainnet. Not audited. Its upgrade authority on devnet is a key the server holds.
- **Mainnet: read-only.** The four catalogs, Jupiter's quote API and real x402 providers are priced, and their payments built and simulated, without sending anything. No mainnet payment has been made, and the server refuses to make one by default.
- **Devnet sellers are ours.** The catalogs list mainnet endpoints; the devnet providers in every demo are Algebra's own demo provider, labelled as such.
- **Not real yet:** a mainnet payment; a swap landing (fake cluster only); a browser wallet signing the on-chain pass transaction in a recorded run (it was signed from a keypair file).
- **Defined, not sold:** five plans and a seven-day Startup trial. The trial and the project limit work; no payment processor is connected, nothing stops at a plan's execution allowance, and nobody can be invited into an organization yet.
- **Not built:** PDF receipts, a TypeScript SDK, a provider credential vault and customer-key (BYO) execution, customer webhooks for the new platform, OAuth for hosted MCP connectors, the optional Parallel discovery source, asynchronous jobs and batches, cross-chain transfers (CCTP) and any EVM rail (Base-only providers are listed and not payable), card rails, Monid's tools (listed for comparison, billed from a Monid balance rather than x402), a recipient allow-list for passes, selling a token or buying SOL.
- **The old product** (a grocery shopping agent, and tenant payment intents) still builds and its tests pass. Its MCP tools are off unless `MCP_LEGACY_TOOLS=on`. See [docs/legacy](docs/legacy/README.md).

## Roadmap

1. Mainnet, in the order of [docs/MAINNET_READINESS.md](docs/MAINNET_READINESS.md): an independent review of the program, the upgrade authority off the server, an isolated signer, then one small payment.
2. Billing for the plans, and enforcement of the execution allowance.
3. Team members, customer webhooks, a TypeScript SDK, PDF receipts.
4. Customer-key execution for providers that bill in fiat, behind a credential vault.
5. More rails: Base, CCTP.

## Repository layout

Each part lives in its own folder, with its own Dockerfile, environment example and README, so it can be built, run and deployed without the others.

```
backend/            the Go server: one module, several programs, one Postgres
  cmd/api             REST API + MCP server (/mcp) + sandbox provider      (image: backend/Dockerfile)
  cmd/mcp             the same MCP tools over stdio, for a local agent
  cmd/demo-provider   paid x402 APIs on devnet that behave like real ones, for demos
  cmd/solana-wallet   create or inspect a wallet; x402-dryrun prices and simulates, never sends
  cmd/verify-intent   verify an Intent Receipt independently (also paychan-smoke, monid-import, merchant-login)
  internal/domain     entities and rules, zero I/O: econ (intents, reservations), routing (candidates,
                      quotes, ranking, classes), spendpass, chain
  internal/app        services: economic coordinator, execution (router, quoting, replay, simulate,
                      web discovery), spend pass controls, health probes
  internal/platform   postgres, redis, solana (RPC, transactions, simulation), safehttp, config, wiring
  internal/api/v1     REST transport (thin); internal/mcpserver: MCP transport (thin)
  providers/          x402client (runner), solanax402 (rail), paychan (payment channels), jupiter (swaps),
                      catalog + paysh + bazaar (the four catalogs), monid (listed only), webdiscovery,
                      sandboxpay (the sandbox rail and providers)
  migrations/         versioned SQL; Postgres is authoritative
  openapi/            the REST API's OpenAPI documents
  test/e2e            end-to-end tests over real HTTP and MCP
  connectors/, policy/, internal/domain/{intent,merchant,...}   the original shopping product, kept
solana-program/     the Spend Pass program (Anchor): a vault only Algebra's payer can pull from, within limits it checks
                    on chain; LiteSVM tests. backend/cmd/spend-pass drives it from keypair files
frontend/           Next.js console and the agent chat                      (image: frontend/Dockerfile)
deploy/             docker-compose.yml (Postgres, Redis, optional app containers) and how to run each part alone
docs/               the project's documentation; docs/legacy is the original shopping product
scripts/            dev-native.ps1: Postgres, Redis and the API on Windows without Docker
.github/workflows   CI: backend, frontend, migrations, security, program unit tests, Docker image builds
```

## Documents

[The developer platform](docs/PLATFORM.md) · [Diagrams](docs/DIAGRAMS.md) · [What is proven](docs/CLAIMS.md) · [Mainnet readiness](docs/MAINNET_READINESS.md) · [Hackathon checklist](docs/HACKATHON_CHECKLIST.md) · [Route audit](docs/ROUTE_AUDIT.md) · [Devnet demo](docs/DEMO.md) · [Execution, routing and the Solana rails](docs/EXECUTION.md) · [Economic coordination](docs/ECONOMIC_COORDINATION.md) · [Spend Passes and the firewall](docs/SPEND_PASSES.md) · [The Spend Pass program on Solana](docs/SPEND_PASS_PROGRAM.md) · [MCP](docs/MCP.md) · [Architecture](docs/ARCHITECTURE.md) · [Threat model](docs/THREAT_MODEL.md) · [Local development](docs/LOCAL_DEVELOPMENT.md) · [Production](docs/PRODUCTION.md) · [REST API](backend/openapi/execution.yaml)

## Tests

```bash
make test               # unit tests, no external dependencies
make test-integration   # real Postgres, and the end-to-end flows over HTTP and MCP (needs DATABASE_URL)
```

```bash
cargo build-sbf --manifest-path solana-program/Cargo.toml && cargo test --manifest-path solana-program/Cargo.toml   # the Spend Pass program
pnpm --dir frontend lint && pnpm --dir frontend build                                                               # the web app
```

Last full run, 2026-10-12 (Go 1.26, cargo 1.92, Node 24): 63 Go packages pass without a database and again against Postgres (59 more tests run with it); `go vet` is clean; the program builds and its 13 unit and 11 LiteSVM tests pass; the web app lints, typechecks and builds.

Tests that need Postgres skip themselves when `DATABASE_URL` and `ALGEBRA_MASTER_KEY` aren't set. The end-to-end tests are hermetic: they turn every provider catalog off and use the sandbox, a fake Solana node and a fake Jupiter, so nothing leaves the machine.

> Core principle: give agents **permission to spend**, not **access to money**.
