# The payer key

Algebra pays providers from one Solana key per cluster, the **payer**. This
page is about where that key comes from and how it is kept.

On devnet the payer pays **test money from its own wallet**, and the test-money
guard (`TEST_MONEY_PER_ACCOUNT_PER_DAY`, `TEST_MONEY_PER_DAY`) bounds how much
any one account, or everyone together, can take from it in a day. Keep it
funded with devnet SOL and USDC. With the on-chain vault switched on
(`ONCHAIN_PASSES=on`, off by default) it is different: for each payment the
vault's program moves exactly that amount to the payer, the payer signs the
payment, and anything the payment did not spend goes back to the vault. Real
money, on mainnet, is always paid that way.

The person has no wallet to think about here: sign-in is an emailed code, and
the payer is Algebra's. The console does not show it as a wallet. It appears
only where the facts belong: on a receipt, as the signer of the payment.

## Why the key must not change

(This applies where the on-chain vault is on.) The payer is every on-chain pass's `agent`, and the account every pull pays
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
| **Cloud KMS** | `SOLANA_<CLUSTER>_KMS_KEY_VERSION` | Mainnet (required there with `APP_ENV=production`). The key never enters the server. |

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

### Cloud KMS

The payer is a Cloud KMS **Ed25519** key (algorithm `EC_SIGN_ED25519`); its private
half cannot be read by anyone, Algebra included, and each signature is a KMS call
(logged by Google). The server holds only the key's address.

1. Create a key ring and an asymmetric signing key, purpose `asymmetric-signing`,
   algorithm `ec-sign-ed25519`, protection level `hsm` for mainnet.
2. Give the service account the server runs as the role `roles/cloudkms.signerVerifier`
   **on that key only**: not on the project.
3. Try the key before a server depends on it. `kms-check` prints the key's Solana
   address (the one to fund and to pin), has KMS sign test messages and checks
   the signatures, and confirms a wrong key name is refused; `-tx` also sends one
   memo transaction on devnet with the key as the fee payer:

   ```bash
   GOOGLE_APPLICATION_CREDENTIALS=/path/to/key.json go run ./cmd/kms-check -version projects/…/cryptoKeyVersions/N
   ```

4. Set `SOLANA_MAINNET_KMS_KEY_VERSION=projects/…/locations/…/keyRings/…/cryptoKeys/…/cryptoKeyVersions/N`
   (one exact version; not a key, not "primary"), `SOLANA_ALLOW_MAINNET=yes`, and
   `SPEND_PASS_REQUIRED=mainnet`. The server logs the key's address at start
   (`mainnet payer key … (kms)`). Fund it with a little SOL for fees and set
   `SOLANA_MAINNET_SIGNER_PUBKEY` to it.
