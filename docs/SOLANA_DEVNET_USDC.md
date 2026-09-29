# USDC on Solana devnet: plan (not built yet)

Demo-only payment rail for Spend Passes. **Devnet only**: the client refuses
any RPC whose `getGenesisHash` is not devnet
(`EtWTRABZaYq6iMfeYKouRu166VU2xqa1wcaWoxPkrZBG`). Live accounts never use it.

## Verified facts (2026-09-29)

- Circle devnet USDC mint: `4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU`.
  It has 6 decimals and is owned by the SPL Token program
  `TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA`.
- Test USDC comes from faucet.circle.com. The faucet is manual and has a
  captcha, so a person has to fund wallets there.

## Decisions

- **No `solana-go` dependency.** Write a minimal stdlib client in
  `internal/platform/solana`:
  - base58
  - ed25519 keys
  - ATA/PDA derivation (on-curve check done with `math/big`)
  - the legacy transaction message format
  - SPL instructions: `TransferChecked` (12), `ApproveChecked` (13) and
    `Revoke` (5), plus ATA `CreateIdempotent` (1)
  - JSON-RPC calls: blockhash, send, signature status, token balance, parsed
    account, airdrop and genesis hash
- **Treasury key** is derived from the master key with HKDF (info
  `algebra/solana/treasury/v1`), the same way the receipt signer works. It
  pays every fee, so users never need SOL, and it is the demo store's
  settlement address.
- **Wallet models ("Both"):**
  1. *Pass wallet* (the default "virtual card"): an ed25519 key per pass,
     encrypted with `privacy.AESGCMEncryptor`. The person funds it from the
     Circle faucet, or with a capped top-up from the demo float the treasury
     holds.
  2. *Own wallet* (advanced): Phantom, Solflare or Backpack connect through the
     Wallet Standard events (`wallet-standard:app-ready` /
     `register-wallet`), so no npm dependency is needed.
     - The backend builds an `ApproveChecked` transaction. It sets the
       delegate to the pass key and the amount to the allowance, and the
       treasury is the fee payer and signs first.
     - The wallet then signs through `solana:signTransaction`, and the backend
       checks that the message bytes are unchanged before submitting.
     - The on-chain allowance is a hard cap, even if Algebra is compromised.
     - Revoking a pass shreds the delegate key. A "Revoke on-chain" button
       sends `Revoke`.
- **Currency:** passes stay in INR. At checkout the price is converted with
  `SOLANA_INR_PER_USDC` (a demo rate, shown on the order), using
  `micro = ceil(paise * 1e6 / ratePaise)`.
- **Settlement hook:** `OrderService.SetPaymentRail(rail)`.
  - `Settle(ctx, ord, agentID)` runs after the pass re-check and before
    `demo_checkout` places the order. It sends a `TransferChecked` to the
    treasury ATA and waits for it to confirm.
  - If placing the order then fails, the treasury refunds the payment.
  - When the pass has no wallet, or the order isn't a demo order, it returns
    `(nil, nil)`.
- **Storage (migration 0014):**
  - `pass_wallets(pass_id PK, user_id, mode, address, secret_enc, nonce,
    owner_address, token_account, created_at)`
  - `order_payments(order_id PK, rail, cluster, signature, amount_micro,
    rate_paise_per_usdc, from_address, to_address, created_at)`
- **Receipt:** add an optional
  `payment {rail: "solana-devnet-usdc", tx, amount, mint, from, to}` field to
  the claims.
- **Order page:** "Paid X USDC on Solana devnet" with a link to
  `https://explorer.solana.com/tx/<sig>?cluster=devnet`.

## API (session only: the person, never the agent)

| Route | Purpose |
|---|---|
| `GET /api/v1/me/passes/{id}/wallet` | Address, mode, balance or allowance |
| `POST /api/v1/me/passes/{id}/wallet` | Create the pass wallet |
| `POST /api/v1/me/passes/{id}/wallet/topup` | Demo float top-up (demo accounts, capped) |
| `POST /api/v1/me/passes/{id}/wallet/allowance/prepare` | `{owner, amount_usdc}` returns the transaction (base64) |
| `POST /api/v1/me/passes/{id}/wallet/allowance/submit` | Signed transaction returns the signature |
| `GET /api/v1/crypto/status` | Treasury address, SOL/USDC balances, cluster check |

`GET /api/v1/pass` and `algebra.spend_pass` also get a read-only wallet
summary for agents.

## Setup a person must do once

1. Run the backend and read the treasury address from `/api/v1/crypto/status`.
2. Fund the treasury with devnet SOL for fees (`requestAirdrop`, or
   faucet.solana.com) and with USDC for the demo float (faucet.circle.com).
