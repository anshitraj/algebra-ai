# The Spend Pass program on Solana

A Spend Pass is an agent's permission to spend: a budget, a cap per call, an
expiry and a kill switch. Algebra has always enforced it on its server. With the
**Spend Pass program**, the same limits also live on Solana, in a vault funded
from the person's own wallet, and the program checks them on every payment.

**What this gives the person:** an agent's budget that not even Algebra can go
past. Algebra can only pull the exact amount one payment needs, only into its
own payer account, and only while the pass allows it. A compromised Algebra
server can't take more than the pass's limits, can't send the money anywhere
else, and can't pull at all once the owner freezes the pass from their wallet.

| | |
|---|---|
| Program in use | `F1Uu8ynQUviLZHUgejHDMkKmFAJJ7ZKDwU3Svc7Xpj21` ([devnet](https://explorer.solana.com/address/F1Uu8ynQUviLZHUgejHDMkKmFAJJ7ZKDwU3Svc7Xpj21?cluster=devnet)): [`solana-program/pinocchio`](../solana-program/pinocchio/src/lib.rs), Pinocchio 0.11, 54 KB, 0.275 SOL of rent |
| The same program on Anchor | `46gQDUuJCt6VdDMaguGPoD8SKEtFqvpG7ECtpC2F9tdS` ([devnet](https://explorer.solana.com/address/46gQDUuJCt6VdDMaguGPoD8SKEtFqvpG7ECtpC2F9tdS?cluster=devnet)): [`solana-program/programs/spend-pass`](../solana-program/programs/spend-pass/src/lib.rs), Anchor 1.1, 262 KB, 1.331 SOL. Kept to show the optimisation; passes made on it keep working |
| Both | legacy SPL Token, USDC; the same instructions, accounts, layout, errors and events ([why two](#the-pinocchio-version-smaller-cheaper-to-deploy)) |
| Deployed | devnet, 2026-10-09: Pinocchio ([deploy tx](https://explorer.solana.com/tx/3P9kDbeENojXT7bW1T8ot7ZocyTtYMZch48JPHHXepzUSioSAbtRJ9beKSBc3caFn73dZN7yXb7SXCtQqjySDwip?cluster=devnet)), Anchor ([deploy tx](https://explorer.solana.com/tx/3Tr54KZ2j5J7hS7sUSsT351gnyJH59swR9bjrG9dweRB5ueTgpEbLFxyq8ujt28J83cvsoQGSM6wnnHxaX4booBa?cluster=devnet)). Mainnet: not yet, see [Mainnet](#mainnet) |
| Go client | [`backend/internal/platform/solana/spendpass`](../backend/internal/platform/solana/spendpass/spendpass.go) (no SDK: instructions and the account layout encoded by hand, checked against the program) |
| Service | [`backend/internal/app/onchain_passes.go`](../backend/internal/app/onchain_passes.go) |
| CLI | [`backend/cmd/spend-pass`](../backend/cmd/spend-pass/main.go) |
| Console | Spend passes page, "Enforced on Solana" ([`frontend/components/console/onchain-pass.tsx`](../frontend/components/console/onchain-pass.tsx)) |

## The account and the instructions

A pass is a program-derived account at `["pass", owner, id (u64 LE)]`. Its
vault is the pass's associated USDC account, so only the program can move
tokens out of it.

| Field | |
|---|---|
| `owner` | the person's wallet: the only key that can deposit, freeze, change the agent, withdraw, revoke or close |
| `agent` | the only key that can `pull` (for Algebra, the rail's payer) |
| `destination` | the only token account a pull can pay into (for Algebra, the payer's USDC account), fixed at creation |
| `per_call_cap`, `total_budget`, `window_secs` + `window_cap`, `expires_at` | the limits, fixed at creation: an owner can tighten a pass by freezing or revoking it, never loosen it (make a new pass instead) |
| `spent`, `window_start`, `window_spent`, `pulls`, `last_intent` | what the program has counted |
| `frozen`, `revoked` | the kill switch, and the end |

| Instruction | Signer | What it does |
|---|---|---|
| `create_pass(id, terms)` | owner | makes the pass and its vault; checks the terms, that the agent isn't the owner, that the destination isn't the vault |
| `deposit(amount)` | owner | owner's USDC into the vault |
| `pull(amount, intent)` | agent | vault into the destination, only if every limit holds; `intent` (the hash of Algebra's reservation) goes in the event and on the pass |
| `refund(amount, intent)` | the destination's authority | destination back into the vault, giving the budget back; never more than `spent`; allowed in any state |
| `set_frozen(bool)` | owner | the kill switch |
| `set_agent(key)` | owner | rotates the agent key; the old one stops at once |
| `withdraw(amount)` | owner | vault to the owner, in any state |
| `revoke` | owner | ends the pass for good |
| `close_pass` | owner | returns the whole vault to the owner and closes both accounts, returning their rent |

Every change emits an event (`PassCreated`, `Deposited`, `Pulled`, `Refunded`,
`FrozenChanged`, `AgentChanged`, `Withdrawn`, `Revoked`, `Closed`), so a pass's
whole history can be rebuilt from the chain.

## How Algebra uses it

```
owner's wallet ──create_pass + deposit──► Pass vault (owner's USDC)
                                              │
agent ─► /api/v1/execute ─► router picks provider ─► Algebra signs the x402 payment (not yet released)
                                              │
                          pull(exact amount, reservation hash) ──► payer's USDC account   [program checks the limits]
                                              │  confirmed
                          the signed x402 payment is handed over ──► provider ──► facilitator settles it
                                              │
                    attempt ends: reconcile proves from the chain what was spent;
                    anything unspent ──refund──► back into the vault
owner's wallet ──set_frozen(true)──► the next request is refused (403) before anything is reserved
```

- **Pull before pay.** `EconomicService.AuthorizePayment` signs the payment, then
  `OnchainPassService.Fund` pulls exactly its amount and waits for the pull to
  confirm (rebroadcasting it, for up to 20 s). Only then is the payment handed to
  the provider. If the pull is refused (frozen, over a limit, expired, an empty
  vault) the attempt is denied with the program's reason and nothing is paid.
- **The x402 payment is unchanged.** Facilitators check that a payment is a
  plain `TransferChecked` from the payer, so the program funds the payer instead
  of making the payment itself. Every x402 provider keeps working.
- **Crash-safe.** The pull is written to `onchain_pulls` before it is sent; a
  pull whose fate isn't known is settled from the chain later. Every state move
  is a compare-and-set, so two API processes can never both refund one pull.
- **Refunds are proven.** For each pull whose attempt has ended, the reconciler
  (every 15 s) asks the rail what the payment spent, from chain state: an `exact`
  payment that never landed spent nothing; a usage-based (`upto`) escrow spent
  what the channel paid the provider. The rest goes back to the vault, or to the
  owner if the pass was closed in the meantime.
- **The on-chain kill switch** is read when an intent is created, so a pass its
  owner froze, revoked or closed on chain is refused at once (HTTP 403), like
  Algebra's own kill switch.
- **Binding.** A pass is made for one Spend Pass: its `id` is derived from the
  Spend Pass's ID (`app.PassNumber`), so an on-chain pass made for someone
  else's Spend Pass can't be linked to yours. Linking checks on chain that the
  pass names Algebra's payer as agent and its USDC account as destination, and
  (on mainnet) that the owner is a wallet the person signed in with.

## API (session only)

| | |
|---|---|
| `GET /api/v1/onchain/networks` | where passes can be made, and the program, agent, destination and mint they must name |
| `POST /api/v1/me/passes/{id}/onchain/prepare` `{"network","owner","deposit_minor"}` | an unsigned transaction that makes the pass with the Spend Pass's limits (and funds it) |
| `POST /api/v1/me/passes/{id}/onchain/link` `{"network","address"}` | checks the pass on chain and links it |
| `GET /api/v1/me/passes/{id}/onchain` | the pass as the chain has it, its vault, and its recent pulls |
| `POST /api/v1/me/passes/{id}/onchain/tx` `{"action","amount_minor"}` | an unsigned owner transaction: `freeze`, `unfreeze`, `deposit`, `withdraw`, `revoke`, `close` |
| `DELETE /api/v1/me/passes/{id}/onchain` | forgets the link |

Every transaction comes back unsigned; the wallet signs and sends it. In the
console this is the Wallet Standard (Phantom, Solflare, Backpack), with no SDK
added.

## Configuration

On-chain passes are on wherever a Solana rail runs and the program is deployed
(checked at startup: a cluster without the program is left out and logged).

| Variable | |
|---|---|
| `SPEND_PASS_PROGRAM_ID` | where new passes are made; empty: the Pinocchio program; `off`: disable. Passes on either deployment are honoured whatever this says. The `api-demo-anchor` launch configuration sets the Anchor one |
| `SPEND_PASS_REQUIRED=mainnet,devnet` | on these clusters a pass must be on chain to pay at all, so the payer never spends money that isn't a pass's. **Set it for mainnet in production.** |
| `SPEND_PASS_ANY_OWNER=devnet` | on these clusters the owner needn't be a sign-in wallet (CLI-made wallets for demos). Refused for mainnet. |
| `SPEND_PASS_PRIORITY_MICROLAMPORTS` | priority fee for pulls and refunds (default 10,000) |

The payer needs a little SOL for pull and refund fees (5,000 lamports plus the
priority fee each; x402 payments themselves are sponsored).

## Build, test, deploy

```bash
cargo build-sbf --manifest-path solana-program/Cargo.toml
```

```bash
cargo test --manifest-path solana-program/Cargo.toml
```

The tests are unit tests of every limit (`state.rs`) and LiteSVM tests that run
the built `.so`: the whole life of a pass, every refusal (per-call cap, budget,
window, expiry, frozen, revoked, wrong signer, wrong destination, refund over
spent), agent rotation, and one pass per id.

```bash
solana program deploy solana-program/target/deploy/spend_pass.so --program-id solana-program/target/deploy/spend_pass-keypair.json --keypair .data/solana-devnet.json --upgrade-authority .data/solana-devnet.json -u devnet
```

The program keypair is git-ignored; a copy is at `.data/spend-pass-program-keypair.json`.
Losing it doesn't lose the program, but the upgrade authority key is what can
upgrade it: keep that safe.

## Proof on devnet (2026-10-09)

With the CLI (pass [`ANUXbEW9…`](https://explorer.solana.com/address/ANUXbEW9KyusMHWCQDAiGUDpRqac4SiUs1jYycVuroWy?cluster=devnet)):
[create](https://explorer.solana.com/tx/41pvamo6U5stK21oSLfMV1hvGEZkxoorE8zZKoggbi21xP9QfFjSqzn3rXKsXaRzpEnqXaGVhcgurRq1zuvo4crH?cluster=devnet) ·
[deposit 1 USDC](https://explorer.solana.com/tx/4Wghs4fwiSFJ9k8w2VWKKYGkSRjmRPNnT6g6VvsZhefzsdn8ersQcuNDesKY4UvEmY8C1SF2ETPa3zbRfJKWF3ho?cluster=devnet) ·
[pull 0.002](https://explorer.solana.com/tx/5ziYMe3bMHNCE9VAQCqkQTTdYaoJj2WGB3TyJ1sE9TsU1uC3WTRTFkHDcjTTmPmmqUqfsvJUroT8xgNFzFXsWSPV?cluster=devnet) ·
a pull of 0.02 refused by the program (`OverPerCallCap`) ·
[refund 0.001](https://explorer.solana.com/tx/3N4vrTaSx1vfXsSxe74HndRFMtDfHJb739Fzh69fsPne7XwJDwixj6niKtvsGyDAYxV3jAscWxjcy83mWW7xdTuB?cluster=devnet) ·
[freeze](https://explorer.solana.com/tx/5ZdZnDAf3iNqqb4DPDG1HgvShV4wFtU9U7fxefTtSVnxWBBfnHqtwmouN5TzN924sQykfq4GfNSVuQa8ZhkTKhJx?cluster=devnet) ·
a pull refused while frozen (`PassFrozen`) ·
[unfreeze](https://explorer.solana.com/tx/61xK9sdBimk3s9LnCmwqfwALjmgGg2NzQkj9thun1BxtJ9J2avd3y3iwnpmwZCpgSgiQmwpxNRhi1F8Zyd5WpT8U?cluster=devnet).

Through Algebra's API, a person's Spend Pass put on chain from the owner's
wallet, then an agent's paid request:

| | |
|---|---|
| Pass made and funded by the owner's wallet, from Algebra's prepared transaction | [3NYzS2St…](https://explorer.solana.com/tx/3NYzS2St9FUfqKKLB3Kv1Y77FH8SsiNN8k4Uv2TbLRcU4QoMwAhXuWuMVEjeqwtMJhsv6zyJXwgiZo8C8mUZ1E97?cluster=devnet) |
| `token.price`: the router chose demo:beta (0.001); the program released exactly 0.001 | [pull 25ppbCr1…](https://explorer.solana.com/tx/25ppbCr1t5QSSKSHsa3BUmQHrTvaf5YG15gowsz59t8yfGapEdhLbRLBLdaBDCN7KF59SbUUZ7ukuxKojWLrG6Ho?cluster=devnet) |
| …then the x402 payment to the provider; intent COMMITTED, result delivered | [bdS66fAo…](https://explorer.solana.com/tx/bdS66fAoJx7dN13MjtbJf1AWaBavJWzFVBpXv7g7jmr8xTgWqJipCtW1MsxHd45a21fBFJLfCgDEFhfUkkhDFoZ?cluster=devnet) |
| Owner froze the pass from their wallet; the next request was refused, 403 "frozen on Solana by its owner's kill switch" | [freeze 9h2cLkVe…](https://explorer.solana.com/tx/9h2cLkVePzUNwV5t9PSAj8YFi6YQDoE33skMb2opDd4qSwbB8RdTQQh1YUMKkorw6N4H94oKsz8BT3drAqSHsKM?cluster=devnet) |
| An attempt whose payment was never handed over: its 0.05 pull refunded to the vault by the reconciler | [pull 5hmkuG15…](https://explorer.solana.com/tx/5hmkuG153HorCH67DB2joo3V8ngdBP2v11j2MrdaQoc6GaX9V1ojj2A1y4d9QT2ZLEkxkK5RPbEmBpTeU7wz26kZ?cluster=devnet), [refund 2SVgcgfb…](https://explorer.solana.com/tx/2SVgcgfbKXdpfEfMa6NWFydXRLXyTvdMtz4kSr7NQkcVbPLT2X7x3Ji84CiiK7CzhN5UVcCWyQbEikeseATG5rCP?cluster=devnet) |

To run it yourself: start `demo-provider` and `api-demo` (which sets
`SPEND_PASS_ANY_OWNER=devnet`), make a devnet owner with
`go -C backend run ./cmd/solana-wallet -new -out ../.data/spend-pass-owner-devnet.json`,
send it a little devnet SOL and USDC, then use
`go -C backend run ./cmd/spend-pass sign-send -owner ... -tx <transaction>` as
the owner's wallet for the transactions the API prepares.

## Mainnet

The Pinocchio program, deployed from the same keypair, so the address is the
same as on devnet. Deploying needs 0.275 SOL of rent (refundable: closing the
program returns it), plus as much again held for the upload buffer during the
deploy (returned at the end), so about 0.56 SOL in the deploying wallet. The
Anchor build would need 1.331 SOL of rent and about 2.7 SOL to deploy. Before
real money goes in:

1. Move the upgrade authority to a multisig (Squads), or make the program
   immutable once it is settled (`solana program set-upgrade-authority --final`).
2. Have the program reviewed: it is small (about 400 lines) and tested, but not
   audited.
3. Set `SPEND_PASS_REQUIRED=mainnet`, so every mainnet payment is a pass's.

## The Pinocchio version (smaller, cheaper to deploy)

[`solana-program/pinocchio`](../solana-program/pinocchio/src/lib.rs) is the same program rewritten without Anchor, on [Pinocchio](https://github.com/anza-xyz/pinocchio) 0.11. It takes the **same instruction bytes and accounts**, keeps the pass in the **same 252-byte layout**, returns the **same error numbers** (6000…) and logs the **same events**, so the backend, the CLI and the console work with it unchanged: point `SPEND_PASS_PROGRAM_ID` at it. The Anchor program is untouched; both exist side by side.

| | Anchor | Pinocchio |
|---|---|---|
| Program size | 261,840 bytes | 53,904 bytes (−79%) |
| Rent locked on mainnet (live `solana rent`) | 1.331 SOL | **0.275 SOL** |
| `pull` compute | 20,007 CU | 10,805 CU |
| `create_pass` / `deposit` / `refund` / `close_pass` | 40,969 / 18,178 / 19,951 / 23,527 CU | 28,366 / 9,965 / 10,630 / 14,191 CU |
| Devnet address | `46gQDUuJCt6VdDMaguGPoD8SKEtFqvpG7ECtpC2F9tdS` | [`F1Uu8ynQUviLZHUgejHDMkKmFAJJ7ZKDwU3Svc7Xpj21`](https://explorer.solana.com/address/F1Uu8ynQUviLZHUgejHDMkKmFAJJ7ZKDwU3Svc7Xpj21?cluster=devnet) |

How it was checked (`cargo build-sbf && cargo test` in `solana-program/pinocchio`, after building the Anchor program in `solana-program`):

- **The Anchor program's own LiteSVM tests**, run against the Pinocchio binary with the Anchor crate's instruction builders and account type (`tests/mod.rs`): all 8 pass. The only difference is the error for a broken account constraint (Anchor's 2001/2015 are the runtime's `IncorrectAuthority` here).
- **Side by side** (`tests/compare.rs`): the same transactions on both programs leave the same pass and the same balances; plus attacks the hand-written checks must stop: a forged pass owned by another program (`IllegalOwner`), a pass's bytes at the wrong address (`InvalidSeeds`), a second token account the pass owns that isn't its vault (`InvalidSeeds`), a fake token program (`IncorrectProgramId`), a pass address someone pre-funded to block creation (still created), unsigned and short instructions.
- **7 unit tests** of the layout at the Anchor offsets and every limit.
- **Devnet, CLI:** create, deposit, pull, a pull refused (`OverPerCallCap`), refund, freeze, a pull refused (`PassFrozen`), unfreeze, withdraw, close.
- **Devnet, through Algebra's API** (`api-demo-pinocchio` launch configuration): a Spend Pass put on chain from the owner's wallet ([oVvabhso…](https://explorer.solana.com/tx/oVvabhsoYEYuV4kZMKN8kcaGNkkYAV3fBWoPSphvhw4a3PVesUz6utFPqyxS8jT7RZ63tHr2YdbeyxbH67jztnd?cluster=devnet)); `token.price` funded by a 0.001 pull ([2X2MW2cC…](https://explorer.solana.com/tx/2X2MW2cC5vmYRtBACKaf3mjdosE974inRy8vuX91xP53pYtcmjdxiLbqaQjTa36cRPnSH1RGKrJRfp9Ykj5N9DFY?cluster=devnet)) and delivered; frozen from the wallet, refused with 403; and a usage-based `llm.chat` whose 0.05 escrow pull ([3z8UZ7q6…](https://explorer.solana.com/tx/3z8UZ7q661uQrD9QUCSW2XH8SxvfTnhroeAzkoZfUG9ioVPB1gK9ReXnFxcQc1QM4ALtZziDxhVxauzGso9rNF6V?cluster=devnet)) settled at 0.00396, the other 0.04604 refunded to the vault automatically ([3ngADtHs…](https://explorer.solana.com/tx/3ngADtHsdL5xe9GBmfxFD4dxxYLfKRznFyRSH14SzfbVknrkDKsieMyq7b16Kujgmgu6ccLk1jTP8hwuLaQK8zfC?cluster=devnet)).

One thing found on the way, worth knowing for any Pinocchio 0.11 program: its `Rent` reads only the sysvar's first field and treats it as lamports per byte (the newer rent model). Under the classic model (lamports per byte-year × an exemption threshold of 2.0), that is **half** the real minimum and the new account is refused. This program reads both fields itself (`rent_exempt_minimum`), so it is correct under either.

Deploy cost on devnet, measured: 0.2758 SOL (rent plus fees). Deploying to mainnet needs about 0.56 SOL in the wallet for a moment (the upload buffer is returned), 0.275 SOL of it stays as the program's refundable rent.

## Limits, honestly

- Not audited. The upgrade authority on devnet is one key.
- A pull and its payment are two transactions: between them the money sits in
  the payer's account for a few seconds. The program bounds what can ever reach
  the payer; it can't make the payment itself without breaking x402.
- The owner signs from a browser wallet (Wallet Standard) or the CLI. Signing
  from a Privy embedded wallet inside the console isn't wired yet.
- The window cap is in the program and the API, but the console doesn't offer
  it yet.
