# Execution: Algebra does the work

Algebra's economic coordinator (`docs/ECONOMIC_COORDINATION.md`) decides whether money may move. This layer
does the work around it: it finds who can do a job, prices them, pays through Algebra's own wallet, makes the
call, checks the result and signs a receipt. The agent never holds a key, a card or a payment value.

```
agent: "get token risk for X, at most 0.05 USDC"
   │
   ▼  POST /api/v1/execute        (or the MCP tool algebra.execute)
intent  ──►  candidates  ──►  quotes  ──►  plan  ──►  attempt(s)  ──►  verify  ──►  receipt
 (econ)     (routing)       (x402       (ranked)    reserve → begin     (schema,     (signed,
                             probe)                  → pay → call →      quality)     JWKS)
                                                     complete → reconcile
```

## The vocabulary (`internal/domain/routing`)

| Type | Meaning |
|---|---|
| `Capability` | A kind of outcome that can be bought (`solana.token-risk`): its kind, evaluator and output schema. |
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
| **Runner** (`providers/x402client`) | Prices a candidate; makes the HTTP call; reports what it saw. | Decide economic state. Pay without the coordinator releasing authority. |
| **Rail** (`app.Rail`: the sandbox, and Solana USDC) | Proves settlement from its own ledger or chain state. | Be overruled by an executor's word. |
| **Coordinator** (`EconomicService`) | The only thing that moves an intent: reserve, begin, authorize, complete, reconcile. | — |
| **Executor** (`ExecutionService`) | Orders the steps, scores the result, falls back. | Move money. A fallback runs only after the coordinator shows the intent open again, i.e. the earlier attempt is *proven* to have moved no money. |

## Safety properties, and the tests that pin them

| Property | Pinned by |
|---|---|
| Never pay twice: a fallback is blocked while an earlier payment could still land (the coordinator refuses it even if the executor tried). | `TestExecution_AmbiguousOutcomeBlocksFallbackUntilReconciled`, `TestE2E_FallbackAfterTheRailProvesNothingMoved` |
| A repeat of a committed request is refused, not paid again. | `TestE2E_RealPaidCallEndToEnd`, `TestExecution_DoIsIdempotentPerOutcome` |
| A lost response is recovered for free by replaying the request with the same idempotency key and no payment. | `TestE2E_LostResponseIsRecoveredWithoutPayingTwice`, `TestRunRecoversTheResultByIdempotentReplay` |
| Terms are re-checked before paying: a changed payee, asset, network or higher price is refused. | `TestRunRefusesToPayWhenTheTermsChanged` |
| A token that calls itself USDC at the wrong address is never paid. | `TestQuotePicksTheCheapestOptionAlgebraCanPay`, `TestCandidateRefusals` |
| The payment header never goes anywhere but the priced endpoint (no redirects), and the payment value never reaches a stored record even if a provider echoes it. | `TestRunNeverFollowsARedirectWithThePayment`, `TestPaymentValueNeverAppearsInAnObservation` |
| Providers are only reachable at public addresses (checked on the dialled IP, after DNS); loopback only on the sandbox's port. | `internal/platform/safehttp` tests |
| Nothing leaves Algebra before approval, and providers the Spend Pass forbids are not even asked for a price. | `TestExecution_NoProviderIsContactedBeforeApproval`, `TestExecution_ProvidersTheSpendPassForbidsAreNotEvenProbed` |
| An agent can't vouch for its own provider: endpoints it supplies are always "found on the open web". | `TestResolveCandidates` |
| The outcome is recorded even if the caller hangs up, and a paid-for result survives a bookkeeping failure. | `TestExecution_BookkeepingSurvivesTheCallerHangingUp`, `TestExecution_AResultIsNeverLostToABookkeepingFailure` |
| An attempt that never got payment authority reopens instead of sticking in RECONCILING. | `TestEconomic_AttemptThatNeverGotPaymentAuthorityReopens` |
| The record routing learns from follows reconciliation (no stale UNKNOWN). | `TestExecution_ReconciliationBringsTheStoredRecordUpToDate` |
| Response bodies are never persisted: only hashes, statuses and verdicts. | `TestExecution_HappyPathCommitsVerifiesAndSignsAReceipt` |

## Surface

| | |
|---|---|
| `POST /api/v1/execute` | Ask for an outcome and have it done. Body: the intent spec (`capability`, `input`, `budget_max_minor`, …) plus `providers` (names configured on the server) and/or `candidates` (endpoints the agent found). |
| `POST /api/v1/economic-intents/{id}/execute` | Run an intent that already exists (for example one the person just approved). |
| `GET /api/v1/economic-intents/{id}/executions` | Attempts with cost, latency, payment, delivery and quality. |
| MCP `algebra.execute`, `algebra.execution_status` | The same, for Claude and other MCP clients. |

