# Algebra

**The router and spend firewall for AI agents that pay for APIs on Solana.**

An agent says what it wants done and the most it will pay. Algebra finds every provider that can do it, asks each for its real price, drops the ones that are down or overcharge, pays the best one in USDC from a wallet the agent never sees, checks the answer, and hands it back with a signed receipt. All of it inside limits a person set, and can freeze with one switch.

Think OpenRouter, but for paid APIs, with a firewall in front of the wallet.

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

Amounts are micro-USDC (3000 = 0.003 USDC). `token.price` is a *class* of work: Algebra prices every provider of it and routes to the best for your strategy (`cheapest`, `fastest` or `auto`), falling back to the next if one fails.

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
| **Never twice** | An economic coordinator moves an intent through reserve → begin → authorize → complete → reconcile. The database allows at most one live attempt and one commitment. An ambiguous outcome freezes, and is settled from rail evidence (the chain). |
| **Rails** | x402 `exact` and `upto` (usage-based, through the Solana payment-channels program) in USDC on Solana mainnet and devnet, side by side, one wallet each. Settlement is proven from chain state; a payment is "not settled" only when it can never land. Mainnet and devnet payments never cross. |
| **Catalogs** | Pay.sh (75 providers), Circle's Agent Marketplace (27), PayAI's bazaar (1,152) and Coinbase's x402 Bazaar (368 of its most-used Solana endpoints, with how many payers each had in 30 days): 1,622 in all. Provider IDs are what passes and receipts name; in open directories they are derived from the host, not the name a stranger gives itself. |
| **Web discovery** | For work no catalog lists, Gemini searches the open web for endpoints; each is asked for its price with a free probe (never with your input) and returned as an unverified candidate the pass still has the last word on. |
| **Swaps** | Buy a token with USDC through Jupiter (`solana.swap`, off by default). The wallet signs only a transaction that, *simulated*, spends no more than the amount, delivers at least the quote less the slippage, and changes nothing else about the wallet: no new delegate, no new owner, no closed account. |
| **Receipts** | Every outcome gets an Ed25519 receipt with the routing decision, the execution and the settlement. Anyone can verify it against `/.well-known/jwks.json`, at `/verify` or with `cmd/verify-intent`. |
| **Replay** | The provider's answer is kept sealed (AES-256-GCM, 24 hours by default, opt out per request) so asking again returns it with `replayed: true` instead of "already committed". |
| **Interfaces** | REST (`/api/v1`), an MCP server at `/mcp` (stateless streamable HTTP, the Spend Pass as a bearer token, so it never passes through the model), and a console with the agent chat, passes, providers, routing, the firewall and executions. |

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

## Quick start

You need Go 1.26+, Node 20+ with pnpm, and Postgres. On Windows without Docker, `scripts/dev-native.ps1` runs a throwaway Postgres (`:5433`) and Redis (`:6380`) from the installs already on the machine; with Docker, `make dev-up`.

```bash
cp .env.example .env                 # then set ALGEBRA_MASTER_KEY: openssl rand -base64 32
make dev-up                          # Postgres + Redis (or: scripts\dev-native.ps1 up)
go run ./cmd/api                     # REST + MCP on :8080; migrations run on start
pnpm --dir web install && pnpm --dir web dev   # console on :3000
```

Open `http://localhost:3000`, create an account, and issue a Spend Pass (Console → Spend passes). The token is shown once; give it to an agent as `Authorization: Bearer …`. Connecting Claude Code, Claude Desktop, Cursor or the OpenAI Agents SDK is a one-line snippet on the **Connect** page.

With `ECONOMIC_SANDBOX=on` (the default for local runs) Algebra hosts a simulated paid provider and a simulated rail, so you can run the whole flow with no wallet and no money; sandbox receipts are marked `test`.

**Real payments on devnet.** The router and firewall are best seen against providers that behave like the real ones:

```bash
go run ./cmd/solana-wallet -new -out .data/solana-devnet.json   # fund it at faucet.circle.com (Solana Devnet)
go run ./cmd/demo-provider                                       # alpha, beta (slow), flaky (down), greedy (overcharges), trap ($25), meter (usage-billed)
ECONOMIC_PROVIDERS="$(go run ./cmd/demo-provider -print-config)" SOLANA_DEVNET_KEYPAIR_FILE=.data/solana-devnet.json go run ./cmd/api
curl -s localhost:8080/api/v1/policy/simulate ... -d '{"capability":"token.price","live_quotes":true}'   # ALLOW: would pay demo:beta; flaky, greedy and trap are refused, with reasons
```

