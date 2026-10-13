# The payer key

Algebra pays providers from one Solana key per cluster, the **payer**. Under a
Spend Pass vault it never holds a person's money for long: for each payment the
vault's program moves exactly that amount to the payer, the payer signs the
payment, and anything the payment did not spend goes back to the vault. This
page is about where that key comes from and how it is kept.

Two wallets, and only two, are the person's to think about: **their wallet**
(the one they sign in with, or the one Privy made for them) and **the vault**
of each Spend Pass. The payer is Algebra's, and the console does not show it as
a wallet. It appears only where the facts belong: on a receipt, as the signer
of the payment.

## Why the key must not change

The payer is every on-chain pass's `agent`, and the account every pull pays
into (`destination`) is fixed when the pass is made
(`solana-program/programs/spend-pass`, `pull.rs`; `set_agent` changes the agent
only). So a different payer key **cannot pull from any existing vault**. The
owner's money is safe (the owner can withdraw and close the vault with their own
wallet), but the vault is no use for payments until it is made again. For that
reason a key is never replaced quietly.

## Where the key comes from

Exactly one source per cluster. Set in the environment of the API.

| Source | Setting | For |
|---|---|---|
| **Generated** | `SOLANA_DEVNET_SIGNER=generated` | The hosted devnet site and local testing. **Devnet only**: refused on mainnet at start, and by the table's `CHECK`. |
| **A keypair** | `SOLANA_<CLUSTER>_KEYPAIR_FILE` (or `_KEYPAIR`) | Local development; an operator who holds the key. |
| **Cloud KMS** | planned: [MAINNET_READINESS.md](MAINNET_READINESS.md#2-the-signer) | Mainnet. The key never enters the server. |

`backend/start.sh` (the single-container image) sets `SOLANA_DEVNET_SIGNER=generated`
and `SPEND_PASS_REQUIRED=devnet` unless the operator set a wallet or another value, so
the hosted devnet site needs no wallet in its settings.

### The generated key

* Made on the first start, stored in `platform_signers` (migration 0026), sealed
  with AES-256-GCM under a key derived from `ALGEBRA_MASTER_KEY` (HMAC-SHA256 with
  the label `algebra:payer-signer:v1`, the address as additional data). **Back up
  `ALGEBRA_MASTER_KEY`: without it the stored key cannot be opened.**
* Two servers starting together end up with one key (`INSERT … ON CONFLICT DO NOTHING`,
  then both read the winner's).
* It needs **SOL** for the fees of pulls and refunds, and no USDC (the owner's
  transaction that makes a vault also creates the payer's USDC account). A
  generated key asks the devnet faucet for SOL at start and every 30 minutes
  while it is low. The public faucet often refuses; the address is in the log and
  in `GET /api/v1/onchain/networks` (`agent`, with `signer_ready`), and can be
  funded at <https://faucet.solana.com>.
* The log line `GENERATED the devnet payer key …` names the address. **Pin it**
  with `SOLANA_DEVNET_SIGNER_PUBKEY=<address>`.

### What the server will not do

It does not make a new key to get out of trouble. At start it stops, with a
message that says why, when:

* a key is stored but cannot be opened (the master key is not the one it was
  sealed with, or the row was altered);
* no key is stored but **on-chain vaults for the network exist** (the database lost
  the row; a new key could not use those vaults);
* `SOLANA_DEVNET_SIGNER_PUBKEY` is set and the key it would use has another address,
  or no key is stored at all.

A pin on a KMS or keypair key is checked the same way.

## Runbook

**See the key and its balance.** With the server's `DATABASE_URL` and
`ALGEBRA_MASTER_KEY` in the environment:

```
go run ./cmd/solana-wallet -generated
```

**Take the key out of the database** (devnet only; never overwrites a file):

```
go run ./cmd/solana-wallet -generated -out ../.data/solana-devnet.json
```

Run a server with that file (`SOLANA_DEVNET_KEYPAIR_FILE`, and remove
`SOLANA_DEVNET_SIGNER`) and the address is the same, so every vault keeps
working.

**The database was lost, the key was not.** Restore it as a keypair file
(`SOLANA_DEVNET_KEYPAIR_FILE`); the vaults it served work again once their
records are linked, or are made again.

**The master key was lost.** The stored key is gone. On devnet: have the owners
withdraw their vaults, delete the `platform_signers` row and the `onchain_passes`
rows for the network, and start again (a new key is made, and vaults are made
again). Nothing but test money is involved.

**Rotating on purpose.** A new key strands the old vaults: each owner withdraws
and makes a new one. Plan it, announce it, and change the pin in the same step.

## Every devnet payment comes from a vault

`SPEND_PASS_REQUIRED=devnet` means a payment on devnet is paid only from a funded
Spend Pass vault; a pass without one is refused with `ONCHAIN_PASS_REQUIRED`
(`UNFUNDED`, `FROZEN` and the rest name what is wrong). The dry run says so
before anyone presses Pay: the offers stay on the cards, refused, with a link to
create the vault. In production (`APP_ENV=production`) every Solana rail must
be in `SPEND_PASS_REQUIRED`, or the server does not start.

The simulated **sandbox** has no wallet and no vault, and is not affected.