Responses: `200` delivered; `202` money may have moved and the outcome is being established (do not retry);
`502` every provider tried failed and nothing was paid; `422` no candidate fit the limits (with reasons);
`409`/`403` the intent can't be attempted (held, committed, approval needed, denied).

Configuration: `ECONOMIC_SANDBOX`, `ECONOMIC_PROVIDERS` (see `.env.example`). Migration `0015_route_executions.sql`.

## Discovery: the catalogs (`providers/catalog`, `providers/paysh`, `providers/circleagents`)

Algebra reads two public catalogs of pay-per-call APIs and serves them as one directory:

| Catalog | Read from | What it gives |
|---|---|---|
| Pay.sh | `https://pay.sh/api/catalog` and each provider's `https://pay.sh/api/<fqn>/index.md` | 75 providers (Google Cloud through the Solana Foundation's gateway, Birdeye, Nansen, Quicknode, …) and their endpoint tables with listed prices. |
| Circle's Agent Marketplace | `GET https://api.circle.com/v2/x402/discovery/resources` | Every endpoint with the payment terms its provider publishes and usually an input JSON Schema. Algebra keeps what accepts Circle's USDC on Solana mainnet as plain x402 and needs no browser sign-in: 27 providers, about 700 endpoints. |

* Provider IDs are `paysh:<fqn with / as .>` and `circle:<slug>`. They are what a Spend Pass's allowed providers, a
  reservation and a receipt name. Capability IDs come from the provider, method and path
  (`birdeye.data.get.x402-defi-price`, `circle.birdeye.get.x402-defi-price`), so an agent can ask for an endpoint
  by capability alone and Algebra knows whose it is.
