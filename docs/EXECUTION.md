# Execution: Algebra does the work

Algebra's economic coordinator ([ECONOMIC_COORDINATION.md](ECONOMIC_COORDINATION.md)) decides whether money may move. This layer
does the work around it: it finds who can do a job, prices them, ranks them, pays through Algebra's own wallet, makes the
call, checks the result and signs a receipt. The agent never holds a key, a card or a payment value.

```
agent: "token price for X, at most 0.01 USDC, cheapest"
   │
   ▼  POST /api/v1/execute        (or the MCP tool algebra.execute, at /mcp)
intent ─► candidates ─► quotes ─► ranking ─► plan ─► attempt(s) ─► verify ─► receipt
 (econ)   (routing)    (free     (cheapest   (best    reserve → begin     (schema,   (signed,
                       unpaid    fastest     few)     → pay → call →      quality)   JWKS)
                       402s)     auto)                complete → reconcile
```

## The vocabulary (`internal/domain/routing`)

| Type | Meaning |
|---|---|
| `Capability` | A kind of outcome that can be bought (`solana.token-risk`): its kind, evaluator and output schema. |
| `Class` | A kind of work many providers sell under different names (`token.price`), with one input shape. See "Classes". |
| `Candidate` | One provider claiming to do it, normalised from whatever source found it. Its ID is a hash of what it is, so the same offer found twice merges. |
| `Quote` | A candidate's live, time-bounded price for one specific input. A candidate's advertised price is never authoritative; a quote fetched shortly before paying is. |
| `ExecutionPlan` | Ranked quotes: a primary and fallbacks, with the reasons others were left out. Sealed with a hash. |
| `ExecutionResult` | What happened to one attempt: cost, latency, what the rail proved, what was delivered, hashes. |
| `QualityResult` | How good the delivered result was. A score that couldn't be judged is absent, never a guess. |

Money is integer minor units (micro-USDC), never floats. `internal/domain/chain` names networks and assets one way
(x402 v1 short names and CAIP-2 ids agree) and knows Circle's real USDC address on each network.

## Who decides what

| | Decides | Can't |
|---|---|---|
| **Runner** (`providers/x402client`, `providers/jupiter`) | Prices a candidate; makes the call; reports what it saw. | Decide economic state. Pay without the coordinator releasing authority. |
| **Rail** (`app.Rail`: the sandbox, Solana USDC, the swap rail) | Proves settlement from its own ledger or chain state. Signs, within its own hard ceilings. | Be overruled by an executor's word. |
| **Coordinator** (`EconomicService`) | The only thing that moves an intent: reserve, begin, authorize, complete, reconcile. | — |
| **Executor** (`ExecutionService`) | Quotes, ranks, orders the steps, scores the result, falls back. | Move money. A fallback runs only after the coordinator shows the intent open again, i.e. the earlier attempt is *proven* to have moved no money. |

## The router

`ExecuteCandidates` turns a request and a list of candidates into a plan, in four steps:

