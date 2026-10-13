# Algebra AI: implementation status report

Date: 2026-10-12 (Colosseum deadline day)
Branch: `feat/b2b-devnet-platform`, head `757ef68`, 12 commits on top of `algebra-ai/main` (`2368e80`)
Push state: **nothing pushed.** The branch is a clean fast-forward from `algebra-ai/main`. A push waits for the owner's explicit yes.

This report is for another reviewer (ChatGPT) to read cold. It says what was asked, what was built, what evidence exists, and what is still missing. Where something is not proven, it says so.

---

## 1. What Algebra is

Algebra is the execution and routing layer between AI agents and paid APIs on Solana. An agent asks for an outcome and a spending ceiling. Algebra finds providers across four catalogs (Pay.sh, Circle Agent Marketplace, PayAI, Coinbase x402 Bazaar), asks each for its real price with a free unpaid request, drops the ones that are down or overcharge, pays the best one in USDC from a wallet the agent never sees, verifies the answer, and returns it with a signed Ed25519 receipt. All of it inside limits a person set (a "Spend Pass"), which can also be enforced on-chain by an Anchor program.

Stack: Go backend, Next.js frontend, PostgreSQL (authoritative), Redis (rate limits), MCP server, Solana (Anchor program on devnet).

It sits above Jupiter, Circle, pay.sh and the providers. It replaces none of them.

---

## 2. The task given

A master implementation prompt asked for, in priority order:

- **P0:** a working Solana devnet demonstration (wallet, vault, quote, policy, payment, receipt, no double-pay).
- **P1:** B2B productization: organizations, projects, scoped API keys, sandbox/devnet/mainnet environments, MCP, six-hourly Provider Intelligence refresh, verification registry, usage metering, pricing + 7-day trial, Book a Demo, functional public site.
- **P2:** credential vault/BYO keys, TypeScript SDK, webhooks, PDF receipts, async jobs.
- Plus: six demos (A to F), six Mermaid diagrams, claims list, mainnet readiness report, hackathon checklist, route audit.
- Hard rules: preserve working code, no mainnet deploy, no funded mainnet transaction, no pretending a mock is production, no invented success.

The effort ran across two sessions (the first hit a usage limit). This report covers both.

---

## 3. What is built and verified

### 3.1 Developer platform

| Feature | State | Evidence |
|---|---|---|
| Organizations, members (roles), projects | Built | HTTP test `TestTenancyOverHTTP_*`; migration `0021` |
| Scoped API keys (`alg_sk_<env>_…`) | Built | secret shown once, SHA-256 hash stored, scopes `read`/`simulate`/`execute`, rotate, revoke, last-used, audit events |
| Environments: sandbox / devnet / mainnet | Built | a sandbox key can never reach a chain; key authenticates, Spend Pass authorizes (two separate authorities) |
| Cross-tenant isolation | Tested | another person's org, project, keys, passes, events, usage all return 404/403 |
| Project limit by plan | Built (this session) | Free = 1 project, enforced with 409 |
| 7-day Startup trial | Built (this session) | one per org (condition in the `UPDATE`), no card, ends on its own because the plan in force is computed on read |
| Usage metering per project | Built (this session) | requests by state; committed executions and spend per network; sandbox runs counted but not as managed executions; a repeat counts once |
| Book a Demo lead endpoint | Built (this session) | `POST /api/v1/leads`: validated, stored in `demo_requests` (migration `0023`), 5/hour/IP, honeypot field, readable only with the operator token |

### 3.2 MCP

Default tools: `algebra.discover`, `quote`, `simulate`, `execute`, `status`, `receipt`, `spend_pass` (plus `classes`, `discover_web`).
`algebra.quote` asks every provider for its live price and pays nothing. `algebra.receipt` returns the signed receipt with the server's own signature check.
Legacy shopping tools and the old tool names (`algebra.discover_providers`, `algebra.execution_status`) only appear with `MCP_LEGACY_TOOLS=on`.
Tested end to end over real HTTP with the official Go SDK client, including that another person's receipt cannot be read.