5. Credentials are the host's application default credentials: automatic on Google
   Cloud (the service account), or on another host a credentials file at
   `GOOGLE_APPLICATION_CREDENTIALS`. `backend/start.sh` writes `GOOGLE_CREDENTIALS_JSON`
   (a service-account key limited to step 2's role) to a private file for hosts that
   only offer environment variables; where the host supports workload identity
   federation, prefer it, because a service-account key is itself a long-lived secret.

What this does and does not protect. KMS stops the key being stolen and used from
somewhere else later; it does not stop a **compromised server** asking KMS to sign
the wrong things while it is compromised. What bounds that is the Spend Pass
program (a vault pays out only what its owner allowed, and the owner can freeze it),
the service account's single-key permission, and KMS's audit log. Changing the key
version is changing the payer: it strands existing vaults like any other new key.

A KMS error or outage stops a payment (nothing is signed) and a start (no other payer
is made up). Signing is retried briefly on 429 and 5xx; a refusal is not retried.

#### What has been run against real Cloud KMS

On 2026-10-13, on **devnet**, with a `software` Ed25519 key and a service-account
key file, from a development machine. The payer was
[`Drx6tu2fNMiHLKZk8mD1qsDpahvJ3FPCK4iQ8J6b42JV`](https://explorer.solana.com/address/Drx6tu2fNMiHLKZk8mD1qsDpahvJ3FPCK4iQ8J6b42JV?cluster=devnet);
every signature below came from KMS.

| What | Result |
|---|---|
| `kms-check`: read the key, sign, verify, same signature twice, a 1232-byte message, a key name without a version, a version that does not exist | All as expected. A signature took about 40 ms. The refusals carried no response body |
| `kms-check -tx`: one memo transaction with the key as fee payer | [confirmed](https://explorer.solana.com/tx/4nKPXtFE8nQHMjSf4bLtxvPVqVvptoAcD6uTD1XnkewvZwEqBYTcwWUPcvM8pi1chmgR7TJysefYHoC5AfCbm4HP?cluster=devnet) |
| The server started with `SOLANA_DEVNET_KMS_KEY_VERSION` | `/api/v1/onchain/networks` reports `signer_mode: kms` and the address; the same after a restart; the right pin is accepted |
| Starts that must fail: a wrong pin, a KMS key and a keypair file together, no credentials, a version that does not exist | Each refused to start with its reason, and no other key was made |
| A fixed-price payment from a vault (x402 `exact`) | [pull](https://explorer.solana.com/tx/5jV4tbx1aMtGgZeEj8XiiMc4PPMRMZ1dqgYQd4MSE6EUyfZkYJ219ZJSVmEF4zisuMZmbMqfbxkPTnWe1fhLkzxX?cluster=devnet), [payment](https://explorer.solana.com/tx/4iEa3rSFdPYugkZR1VE7LfKY6T4Z2NjFgsmYonVycYimv5aMDrX2Fc6ngjfhVdnGHMANGn33LJREMfsqWwbE5W2H?cluster=devnet). The receipt names the KMS key as the payer and verifies |
| A metered payment over a payment channel (x402 `upto`): 0.05 USDC pulled, 0.00296 spent | [pull](https://explorer.solana.com/tx/JVVo6cMkEoZDQyrjM2XHvJcR9otHSDphe9yuqPmoZ7Axavgdb3J1ETR72dit5FTJrRGDViVX2WQA2d8Hd9JsmJb?cluster=devnet), [settlement](https://explorer.solana.com/tx/hP6tYw1gkr3CpBYGY6ETon3Lh7eRwLiHLwASNvjD47oPXwy8weBhrcMkBnx6xFZkEXLEfr1i3P3fwnVoJPmu9zd?cluster=devnet), [0.04704 back to the vault](https://explorer.solana.com/tx/wcvKscPxVisFn6RcE7JB2Bz6XuDknrBkcA4hX5RtQLtghih2MTeojAJSevjCFgZUie5So1D6dA7GyudXXrtqqwz?cluster=devnet). KMS signed the pull, the channel's open and the refund; the voucher is the seller's to sign |
| A seller that opened a channel and never settled it (`meterstall`): 0.04 USDC pulled, nothing spent | The payer's own exit, each step signed by KMS: [close requested](https://explorer.solana.com/tx/4ERbJ7V1UCfam2nwMygNbwJVdcDQPuYJhSSGDDSUczzGn7CqGWG6sDvZmasUWyivr8iGaSvanhbP9zQQE1BrSi3S?cluster=devnet), [sealed](https://explorer.solana.com/tx/3oQnHpu9FKUGggnhMoXpuPh8CXqpjCTa8hcSJYSZ4BJRPmNWHEGTvQVkrdRL3ksLS5yeaU8xfyGCQFTivVLZCuov?cluster=devnet), [withdrawn](https://explorer.solana.com/tx/2ypHsMTgFgh3Q3zxcWSexB4tVrYAaqme9Pf2bMjf6DVE8V3t78RaEHgV3PTbqc7RvaBvBfW7R3MvSLp1M1RivmXp?cluster=devnet), [all 0.04 back to the vault](https://explorer.solana.com/tx/4c619ySfYfKwMVWh16ATWt1ejkmXbJnisKfB4NsDRxmfYDpNSCivdZjFrbDCpKes64xbTRvAfj2MMtJdqcWJSsct?cluster=devnet) |

The sellers were Algebra's own demo sellers and the vault owner was a keypair file
standing in for a person's wallet. **Not run:** an `hsm` key, the mainnet cluster,
the deployed server holding the credentials (`GOOGLE_CREDENTIALS_JSON`), a KMS
outage in the middle of a payment (the retry and the refusal are covered by tests
against a stand-in for KMS only), and workload identity federation.

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