1. **Who could do it.** Candidates are normalised, de-duplicated and screened against the Spend Pass without asking anyone anything:
   a provider the pass forbids is not even asked for a price. Three guards then keep candidates out of the (at most 12) pricing slots:
   `provider_down` (the free health probe found it failing twice in a row; unpaid `402` probes every 15 minutes), `price_outlier`
   (listed at more than 10× the class's median and more than $0.05; $1,000 a call is never paid) and a candidate on a network the intent
   can't pay on. History from `route_executions` (30 days, 100 calls per provider) is attached to what is left.
2. **What each asks right now.** All of them are priced at once, each with a free unpaid request and its own deadline. Once one has priced,
   the others have 1.5 seconds more (`quoteGrace`): a provider still working then is cancelled and reported as `unquotable`
   ("no price within 1.5s of the first provider's"), so a call takes as long as the slow end of the *answers*, not of the slowest of twelve.
   A failure doesn't start that clock, only a price does. A runner that panics fails its own quote and nothing else.
3. **Which the intent's limits allow.** Over budget, wrong network or asset, too slow, too much slippage or price impact, a provider below
   the intent's reliability or quality floor, a live price more than 5% above its own listing (`price_above_listing`) or an outlier.
4. **Ranking.** `routing.Rank` is pure and stable (ties keep the order the caller gave). Real money is always ranked before test money,
   and an observed provider that delivers less than half the time after any other.
   - `CHEAPEST`: the lowest total cost first; the live quote, fees included.
   - `FASTEST`: the shortest expected time to a result first, from Algebra's own measurement of the provider when it has one.
   - `AUTO`: a weighted score: cost 0.30, reliability 0.25, quality 0.15, latency 0.12, trust 0.10, price honesty 0.08. A provider with no
     record is scored on a prior from its source (native 0.90, listed 0.70, unverified 0.40) that fades as real calls come in
     (it counts for three calls; after five, the record outweighs it).
   The best few (`MaxPlanSteps`) become the plan. The response says who was priced, each score by component and why the rest were left out.

Plans are tried in order, and only the coordinator can let the next step run.

### Classes

A catalog lists each endpoint as its own capability (`birdeye.data.get.x402-defi-price`), so an agent that names one is never routed to another.
A *class* gives them one name and one input shape: 14 of them today (`token.price`, `solana.token-risk`, `wallet.balances`, `wallet.risk`,
`web.search`, `web.scrape`, `news.search`, `llm.chat`, `image.generate`, `weather.forecast`, `text.translate`, `ip.geolocate`, `domain.lookup`,
`email.verify`), 661 endpoints across the catalogs, 280 routable. Membership is decided from what a catalog says about an endpoint (its path and
description), which is third-party text: it makes an endpoint a *candidate*, never a trusted one. Each member has an input adapter that renames
or reshapes the class's fields into its own. `GET /api/v1/classes` and `/classes/{id}` list them with listed prices and health; the MCP tool
`algebra.classes` does the same. Ask for the class and Algebra routes; ask for one provider's capability and the call is pinned to it.

## Safety properties, and the tests that pin them

| Property | Pinned by |
|---|---|
| Never pay twice: a fallback is blocked while an earlier payment could still land (the coordinator refuses it even if the executor tried). | `TestExecution_AmbiguousOutcomeBlocksFallbackUntilReconciled`, `TestE2E_FallbackAfterTheRailProvesNothingMoved` |
| A repeat of a committed request is answered from the kept result and pays nothing, or is refused when nothing was kept; never paid again. | `TestReplay_AskingAgainReturnsTheKeptAnswerAndPaysNothing`, `TestExecuteOverHTTP_ARepeatRequestIsAnsweredFromTheKeptResult`, `TestExecution_DoIsIdempotentPerOutcome` |
| A kept answer is sealed at rest, bound to its person, intent and reservation, tamper-evident, expiring, and checked against the asking agent's own pass and the committed result hash before it is returned. | `TestResultVaultKeepsSealedAndReturnsWhatWasKept`, `TestResultVaultNoticesTampering`, `TestReplay_ChecksTheAskingAgentsOwnPass`, `TestReplay_ABodyThatDoesNotMatchTheCommittedHashIsNotReplayed` |
| A lost response is recovered for free by replaying the request with the same idempotency key and no payment. | `TestE2E_LostResponseIsRecoveredWithoutPayingTwice`, `TestRunRecoversTheResultByIdempotentReplay` |
| Terms are re-checked before paying: a changed payee, asset, network or higher price is refused. | `TestRunRefusesToPayWhenTheTermsChanged` |
| A token that calls itself USDC at the wrong address is never paid. | `TestQuotePicksTheCheapestOptionAlgebraCanPay`, `TestCandidateRefusals` |
| The payment header never goes anywhere but the priced endpoint (no redirects), and the payment value never reaches a stored record even if a provider echoes it. | `TestRunNeverFollowsARedirectWithThePayment`, `TestPaymentValueNeverAppearsInAnObservation` |
| Providers are only reachable at public addresses (checked on the dialled IP, after DNS); loopback only on the sandbox's port. | `internal/platform/safehttp` tests |
| Nothing leaves Algebra before approval, and providers the Spend Pass forbids are not even asked for a price. | `TestExecution_NoProviderIsContactedBeforeApproval`, `TestExecution_ProvidersTheSpendPassForbidsAreNotEvenProbed` |
| An agent can't vouch for its own provider: endpoints it supplies are always "found on the open web". | `TestResolveCandidates` |
| The call doesn't wait for the slowest provider, and one slow or crashing provider costs only itself. | `TestRouter_StopsWaitingForProvidersThatPriceAfterTheGraceHasPassed`, `TestRouter_AProviderThatPanicsWhilePricingCostsOnlyItself` |
| The wallet signs a swap only if simulating it shows the wallet's accounts changing as stated and nothing else; a transaction that spends three times the amount, approves a stranger, delivers short or would fail is never signed and no money moves. | `TestRailRefusesToSignWhatTheSimulationShows`, `TestSwapOverHTTP_ATransactionThatDoesMoreThanItSaysIsNeverSigned` |
| Web discovery probes with a sample, never the agent's input, and is bounded per agent. | `TestWebDiscovery_ProbesWithTheClassSampleNeverTheAgentsInput`, `TestWebDiscovery_EachAgentIsHeldToAFewSearchesAnHour` |
| The outcome is recorded even if the caller hangs up, and a paid-for result survives a bookkeeping failure. | `TestExecution_BookkeepingSurvivesTheCallerHangingUp`, `TestExecution_AResultIsNeverLostToABookkeepingFailure` |
| An attempt that never got payment authority reopens instead of sticking in RECONCILING. | `TestEconomic_AttemptThatNeverGotPaymentAuthorityReopens` |
| The record routing learns from follows reconciliation (no stale UNKNOWN). | `TestExecution_ReconciliationBringsTheStoredRecordUpToDate` |
| Response bodies are never persisted in the clear: only hashes, statuses and verdicts, plus the sealed, expiring copy above. | `TestExecution_HappyPathCommitsVerifiesAndSignsAReceipt` |

## Surface

| | |
|---|---|
| `POST /api/v1/execute` | Ask for an outcome and have it done. Body: the intent spec (`capability`, `input`, `budget_max_minor`, `window`, `provider_policy.strategy`, `constraints`, …) plus `providers`, `candidates` (endpoints the agent found) and `store_result`. |
| `POST /api/v1/policy/simulate` | The dry run: takes what `/execute` takes plus `live_quotes`, answers ALLOW / REQUIRE_APPROVAL / DENY with the reasons, the plan and what each provider would meet. Creates nothing, pays nothing. |
| `POST /api/v1/economic-intents/{id}/execute` | Run an intent that already exists (for example one the person just approved). |
| `GET /api/v1/economic-intents/{id}/executions` | Attempts with cost, latency, payment, delivery and quality. |
| `GET /api/v1/economic-intents/{id}/result` | The answer kept for an intent, while it is kept (`/me/…` for the person). `Cache-Control: no-store`; the body is untrusted provider data. |
| `GET /api/v1/classes`, `/classes/{id}` | The classes, and a class's providers with listed prices and health. |
| `GET /api/v1/providers`, `/providers/{id}` | The catalogs as one directory. |
| `POST /api/v1/discover/web` | Search the open web for endpoints no catalog lists (needs `GEMINI_API_KEY`). |
| MCP `algebra.execute`, `execution_status`, `simulate`, `classes`, `discover_providers`, `discover_web`, `spend_pass` | The same, for Claude and other MCP clients, at `/mcp`. See [MCP.md](MCP.md). |

Full request and response schemas: [openapi/execution.yaml](../openapi/execution.yaml).

Responses: `200` delivered (`replayed: true` when it is the kept answer to an earlier identical request); `202` money may have moved and the outcome is
being established (do not retry); `502` every provider tried failed and nothing was paid; `422` no candidate fit the limits (with reasons);
`409`/`403` the intent can't be attempted (held, committed with nothing kept, approval needed, denied); `429` too many web searches.

Configuration: `ECONOMIC_SANDBOX`, `ECONOMIC_PROVIDERS`, `RESULT_RETENTION`, `RESULT_MAX_BYTES`, the catalog and Solana settings below (all in `.env.example`).

### Results are kept so that asking again works

When an outcome is paid for, the provider's answer is sealed (AES-256-GCM under a key derived from the master key and used for nothing else) and kept for
`RESULT_RETENTION` (24 hours by default, 7 days at most, `off` to keep nothing) up to `RESULT_MAX_BYTES` (1 MiB; a larger answer is not kept, never truncated).
Asking again for a committed outcome then returns it with `replayed: true`: nothing runs, nothing is paid, and the receipt is the same. A request can say
`store_result: false` to have its answer not kept, and a repeat is then refused as already committed. The kept answer is returned only after the asking agent's
own pass is checked again and it matches the committed reservation and the result hash the coordinator holds. Erasing an account deletes its kept answers.

## Discovery: the catalogs (`providers/catalog`, `providers/paysh`, `providers/bazaar`)

Algebra reads four public catalogs of pay-per-call APIs and serves them as one directory (1,622 providers on 2026-10-07):

| Catalog | Read from | What it gives |
|---|---|---|
| Pay.sh | `https://pay.sh/api/catalog` and each provider's `…/index.md` | 75 providers (Google Cloud through the Solana Foundation's gateway, Birdeye, Nansen, Quicknode, …) with their endpoint tables and listed prices. Mainnet only. |
| Circle's Agent Marketplace | `GET https://api.circle.com/v2/x402/discovery/resources` | 27 providers, about 700 endpoints, with the payment terms each one publishes and usually an input JSON Schema. Kept: plain x402 in Circle's USDC on Solana, no browser sign-in. |
| PayAI's bazaar | `GET https://facilitator.payai.network/discovery/resources` | 1,152 providers that settle through PayAI's facilitator; the source of devnet-payable providers today (14). |
| Coinbase's x402 Bazaar | `GET https://api.cdp.coinbase.com/platform/v2/x402/discovery/resources` | 35,000 endpoints from every chain; the list ignores the network filter and is ordered by use, so Algebra reads the first five pages (about 15 MB, at most twice an hour) and keeps what accepts USDC on Solana with the plain `exact` scheme: 368 providers, each with how many paid calls and distinct payers it had in 30 days. |

