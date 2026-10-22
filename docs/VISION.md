# Vision: one vault, one pass, every paid API

Paying per use is how software buys things from now on: an agent needs a price, a search, a model call or a data feed,
and pays a few cents for exactly that. Algebra is the place where that spending is **safe, simple and provable**,
for one person with one agent and for a company with a hundred.

Three things, and only three:

| | What it is | What the person sees |
|---|---|---|
| **Vault** | The USDC an owner has put in. One vault, whichever chain the money came from. | A balance. |
| **Spend Pass** | What an agent holds: a budget drawn from the vault, and the rules it must obey (when to ask, per-call cap, providers, speed). | A card with a budget and a line like "Asks every time". |
| **Receipt** | Signed proof of each payment: who paid whom, how much, on which chain. | A link anyone can verify. |

Everything else (routing, guards, the dry run, the kill switch, catalogs, verification) exists to make those three
trustworthy. It is not a fourth thing the person has to learn.

## The promise

An agent can spend, but only what you gave it, only the way you said, and you can stop it with one click. Every payment
comes with a receipt. Nobody holds a key they shouldn't.

## One vault, Solana and Base

Today a Spend Pass is funded from a Solana wallet (the Anchor vault on devnet, or the test payer). The goal is one
**vault per owner**, created with the account (no step to learn), with two ways in:

- a Solana address and a Base address, both shown on the vault, both USDC;
- a deposit on either chain credits the same balance.

A pass is a slice of that balance with rules. A payment goes out on whichever chain the seller wants (Solana x402 or
Base x402), and the vault pays it. The person never picks a chain for a payment; they only see the balance and the
receipt.

**What this needs, honestly (none of it exists yet):**

1. A ledger in Postgres: vault balance, holds while a payment is in flight, settled spend, refunds. The pass budget
   check already works this way for reservations; the vault adds the funding side.
2. Deposit detection on both chains (Solana USDC transfers to the vault's address, Base USDC `Transfer` events) with a
   confirmation depth, crediting once.
3. A Base payment rail: x402 `exact` on Base (EIP-3009 `transferWithAuthorization`). Not built; today Algebra pays on
   Solana only.
4. Withdrawal back to the owner's own address, so the vault is never a one-way door.

**The decision that comes before the code: custody.** A vault that holds users' USDC and pays out of a pooled account is
custodial, which is a different legal and security position from "Algebra never holds your funds" (which the Anchor
vault, where only the owner can withdraw, gives today). Options, from lightest to heaviest:

- *Non-custodial per chain:* keep the on-chain vault (Solana program; an equivalent contract on Base) and show both as
  "your vault". Two funding paths, no pooled money, more building.
- *Custodial ledger:* one pooled hot wallet per chain and a ledger. Fastest to build, simplest for the person, needs
  legal review, limits per account, and real key management (KMS, already prototyped) before any real money.
- *Test money only:* a ledger that credits devnet and Base-Sepolia USDC. Safe to ship now, proves the experience, no
  custody question yet. This is the recommended first stage.

## Providers: all of them, properly

"All the providers" means every endpoint an agent can legitimately pay, found, priced and judged in one place.
As measured on 2026-10-12 from the live feeds (approximate, they move daily): Pay.sh lists 1,600+ endpoints; Circle's
Agent Marketplace feed has about 712 plain Solana `exact` endpoints, 69 plain Base-only ones and about 1,928 that need
Circle Gateway; PayAI's and Coinbase's bazaars add more. Today Algebra routes to the Solana-payable ones.

What "properly" means for each provider, in order:

1. **Found:** listed from the catalogs with its price, network and input shape.
2. **Probed:** an unpaid request confirms it answers `402` with the price it advertised; one that asks more than it
   lists is refused (already built).
3. **Classed:** mapped to a class of work (token price, web search, LLM…) so the router can compare like with like
   (already built for 14 classes).
4. **Verified:** a person has checked the operator's identity and written down the evidence. A listing never verifies
   itself, and Algebra does not hand out ticks on its own. The next build is a review tool that runs the checks and
   produces a dossier for the person who decides.
5. **Payable:** a rail exists for the seller's network. Solana `exact` works; Base `exact` needs the Base rail above;
   Gateway-only sellers need Circle Gateway.

## Staged plan

| Stage | What | State |
|---|---|---|
| 1 | One concept in the console: passes live on the Spend firewall page; "ask before paying" is one setting on the pass, enforced by the server for every agent and followed by the chat | built (this branch) |
| 2 | Sign-in by emailed code, practice mode, test-money guard | built (this branch) |
| 3 | Vault as a ledger, test money on Solana devnet and Base Sepolia; deposit watchers; vault balance on the pass | design above; not built |
| 4 | Base x402 `exact` rail; ingest the Base-payable endpoints | not built |
| 5 | Review tool and verified ticks for providers | not built |
| 6 | Custody decision, KMS signing, limits, then real money on mainnet | needs the custody decision first |