### 3.3 Provider Intelligence

- Registry of every paid endpoint from the four catalogs (6,131 endpoints, 1,725 providers on 2026-10-12).
- Refresh every six hours (`0 */6 * * *` UTC), outside user requests, lease-based so exactly one instance runs it, per-catalog timeout and retries with backoff, last good copy kept when a catalog fails or answers empty, history kept forever (`added`, `removed`, `restored`, `price_changed`, `payee_changed`, `callable_changed`, `unreachable`, `trust_changed`).
- Trust model: new listings start `unverified` with payee pinned. `verified` only after a paid delivery to the pinned payee. A different payee, in a listing or a live quote, is refused at payment time and blocks the endpoint (`PAYEE_CHANGED`). A person decides unblock. Parallel AI or any search result can never verify.
- Five separate facts shown in the console: Listed, Quoted, Tested, Verified, Executable.
- Tests (this session): a whole refresh against a stand-in catalog on real Postgres (new, re-priced, re-payee'd, outage, empty answer, delivery, removals), and sixteen concurrent instances claiming the job with exactly one winner.

### 3.4 Public site and console

- New pages: `/pricing` (five plans, 7-day trial, who is paid what; states plainly that paid plans cannot be bought online), `/book-demo` (working form, confirms only after the row is written).
- Console: plan badge, project limit, "Start 7-day trial" button, monthly usage table on `/console/projects`.
- Nav, footer, sitemap and landing calls to action updated. Privacy policy covers demo requests.
- Console pages already existed or were added earlier in the effort: Provider intelligence, Projects & API keys, Quickstart (Demo A end to end), Providers, Routing, Firewall, Executions, Passes, Connect, Settings.

### 3.5 Solana

- Anchor Spend Pass program `46gQDUuJCt6VdDMaguGPoD8SKEtFqvpG7ECtpC2F9tdS` on devnet; nine instructions; PDA vault only the program can move.
- `cargo build-sbf` succeeds (261,840 bytes). 13 unit tests + 11 LiteSVM tests pass (3 added this session: only the pass's own asset can be pulled, a replayed transaction is not a second pull, a non-pass account is refused).
- The program is **not** on mainnet (checked against the cluster on 2026-10-12).

---

## 4. Devnet evidence (Demo B, re-verified on 2026-10-12)

A project's devnet API key paid for `token.price` from an on-chain pass vault.

| Step | Transaction signature (devnet) |
|---|---|
| Owner created the pass and deposited 0.1 USDC (`CreatePass` + `Deposit`) | `8ieRLFSe2T99w7C2aAfeiq1w5xNPYvMiyP14cHhSaTgiwpYRyWw4ZCvkWHjvph2zsb6Jj8Cf6Jnwu9Kr6LYxLYs` |
| Program `pull` of exactly 0.001 USDC to the payer | `PNMGp9o1WLsUba1fasrqDuDuDQ8EXbstVjBMfZFf9JxqU9HLc3UGeCAjBWbsNRq9R5GpGEyAcrcjr1KcP5gFWSr` |
| x402 payment to demo:beta, intent COMMITTED | `5C37eZ6GBoFFqJBb5gRApSKcb963Wjw4K6vvva6aXXiZhm6ou615NFtmXefFtLNzNa5LXw8yEfbVC9BJztWmDqZ` |

- Pass account `6mWZibyYhPJmML2ZKZjrgttiaZ7tfMmNzagbvA5v9Uyz`; owner `9kEVg2noE8uubD6e1jMtEwumULEyNLgr7uYCXDVL4CLW`.
- All three finalized without error. The pass account has exactly two transactions.
- Same request sent again (Demo D): answered from the kept result, `replayed: true`, no second pull, no second payment. Database: one pull for the pass, state `SETTLED`.
- The signed receipt (`ircpt_99265b38-…`) verifies (`valid: true`) at `POST /api/v1/receipts/verify`, naming the pass, provider, amount and payment transaction.

**Important honesty note:** the seller, `demo:beta`, is Algebra's own devnet demo provider, not an independent third party. The public catalogs list mainnet endpoints and no third-party x402 provider settling on devnet was found.

---

## 5. Demonstrations

| Demo | What it shows | State |
|---|---|---|
| A | Sign up, project, sandbox pass, API key, dry run, sandbox execution, receipt | Works. Run in a real browser on 2026-10-12 |
| B | Devnet payment from on-chain Spend Pass, explorer links, signed receipt | Works on devnet, evidence above |
| C | Provider that changes payee is refused then blocked, reason shown | Works. HTTP test; visible in Quickstart and Provider intelligence |
| D | Same request twice pays once | Works. HTTP test and devnet proof |
| E | Registry refresh detects new, re-priced, re-payee'd endpoints, trust transition | Works. Postgres test on a stand-in catalog; the refresh also runs against the four live catalogs |
| F | Jupiter swap: quote, policy, simulate before sign | Tested on a fake cluster with a fake Jupiter only. **No swap has landed on any chain** |

---

## 6. Test and build results at head `757ef68`

| Check | Result |
|---|---|
| Go tests, no database | 63 packages pass |
| Go tests, real Postgres | 63 packages pass; 59 further tests run only with the DB |
| `go vet ./...` | clean |
| gofmt on a clean `git archive` export | clean; build + vet + tests re-run there, 63 ok |
| Solana: `cargo build-sbf` | OK |
| Solana: unit + LiteSVM tests | 13 + 11 pass |
| Frontend: eslint, `tsc --noEmit`, `next build` | pass (31 routes) |
| Secret scan over all unpushed commits | 0 real-value hits; 6 shape hits are devnet tx signatures in explorer links (public) |

Browser route audit done by hand against a local API and web server (see `docs/ROUTE_AUDIT.md`): every public and console page loaded; Book a Demo submitted and the row read back from the database; trial started; Demo A executed; `/pricing`, `/book-demo`, `/console/projects` checked at 375 px with no horizontal overflow.

Bugs found by that audit and fixed:
1. Every provider detail page answered "no such provider": the provider ID (with a colon) was percent-encoded twice.
2. The Connect page named two MCP tools by their old names.
3. The overview page claimed only the sandbox could be paid regardless of server config.
4. The pricing page listed member seats nobody can use (removed).

---

## 7. Not built (labelled "planned" in the docs; must not be claimed)

- A payment processor for the five plans; nothing is refused at a plan's monthly execution allowance (usage is counted and shown; only the project limit is enforced).
- Inviting a second person into an organization (roles exist in the schema).
- PDF receipts (the signed JSON receipt is the record).
- TypeScript SDK.
- Provider credential vault and BYO-key execution for fiat-billed APIs.
- Customer webhooks for execution events on the new platform.
- OAuth 2.1 for hosted MCP connectors (ChatGPT/claude.ai app connectors).
- Parallel AI as an optional discovery source.
- Asynchronous jobs and Smart Batch.
- Base / CCTP / cards / enterprise SSO (P3, deferred).

## 8. Not tested

- Wallet connect in a browser (needs a wallet extension). The owner's transaction in Demo B was signed from a keypair file with the project's CLI, the same unsigned transaction the console would hand to a wallet.
- Privy sign-in (no Privy app configured locally).
- The agent chat answering (needs a model key; sending can spend).
- Screenshots / visual review (browser pane was hidden; layout verified by text and measured width only).
- Named MCP clients (Claude, Cursor, ChatGPT) against this build. The server is tested with the official Go SDK client only.

---

## 9. Mainnet: read-only, not ready

Mainnet today: discovery, unpaid quotes, cost comparison and dry runs work. Every mainnet payment is refused (`MAINNET_EXECUTION_DISABLED`) unless `MAINNET_EXECUTION=on`, and a sandbox/devnet key or pass can never pay on mainnet.

Before a funded mainnet test (full list in `docs/MAINNET_READINESS.md`):
1. Independent security review of the Spend Pass program (nobody outside the team has read it; not audited).
2. Deploy to mainnet (about 2.7 SOL), with the owner's explicit go-ahead.
3. **Upgrade authority**: on devnet it is `GhpmTWT8…EoPA`, the same key the server uses as agent and payer. Acceptable for devnet; for mainnet it must be a multisig or the program made immutable.
4. Isolated production signer (a KMS signer exists only as unmerged, unreviewed local work).
5. Monitoring/alerting, per-org budgets, paid RPC, incident runbook, then one payment of a few cents by the operator.

No mainnet deployment, transaction, transfer, authority change or `MAINNET_EXECUTION` switch has been done or will be done without the owner's say-so.

---

## 10. Commits on the branch (oldest first)

```
7ab3af2 Port sandbox sellers, demo pass and provider detail pages from local work
680a359 Organizations, projects and scoped API keys; environment and mainnet gates
74c8975 Provider Intelligence: endpoint registry, payee pinning and a six-hourly refresh
ba2a6d9 Provider trust: sandbox sellers listed at startup; Demo C over HTTP
c0d1aab Provider Intelligence API: registry, history, status, refresh, trust
0a0d4e5 Console: provider intelligence, projects and API keys, quickstart
a706051 Plans, the seven-day Startup trial, project usage and Book a Demo enquiries
08a5ed7 MCP: the execution tools are the default; the legacy tools need a flag
4b9bc2f Public site: Pricing and Book a Demo; console: plan, trial and project usage
3784bca Console: provider pages open again; Connect lists the current MCP tools
9447eda Docs for the platform and the submission; three more on-chain tests
757ef68 Demo E on Postgres: a whole registry refresh, and the job lease under a race
```

## 11. Documents in the repository

`README.md` (rewritten), `docs/PLATFORM.md`, `docs/DIAGRAMS.md` (6 Mermaid diagrams: architecture, discovery and routing, Solana payment, policy decisions, provider verification, receipts and reconciliation), `docs/CLAIMS.md` (every claim with its test or transaction, and a "do not say" list), `docs/MAINNET_READINESS.md`, `docs/HACKATHON_CHECKLIST.md` (video scripts, commands, say-this-not-that), `docs/ROUTE_AUDIT.md`, plus updated `docs/DEMO.md`, `docs/MCP.md`, `docs/SPEND_PASS_PROGRAM.md`.

## 12. Open decisions for the owner

1. Push the branch to `algebra-ai`? (Routine: fetch, secret scan, clean-checkout checks, plain fast-forward, then check CI.)
2. Add a `LICENSE` file. The site footer says Apache-2.0; the repo has none.
3. Decide how the devnet program's upgrade authority is handled before any mainnet work.
4. Submission items outside the repo: demo video, pitch deck and go-to-market, team section, hosted deployment URL.
5. Wallet-connect step recorded in a browser with a wallet extension.

## 13. One-paragraph summary

Algebra now has a working B2B platform on top of the existing execution core: organizations, projects, environment-scoped API keys, plans with an enforced project limit and a working 7-day trial, per-project usage, a stored Book a Demo endpoint, an MCP server whose default tools are the seven execution tools, a six-hourly lease-based Provider Intelligence registry with payee pinning and trust states, and a console and public site that were checked in a browser. A real devnet payment from an on-chain Spend Pass vault was driven by a project API key and verified finalized, with a verifying signed receipt and a proven no-double-pay replay. All tests and builds pass with and without a database. Billing, SDK, PDF receipts, BYO keys, member invites and mainnet payments are not built and are labelled as such. The devnet seller is Algebra's own demo provider, and nothing has been pushed.