Circle's, PayAI's and Coinbase's listings are the same x402 discovery format, read by one generic reader (`providers/bazaar`) with a profile per directory.
For each endpoint it keeps the cheapest plain-x402 USDC option on each Solana cluster, so one endpoint can be payable on mainnet and devnet at different prices.

* Provider IDs are `paysh:<fqn>`, `circle:<slug>`, `payai:<slug>` and `cdp:<host>`. They are what a Spend Pass's allowed providers, a reservation and a
  receipt name. In Coinbase's open directory a provider is named by the host its endpoints are called at, not by the name it gives itself: anyone can list
  under any name, and an ID a stranger can choose would let it take another's place in an allow-list. Capability IDs come from the provider, method and path.
* Every provider lists the clusters it can be paid on (`networks`); `?network=solana` or `solana-devnet` keeps those. An execution's
  `constraints.allowed_networks` restricts where it may pay.
* Everything a catalog says is third-party data: fetched through the SSRF-safe client with a body limit, validated and bounded field by field, one bad entry
  never sinks the rest, text is shown and never followed. A listing is not an endorsement and a listed price is not a quote: the executor prices each endpoint
  with an unpaid request and checks the terms again at payment. In practice they differ (Birdeye's "free" listing on Pay.sh asks 0.003 USDC).
* Each catalog is cached (10 minutes; Coinbase's 30) and served stale, labelled as such, when it is down (6 hours; Coinbase's 24); a catalog that can't be
  read leaves the others listed. The class index is built in the background when the API starts, so the first request doesn't wait for it.
* Endpoints whose path has `{parameters}` (also `:param` and `{id}:action`) are callable: each parameter is a field of the input, escaped as one path
  segment; `.`, `..`, slashes and control characters are refused.

Settings: `PAYSH_*`, `CIRCLE_*`, `PAYAI_*`, `CDP_*` (`…_ENABLED=off`, `…_DISCOVERY_URL`; see `.env.example`).

**Monid (`providers/monid`), listed only.** Monid sells 624 tool endpoints from 24 providers (Apify scrapers, search, enrichment) behind one API key and
bills them from a prepaid Monid balance. Its connectors are open source (github.com/monid-ai/monid), so `cmd/monid-import` reads them, by pattern and without
running them, into a snapshot built into the binary: names, categories, method and path, and a USD price where the provider bills in dollars (241 endpoints;
the rest bill in vendor credits and are left unpriced rather than guessed). They appear in the directory as `monid:<provider>` with `billing: monid-balance`,
next to the x402 providers, so a person can compare, and are never routed or paid: nothing in Algebra can settle a Monid balance. `providers/monid/client.go`
speaks Monid's API (`/discover`, `/inspect`, `/run`, `/runs/{id}`) for when a key and a rail exist; it is not wired into the API. `MONID_CATALOG=off` hides them.

Checked against the live sites with `cmd/x402-dryrun` (sends nothing): Google Vision through Pay.sh's gateway, Birdeye, Exa, Vybe and Nansen all answer with x402 v2
on Solana mainnet in Circle's USDC with a sponsor paying fees, and the rail builds a valid payment for each. Nansen's 2 USDC call is refused by the rail's 1 USDC
ceiling, as it should be.

### The open web (`providers/webdiscovery`, `app.DiscoverWeb`)

For work no catalog lists, `POST /api/v1/discover/web` (MCP `algebra.discover_web`) asks Gemini, with Google Search grounding, for endpoints that charge by x402.
A model can invent a URL or be steered by the page it read, so it is trusted with one thing, the address of an endpoint, and the address is checked the only
way that means anything: each find is asked, with a free unpaid request, what it charges. The probe carries the class's sample input or nothing, **never the
agent's input**, so nobody the agent hasn't chosen learns what it wants. A find that answers with x402 terms Algebra can pay is marked `verified` with its price and
network; the others are returned with the reason. Nothing is paid or chosen: the agent passes the ones it wants to `/execute` as `candidates`, where they are
unverified web finds that the Spend Pass, the new-provider rule and the price guards decide on like any other. Needs `GEMINI_API_KEY`, a Spend Pass, and is
limited to ten searches an hour per agent.

## The Solana rails

### x402 (`providers/solanax402`)

Pays x402 `exact` on Solana in USDC from a wallet Algebra controls. It builds the transaction the x402 spec describes (a v0 transaction, sponsor as fee payer,
`[SetComputeUnitLimit, SetComputeUnitPrice, TransferChecked, Memo]`), signs as the payer only, and returns it as the payment header (`PAYMENT-SIGNATURE` for v2,
`X-PAYMENT` for v1). The provider's sponsor adds its signature and submits. Usage-based (`upto`) options are paid through the Solana Foundation's
payment-channels program (`providers/paychan`): the wallet escrows the provider's ceiling in a channel, the provider settles what the call cost from a signed
voucher, and the rest is refunded in the same instruction. The coordinator commits what the chain proves, not the ceiling.

* **Refuses to sign** anything but Circle's real USDC mint on the configured cluster; anything over the attempt's hold or over `SOLANA_MAX_PAYMENT_USDC` (a hard
  ceiling in the rail itself, default 1 USDC); a payee whose USDC account doesn't exist; a wallet that can't cover it; a sponsor that is the payer.
* **Proves settlement from the chain, not from anyone's word.** The payer's signature on the transaction is the payment's identity, so the payment is found on
  chain even when the provider's response (and with it the sponsor's signature) was lost. `NOT_SETTLED` is only claimed when the transaction landed and failed,
  or when its blockhash has expired at a *finalized* height and a finalized scan finds nothing, and never from a scan that couldn't reach back far enough.
