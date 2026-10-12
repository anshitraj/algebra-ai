# Hackathon checklist

For Colosseum, Solana Turbin3 and developer-school reviews. What to show, in
what order, what to say, and what to check before pressing record. Everything
here runs on **Solana devnet** or in the sandbox; nothing touches mainnet.

## One sentence

Algebra is the execution layer between AI agents and paid APIs on Solana: an
agent asks for an outcome and a ceiling, Algebra finds the providers, checks
their real prices and its owner's limits, pays one of them in USDC, and hands
back the answer with a signed receipt, without the agent ever holding a key.

## Before recording

- [ ] `scripts\dev-native.ps1 up` (Postgres `:5433`, Redis `:6380`)
- [ ] Devnet wallet `.data/solana-devnet.json` holds devnet USDC
      ([faucet.circle.com](https://faucet.circle.com)); the demo provider's
      wallet holds devnet SOL ([faucet.solana.com](https://faucet.solana.com))
- [ ] `demo-provider`, the API and the web app are running
      ([DEMO.md](DEMO.md#run))
- [ ] `GET /readyz` is 200; Provider intelligence shows a recent refresh
- [ ] A fresh account for the take (the Quickstart is best from empty)
- [ ] For the on-chain pass: a devnet owner wallet with a little SOL and USDC,
      either in a browser wallet or as `.data/spend-pass-owner-devnet.json`
- [ ] Console switched to **Devnet** where the script says so
- [ ] No API key, pass token or keypair path visible on screen

## Product walkthrough (about 3 minutes)

1. **The problem, on the landing page** (15 s). An agent that can pay has
   three problems: which provider, how to stop it overspending, and whether it
   paid exactly once.
2. **Quickstart** (`/console/quickstart`, 60 s). New account. Create the
   sandbox pass, issue an API key (shown once), run the dry run. Read the
   refusals aloud: one provider is down, one asks more than it lists, one is
   priced like a trap, one changed where it wants to be paid and is blocked.
   Execute. "Committed: paid once. Test payment, no money moved."
3. **Projects & API keys** (20 s). The key is for one project and one
   environment. A sandbox key cannot move devnet or mainnet money. Usage for
   the month is here.
4. **Provider intelligence** (30 s). 6,000 endpoints from four catalogs,
   refreshed every six hours. Listed, Quoted, Tested, Verified and Executable
   are five different facts. Open *Suspicious changes*.
5. **Spend firewall** (30 s). Freeze every pass; ask again; refused. Lift it.
6. **Receipt** (20 s). Copy the receipt into `/verify`. Anyone can check it.

## Technical Solana demonstration (about 4 minutes)

1. **The program** (30 s). Open
   [`46gQ…9tdS` on the explorer](https://explorer.solana.com/address/46gQDUuJCt6VdDMaguGPoD8SKEtFqvpG7ECtpC2F9tdS?cluster=devnet).
   Nine instructions. A pass is a PDA at `["pass", owner, id]`; its vault is
   the pass's own USDC account, so only the program moves tokens out.
2. **Create and fund** (40 s). Spend passes → *Enforced on Solana*. The owner's
   wallet signs one transaction: `CreatePass` + `Deposit`. Show it on the
   explorer.
3. **Pay** (60 s). With the project's devnet API key:

   ```bash
   curl -s $ALGEBRA/api/v1/execute -H "Authorization: Bearer $ALGEBRA_DEVNET_KEY" -H 'content-type: application/json' -d '{"capability":"token.price","input":{"mint":"EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"},"budget_max_minor":10000,"window":"demo-1","constraints":{"allowed_networks":["solana-devnet"]}}'
   ```

   Two transactions: the program's `pull` of exactly the price, then the x402
   payment to the provider. Say plainly: the provider is Algebra's own devnet
   demo provider.
4. **Never twice** (20 s). Send the same request again: `replayed: true`. The
   pass account still has two transactions.
5. **The program says no** (40 s). Freeze the pass from the owner's wallet.
   Ask again: 403, "frozen on Solana by its owner's kill switch". The server
   cannot undo that.
6. **Tests** (30 s). `cargo test --manifest-path solana-program/Cargo.toml`:
   13 unit and 11 LiteSVM tests against the built program.
7. **Receipt** (20 s). The receipt names the pass, the provider, the amount and
   the payment's transaction signature.

Reference run with every link: [DEMO.md](DEMO.md#proof-a-projects-api-key-paying-from-an-on-chain-spend-pass-2026-10-12).

## The business, in five lines

- **Customer:** teams shipping agents that buy data and compute: agent
  startups, agentic-commerce products, Solana applications, API marketplaces.
- **Why now:** x402 made paid APIs callable by machines; four catalogs already
  list thousands of endpoints; nothing sits between an agent and its wallet.
- **What they pay for:** the control plane. A monthly plan by projects and
  on-chain executions. Algebra adds nothing to a provider's price.
- **Why Solana:** sub-cent fees make per-call payments real, and the Spend
  Pass program puts the budget where not even Algebra can exceed it.
- **What is defensible:** the routing record (who delivered, at what price,
  how fast), the trust registry, and limits enforced on chain.

## Commands a reviewer can run

```bash
make test
```

```bash
make test-integration
```

```bash
cargo build-sbf --manifest-path solana-program/Cargo.toml
```

```bash
cargo test --manifest-path solana-program/Cargo.toml
```

```bash
pnpm --dir frontend lint
```

```bash
pnpm --dir frontend build
```

Results of the last full run are in the README under *Tests*.

## Say this, not that

| Say | Not |
|---|---|
| "Real payments on Solana devnet" | "Live payments" |
| "Our own demo provider on devnet" | "A provider" |
| "Mainnet is read-only: discovery, quotes and dry runs" | "Works on mainnet" |
| "Swaps are simulated before signing; tested on a fake cluster" | "Jupiter swaps work" |
| "Not audited" | nothing |
| "Plans are defined and the trial works; there is no checkout yet" | "Subscriptions" |
| "Listed in four catalogs" | "Integrated with" a named provider |

The full list, with the test or transaction behind each claim:
[CLAIMS.md](CLAIMS.md).

## Submission items

- [x] Repository builds and tests from a clean checkout
- [x] README: positioning, problem, architecture, Solana features, setup,
      tests, devnet deployment, limits, demos, roadmap
- [x] Six diagrams ([DIAGRAMS.md](DIAGRAMS.md))
- [x] Program documentation ([SPEND_PASS_PROGRAM.md](SPEND_PASS_PROGRAM.md))
- [x] Devnet transaction evidence ([DEMO.md](DEMO.md))
- [x] Claims supported by tests, and what remains planned ([CLAIMS.md](CLAIMS.md))
- [x] Mainnet readiness, separately ([MAINNET_READINESS.md](MAINNET_READINESS.md))
- [x] Route audit ([ROUTE_AUDIT.md](ROUTE_AUDIT.md))
- [ ] Demo video (record from the scripts above)
- [ ] Pitch deck and go-to-market
- [ ] Team section
- [ ] A hosted deployment and its URL
- [ ] A `LICENSE` file: the site footer says Apache-2.0 and the repository has
      none
- [ ] Wallet-connect step recorded in a browser with a wallet extension