* Everything a catalog says is third-party data: fetched through the SSRF-safe client with a body limit, validated
  and bounded field by field, one bad entry never sinks the rest, text is shown and never followed. A listing is not
  an endorsement and a listed price is not a quote: the executor prices each endpoint with an unpaid request and
  checks the terms again at payment. In practice they differ (Birdeye's "free" listing on Pay.sh asks 0.003 USDC).
* Each catalog is cached for 10 minutes and served stale, labelled as such, for up to 6 hours when it is down; a
  catalog that can't be read leaves the others listed.
* Endpoints whose path has `{parameters}` are listed but not callable yet.

Surface: `GET /api/v1/providers?q=&category=&source=`, `GET /api/v1/providers/{id}`, the MCP tool
`algebra.discover_providers`, and the console's Providers page. `POST /api/v1/execute` accepts catalog provider IDs in
`providers`, or a catalog capability with no provider named. Settings: `PAYSH_*`, `CIRCLE_*` (see `.env.example`).

Checked against the live sites with `cmd/x402-dryrun` (sends nothing): Google Vision through Pay.sh's gateway, Birdeye,
Exa, Vybe and Nansen all answer with x402 v2 on Solana mainnet in Circle's USDC with a sponsor paying fees, and the rail
builds a valid payment for each. Nansen's 2 USDC call is refused by the rail's 1 USDC ceiling, as it should be.

## The Solana rail (`providers/solanax402`)

Pays x402 `exact` on Solana in USDC from a wallet Algebra controls. It builds the transaction the x402
spec describes (a v0 transaction, sponsor as fee payer, `[SetComputeUnitLimit, SetComputeUnitPrice,
TransferChecked, Memo]`), signs as the payer only, and returns it as the payment header (`PAYMENT-SIGNATURE`
for v2, `X-PAYMENT` for v1). The provider's sponsor adds its signature and submits.

* **Refuses to sign** anything but Circle's real USDC mint on the configured cluster; anything over the
  attempt's hold or over `SOLANA_MAX_PAYMENT_USDC` (a hard ceiling in the rail itself, default 1 USDC); a payee
  whose USDC account doesn't exist; a wallet that can't cover it; a sponsor that is the payer.
* **Proves settlement from the chain, not from anyone's word.** The payer's signature on the transaction is the
  payment's identity, so the payment is found on chain even when the provider's response (and with it the
  sponsor's signature) was lost. `NOT_SETTLED` is only claimed when the transaction landed and failed, or when
  its blockhash has expired at a *finalized* height and a finalized scan finds nothing, and never from a scan
  that couldn't reach back far enough.
* **Wrong cluster is fatal.** The rail checks the RPC node's genesis hash at startup; paying "mainnet" through a
  devnet node (or the reverse) stops startup. A node that is merely unreachable leaves the rail out, loudly.
* Mainnet needs `SOLANA_ALLOW_MAINNET=yes` in addition to `SOLANA_CLUSTER=mainnet`.

Settings: `SOLANA_CLUSTER`, `SOLANA_RPC_URL`, `SOLANA_KEYPAIR_FILE` (preferred) or `SOLANA_KEYPAIR`,
`SOLANA_MAX_PAYMENT_USDC`, `SOLANA_ALLOW_MAINNET` (see `.env.example`).

### Tools

| | |
|---|---|
| `go run ./cmd/solana-wallet` | Shows the configured wallet: cluster check, address, balances, per-payment limit. Read-only. |
| `go run ./cmd/solana-wallet -new -out FILE` | Writes a new keypair file (never overwrites; `.data/` is gitignored). |
| `go run ./cmd/x402-dryrun -url URL [-method M] [-body JSON]` | Prices a real x402 resource, builds and signs the payment exactly as the rail would, has a node **simulate** it, and discards it. Works with an unfunded wallet and says what would be refused. **Sends nothing.** |
| `go run ./cmd/verify-intent …` | Verifies an Intent Receipt independently. |

### Runbook: the first real payment

1. `go run ./cmd/solana-wallet -new -out .data/solana-devnet.json` (or point `SOLANA_KEYPAIR_FILE` at your own key).
2. Fund it. Devnet: faucet.circle.com (Solana Devnet). Mainnet: send a small amount of USDC (say 0.05) to the printed
   address. It needs no SOL.
3. Set `SOLANA_CLUSTER`, `SOLANA_KEYPAIR_FILE`, a small `SOLANA_MAX_PAYMENT_USDC` (for example `0.01`), and for mainnet
   `SOLANA_ALLOW_MAINNET=yes`.
4. `go run ./cmd/solana-wallet` confirms the cluster and the balance.
5. `go run ./cmd/x402-dryrun -url <provider>` confirms the provider's terms are ones the rail accepts and the
   transaction is well-formed. Do this for every new provider before its first real call.
6. `make dev-up`, `go run ./cmd/api`, sign in to the console, then create a Spend Pass **in USDC** (the console
   form only offers INR today, so use the API from your signed-in session):
   `POST /api/v1/me/passes` with `{"label":"first x402","currency":"USDC","budget_minor_units":50000,
   "max_per_purchase_minor_units":10000,"allowed_categories":["digital_services"],
   "allowed_merchants":["95.216.126.169.sslip.io"],"expires_in_days":1}`. Amounts are micro-USDC (50000 = 0.05).
   The response contains the agent token, once.
7. With that token, `POST /api/v1/execute` with `{"capability":"solana.token-risk","input":{"mint":"So111…"},
   "budget_max_minor":10000,"candidates":[{"endpoint":"https://95.216.126.169.sslip.io/v1/token-check",
   "method":"GET","network":"solana"}]}`. (A candidate's provider name defaults to its host, which is what the pass's
   `allowed_merchants` matches.)
8. `go run ./cmd/verify-intent -api http://localhost:8080 <intent id>` checks the receipt, and the transaction
   signature in it is on the Solana explorer.

## What is real, what is not (as of this commit)

* **Real, and exercised against real HTTP:** the executor, the x402 runner (v1 body and v2 header challenges),
  the coordinator, SSRF-safe HTTP, receipts, quality evaluation, fallback.
* **Real, and checked against live Solana clusters (read-only):** the address derivation (5 of 5 associated token
  accounts the mainnet network created match ours), the transaction encoding (a devnet node ran our compute-budget
  instructions and our `TransferChecked`, failing only on the empty token accounts), and, with `x402-dryrun`, a
  live x402 v2 provider on mainnet priced, parsed and accepted.
* **Verified on a fake cluster only:** a full payment landing and being settled. The fake provider's verifier is
  written from the x402 spec independently of the code that builds payments, and the rail passes it for v1 and v2.
  **No real payment has been made.** That step needs a funded wallet and is the operator's to take (runbook above).
* **Simulated:** the sandbox rail and sandbox provider, which are for local runs and tests. Their receipts are
  marked `test`.
* **Real, and read live:** the Pay.sh and Circle Agent Marketplace catalogs (see Discovery above).
* **Not built:** other discovery sources (x402 facilitators' Bazaar, the open web); the router that ranks
  candidates (candidates are tried in the order given); path parameters for templated endpoints; Jupiter; CCTP.
* **Not verified here:** `internal/platform/postgres/execution_repo.go` and migration 0015 compile and were
  reviewed, but their tests need a database (`make dev-up && make test-integration`).
* **Known limit:** the result of a call is returned once and not stored, so a repeated request for a committed
  outcome is refused and the caller must keep what it got. An encrypted, short-lived result cache would lift that.