* **Wrong cluster is fatal.** The rail checks the RPC node's genesis hash at startup; paying "mainnet" through a devnet node (or the reverse) stops startup.
  A node that is merely unreachable leaves the rail out, loudly.
* Mainnet needs `SOLANA_ALLOW_MAINNET=yes` in addition to configuring a mainnet wallet.

**Both clusters at once.** `SOLANA_DEVNET_KEYPAIR_FILE` and `SOLANA_MAINNET_KEYPAIR_FILE` (each with an optional `_RPC_URL`) run a devnet rail
(`x402-solana-devnet`) and a mainnet rail (`x402-solana`) side by side, one wallet each. A payment goes to the rail of its own cluster, so devnet terms are
never paid from the mainnet wallet. `GET /api/v1/rails` (signed in) shows, per cluster, whether a wallet is configured, its address, its USDC balance and the
per-payment ceiling; the console's network switch reads it. Single-cluster settings (`SOLANA_CLUSTER`, `SOLANA_RPC_URL`, `SOLANA_KEYPAIR_FILE`,
`SOLANA_MAX_PAYMENT_USDC`) still work; a cluster configured twice stops startup.

### Swaps: buy a token with USDC (`providers/jupiter`)

`solana.swap` (execution type `solana_swap`), off unless `JUPITER_SWAP_ENABLED=on` and a verified mainnet wallet exists: Jupiter has no devnet, and a swap is never
attempted on a cluster that couldn't be verified. Input: `{"output_mint": "<token mint>", "amount_usdc": "2.50", "max_slippage_bps": 50}`. Only buying with USDC is
supported: the Spend Pass is a USDC budget, and selling a token or buying SOL would move value it doesn't measure.