A three-minute script for the router, the firewall and payment channels against those providers, with the funding steps and the curl and MCP equivalents: [docs/DEMO.md](docs/DEMO.md).

Wallet and rail details, mainnet, the first-payment runbook and `x402-dryrun` (prices a real provider and has a node *simulate* the payment, sending nothing): [docs/EXECUTION.md](docs/EXECUTION.md).

## Status, honestly

- **Built and tested:** everything above. The Go suite (unit, Postgres integration and end-to-end over real HTTP and MCP) passes; the web app typechecks and lints clean.
- **Verified against live services, read-only:** the four payable catalogs, Jupiter's quote API, Solana address derivation and transaction encoding on mainnet and devnet, and real x402 providers priced and their payments built and simulated with `cmd/x402-dryrun`. Mainnet is configured in the demo and dry-runs against live providers (a Circle-listed price provider, a Coinbase-listed token-risk provider with two down and two trap-priced ones refused, PayAI's web search).
- **Real payments, on devnet:** x402 `exact` calls and a metered `upto` call through a Solana payment channel (escrow, voucher, settle and refund in one transaction), each chosen and paid by the router with its guards and the firewall on. The transactions are linked in [docs/DEMO.md](docs/DEMO.md#proof-real-devnet-payments-through-algebra-2026-10-07).
- **Not yet real:** a mainnet payment, which needs a funded wallet and is the operator's to make (runbook in [docs/EXECUTION.md](docs/EXECUTION.md)), and a swap landing, verified on a fake cluster only.
- **Not built:** cross-chain transfers (CCTP) and any EVM rail, so Base-only x402 providers are listed but not payable; Monid's tools, listed for comparison but billed from a Monid balance rather than x402, and card rails such as Stripe; selling a token or buying SOL; OAuth sign-in for MCP (a Spend Pass bearer token is used); a recipient allow-list for passes.
- **The old product** (a grocery shopping agent, and tenant payment intents) still builds and its tests pass; its connectors, including web search, are kept as is, but it is not what Algebra is now. See [docs/legacy](docs/legacy/README.md).

## Repository layout

```
cmd/api                 REST API + MCP server (/mcp) + sandbox provider
cmd/mcp                 the same MCP tools over stdio, for a local agent
cmd/demo-provider       paid x402 APIs on devnet that behave like real ones, for demos
cmd/solana-wallet       create or inspect a wallet; cmd/x402-dryrun prices and simulates, never sends
cmd/verify-intent       verify an Intent Receipt independently
internal/domain         entities and rules, zero I/O: econ (intents, reservations), routing (candidates,
                        quotes, ranking, classes), spendpass, chain
internal/app            services: economic coordinator, execution (router, quoting, replay, simulate,
                        web discovery), spend pass controls, health probes
internal/platform       postgres, redis, solana (RPC, transactions, simulation), safehttp, config, wiring
internal/api/v1         REST transport (thin); internal/mcpserver: MCP transport (thin)
providers/              x402client (runner), solanax402 (rail), paychan (payment channels), jupiter (swaps),
                        catalog + paysh + bazaar (the four catalogs), monid (listed only), webdiscovery,
                        sandboxpay (the sandbox rail and providers)
migrations/             versioned SQL; Postgres is authoritative
web/                    Next.js console and the agent chat
connectors/, internal/domain/{intent,merchant,...}   the original shopping product, kept
```

## Documents

[Devnet demo](docs/DEMO.md) · [Execution, routing and the Solana rails](docs/EXECUTION.md) · [Economic coordination](docs/ECONOMIC_COORDINATION.md) · [Spend Passes and the firewall](docs/SPEND_PASSES.md) · [MCP](docs/MCP.md) · [Architecture](docs/ARCHITECTURE.md) · [Threat model](docs/THREAT_MODEL.md) · [Local development](docs/LOCAL_DEVELOPMENT.md) · [Production](docs/PRODUCTION.md) · [REST API](openapi/execution.yaml)

## Tests

```bash
make test               # unit tests, no external dependencies
make test-integration   # real Postgres, and the end-to-end flows over HTTP and MCP (needs DATABASE_URL)
```

Tests that need Postgres skip themselves when `DATABASE_URL` and `ALGEBRA_MASTER_KEY` aren't set. The end-to-end tests are hermetic: they turn every provider catalog off and use the sandbox, a fake Solana node and a fake Jupiter, so nothing leaves the machine.

> Core principle: give agents **permission to spend**, not **access to money**.