Jupiter hands back an opaque transaction across many programs, and signing it on trust would let a hostile or compromised API drain the wallet. So the rail
never reads the instructions. It simulates the transaction against the node and compares the wallet's accounts before and after, and signs only if:

* USDC falls by no more than the amount authorized, and the bought token rises by at least the quote less the slippage;
* SOL falls by at most 0.005 (fees, rent for a new token account);
* every other token account of the wallet holds at least what it held;
* no token account of the wallet changes owner, gains a delegate or a close authority, is frozen or is closed (an approval gives someone the balance later,
  which no balance check can see);
* the wallet is the fee payer and a required signer, and the transaction has an expiry (so its absence can later be proven).

Settlement is read from the chain by the signature (ours alone, as fee payer): settled by what left the wallet's USDC account, not settled only when it landed
and failed or its blockhash expired at a finalized height and it is nowhere on chain. The rail has its own ceilings whatever any pass allows (5 USDC a swap,
`JUPITER_MAX_SWAP_USDC`; 3% slippage), and the runner holds the order to the quote: the same amount, a price within the slippage, and Jupiter's own minimum
output no looser than the slippage allowed. Settings: `JUPITER_SWAP_ENABLED`, `JUPITER_API_KEY` (the keyless tier is five requests a window), `JUPITER_MAX_SWAP_USDC`,
`JUPITER_MAX_SLIPPAGE_BPS`, `JUPITER_BASE_URL`. **Use a wallet made for it:** the rail judges the accounts the wallet has, so what it holds is what is protected.
A first swap to Jupiter waits for the person unless the pass allows new providers.

## The console's agent chat

The console has its own agent (`/console/agent`), a chat that does what an external agent does through the API: it searches the catalogs and classes, reads a
provider's endpoints, and pays for one call through `POST /api/v1/execute`.

* It acts with the signed-in session's console agent, which has no Spend Pass of its own. The person picks one of their active USDC passes in the chat, and the
  execution names it as `spend_pass_id`. Only the console agent may do this, and only with a pass of the same person (`executorFor` in
  `internal/api/v1/economic_execute.go`); it then spends as that pass's agent, with exactly that pass's limits. Any other agent naming a pass is refused.
* Every payment is restricted to the network picked at the top of the console (`constraints.allowed_networks`).
* A call at or above the pass's approval line comes back `409` with the `intent_id`. The chat shows an approval card; the person approves or cancels with their
  session (`/me/economic-intents/{id}/approve|cancel`), and the agent then runs the approved intent. The agent can't approve.
* The chat makes at most 3 paid calls per message and at most 5 USDC per call, on top of the pass and the rail's ceiling. Provider text and responses reach
  the model as bounded, labelled third-party data.

### Tools

| | |
|---|---|
| `go run ./cmd/solana-wallet` | Shows the configured wallet: cluster check, address, balances, per-payment limit. Read-only. |
| `go run ./cmd/solana-wallet -new -out FILE` | Writes a new keypair file (never overwrites; `.data/` is gitignored). |
| `go run ./cmd/x402-dryrun -url URL [-method M] [-body JSON]` | Prices a real x402 resource, builds and signs the payment exactly as the rail would, has a node **simulate** it, and discards it. Works with an unfunded wallet and says what would be refused. **Sends nothing.** |
| `go run ./cmd/demo-provider` | Paid x402 APIs on devnet that behave like real ones: honest, slow, down, overcharging, a honeypot, and a usage-billed LLM. `-print-config` prints the `ECONOMIC_PROVIDERS` to point the API at them. |
| `go run ./cmd/verify-intent …` | Verifies an Intent Receipt independently. |

### Runbook: the first real payment

1. `go run ./cmd/solana-wallet -new -out .data/solana-devnet.json` (or point `SOLANA_KEYPAIR_FILE` at your own key).
2. Fund it. Devnet: faucet.circle.com (Solana Devnet). Mainnet: send a small amount of USDC (say 0.05) to the printed address. It needs no SOL for x402 payments
   (the provider's sponsor pays fees); a swap needs about 0.005 SOL.
3. Set the wallet (`SOLANA_DEVNET_KEYPAIR_FILE` or `SOLANA_MAINNET_KEYPAIR_FILE`), a small `SOLANA_MAX_PAYMENT_USDC` (for example `0.01`), and for mainnet
   `SOLANA_ALLOW_MAINNET=yes`.
4. `go run ./cmd/solana-wallet` confirms the cluster and the balance.
5. `go run ./cmd/x402-dryrun -url <provider>` confirms the provider's terms are ones the rail accepts and the transaction is well-formed. Do this for every new
   provider before its first real call.
6. `make dev-up`, `go run ./cmd/api`, sign in to the console and create a Spend Pass **in USDC** (Console → Spend passes). The token is shown once.
7. With that token, `POST /api/v1/execute` with `{"capability":"token.price","input":{"mint":"So111…"},"budget_max_minor":10000}`, or first
   `POST /api/v1/policy/simulate` with `"live_quotes":true` to see who would be paid. A provider you have never paid is capped at $0.05 per call by default
   (the pass's `new_providers` rule): approve it in the console or set the pass to `allow`.
8. `go run ./cmd/verify-intent -api http://localhost:8080 <intent id>` checks the receipt, and the transaction signature in it is on the Solana explorer.

## What is real, what is not (as of this commit)

* **Real, and exercised against real HTTP:** the executor and router, the x402 runner (v1 body and v2 header challenges), the coordinator, SSRF-safe HTTP,
  receipts, quality evaluation, fallback, result replay, the `/mcp` endpoint, the four payable catalogs and Monid's listing.
* **Real, and checked against live Solana clusters (read-only):** the address derivation (5 of 5 associated token accounts the mainnet network created match
  ours), the transaction encoding (a devnet node ran our compute-budget instructions and our `TransferChecked`, failing only on the empty token accounts), the
  payment-channels encoders against a real devnet channel (same PDA, accounts and bytes), and, with `x402-dryrun`, live x402 v2 providers on mainnet priced, parsed and
  accepted. Jupiter's real quote answer is a test fixture (`providers/jupiter/testdata`), and its own minimum-output rounding matches ours.
* **Real payments on devnet, through Algebra:** x402 `exact` calls to the demo providers and a metered `upto` call through the payment-channels program
  (open, then settle and refund from a voucher), each chosen by the router and passed by the firewall, receipts committed at the amount the chain proves.
  Transactions in [DEMO.md](DEMO.md#proof-real-devnet-payments-through-algebra-2026-10-07).
* **Not yet real:** a mainnet payment (the demo's mainnet rail is configured and dry-runs against live providers; the wallet is the operator's to fund, runbook
  above) and a swap landing, verified on a fake cluster only (`providers/jupiter/jupitertest`).
* **Simulated:** the sandbox rail and sandbox provider, which are for local runs and tests. Their receipts are marked `test`.
* **Not built:** cross-chain transfers (CCTP) and any EVM rail, so Base-only x402 providers are listed but not payable; selling a token or buying SOL; a recipient
  allow-list for passes. These are held back on purpose: a burn to a wrong address is irrecoverable, and there is nothing yet on the EVM side to spend what is bridged.
* **Known limits:** the answer is kept for a limited time only (a repeat after that is refused as already committed); the quote probe sends the agent's input to
  every candidate priced (up to twelve), so for sensitive input name the provider; Coinbase's directory is read to its first five pages, so its long tail of
  rarely-used endpoints is not listed.
