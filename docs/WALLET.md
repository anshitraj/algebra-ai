# The owner wallet (the Vault)

Algebra's vault is the owner's own wallet, made in their browser. Algebra never holds, sees or receives the owner's
private keys. This page is the design and its threat model; `docs/ROADMAP.md` has the order things are built in.

## What it is

- **One recovery phrase** (BIP-39, 12 words, 128 bits of entropy) per owner, generated in the browser with
  `@scure/bip39` from the platform's secure random source.
- **Two addresses from it.** Solana: SLIP-0010 ed25519 at `m/44'/501'/0'/0'` (the path Phantom and Solflare use, so
  the phrase imports into them). EVM: BIP-32 secp256k1 at `m/44'/60'/0'/0/0`; the same address receives on Base and
  on Arc (and their testnets). Libraries: `@noble/curves`, `@noble/hashes`, `@scure/bip32`, `@scure/base`, all
  audited, zero-dependency, open source.
- **Encrypted at rest in the browser.** The phrase is sealed with AES-256-GCM (WebCrypto) under a key derived from the
  owner's wallet password with Argon2id (t=3, m=32 MiB, p=1, 16-byte random salt, 12-byte random IV). The additional
  authenticated data binds the ciphertext to its two addresses, and opening it re-derives both and compares, so a
  swapped or edited keystore is refused. The sealed keystore lives in this browser's storage and in a backup file the
  owner downloads.
- **The wallet password is not the sign-in.** Signing in is an emailed code; the wallet opens with its own password,
  and is recovered with the phrase or the backup file and its password. An email account takeover gives no key.
- **Unlocked only in memory**, and locked again after 15 minutes without use, on "Lock", and on reload.

## What Algebra's server gets

Only public things: the two addresses and two signatures over one message naming the account and both addresses
(ed25519 for Solana; EIP-191 `personal_sign` for EVM), which prove the addresses belong to whoever holds the phrase.
The server verifies both before recording them. It reads balances from the chains, hands the browser what it needs to
build a transaction (a recent blockhash; a nonce and fees), and relays transactions the browser has already signed.

## Withdrawals

The browser builds the transaction itself (an SPL `TransferChecked` on Solana, creating the recipient's token
account when it has none; an ERC-20 `transfer` in an EIP-1559 transaction on Base or Arc), shows what it will do,
signs it and gives the signed bytes to the server to broadcast. The server never supplies the transaction, so it
cannot slip in a different recipient or amount.

## Agents spending

Agents spend from the vault only through a limit the chain enforces (`internal/app/vault_allowance.go`,
`frontend/lib/wallet/allowance.ts`). Built on Solana (Testnet now, mainnet behind `VAULT_MAINNET_DEPOSITS`), and on
Base Sepolia and Arc testnet (below).

- **The limit.** The owner sets "agents may spend up to X USDC". The browser builds an SPL `ApproveChecked` that names
  Algebra's payer as the delegate of the vault's USDC account for X, and the vault signs it. The token program then
  refuses any transfer by the payer past what is left of X, whatever Algebra's server does. "Stop" is an SPL `Revoke`,
  signed the same way. Phantom, Solflare or any Solana wallet holding the phrase can revoke too, without Algebra.
- **Algebra pays the network fee** of the approve and the revoke, so a vault holding only USDC can use them. The
  browser signs as the second signer; the server adds the payer's signature as fee payer only after checking the
  transaction holds exactly one approve (to the payer, on the vault's USDC account, at most `VAULT_ALLOWANCE_MAX_USDC`)
  or one revoke, and at most a bounded compute budget. The payer's signature then pays the fee and nothing else.
- **The browser checks the server's answer**: the USDC mint and the vault's own token account come from the browser's
  own table and derivation, not from the server; a different mint, token account or a delegate that isn't the fee
  payer is refused, as is a priority fee far above normal.
- **Pull before pay.** Just before an agent's signed x402 payment is handed over, the payer pulls exactly its amount
  from the vault into its own USDC account, as the delegate (one `TransferChecked` with a memo naming the attempt).
  The payment is released only once the pull is confirmed. Whatever the attempt didn't spend goes back to the vault
  (the same reconciler as on-chain Spend Passes). A refund returns the USDC but not the limit: the token program counts
  the limit down on every transfer, and only the owner can raise it.
- **Spend Passes and Job Passes are budgets inside the limit**, kept by Algebra's atomic reservations. They are not
  separate funds on chain. The limit is the most any of them, together, can ever take.
- **Testnet credits** are used only while agents don't pay from the vault; a payment the vault funded is not counted
  against them. The receipt names the vault's USDC account and the pull's transaction (`owner_vault_allowance`).
- **What the limit does not bound**: who receives what the payer pulls. A delegate can transfer to any account, so a
  compromised payer key could send up to what is left of the limit elsewhere. The owner bounds that by the size of the
  limit, and stops it at once with a revoke. A program-held pass (the on-chain Spend Pass, whose pulls can only go to
  the payer) is the stricter option, at the cost of moving USDC into it.

### On Base and Arc

`internal/app/evm_allowance.go`, `frontend/lib/wallet/permit.ts`. The same limit, through USDC's EIP-2612 permit:

- **The browser signs a permit**, never a transaction: "Algebra's EVM payer may spend up to this much of the vault's
  USDC, until this deadline", over USDC's own EIP-712 domain on that chain. Signing costs the vault nothing; Algebra
  submits it and pays the gas. A permit for 0 stops it. The browser checks the chain id, the USDC contract, its domain
  name and version, and that the owner named is this vault, from its own tables; the server checks the permit
  recovers to the vault's address, for its payer, at the current nonce, before submitting it.
- **USDC holds the line**: `transferFrom` past what is left of the allowance fails in the contract. The payer pulls
  exactly a payment's amount just before it signs the x402 payment, and the payment is released only once the pull is
  mined and succeeded; what an attempt didn't spend is sent back (`Reconcile`).
- **The payer needs gas** on each chain (Base Sepolia ETH; on Arc, USDC is the gas) to submit permits, pulls and
  refunds. The x402 payments themselves cost it nothing: the seller submits them.
- Checked against the real contracts without moving money: a read-only `permit()` call on Base Sepolia and Arc testnet
  succeeds with the browser's signature and fails with a tampered one (`providers/evmx402`, `EVMX402_LIVE=1`).

## Mainnet

Deposit addresses are shown for test networks only. Real-money mainnet deposits stay off (`VAULT_MAINNET_DEPOSITS`)
until this design has had an independent security review. Withdrawal works everywhere: it is the owner's key.

## Threats, and what answers them

| Threat | Answer |
|---|---|
| Algebra's server or database is breached | No private key or phrase is there; only addresses. |
| The owner's email is taken over | The wallet needs its own password or the phrase. |
| The keystore is copied off the device | Argon2id at 32 MiB per guess; AES-GCM refuses tampering; a long password is required. |
| A malicious server answer during a withdrawal | The browser builds the transaction; the server only gives a blockhash or nonce and fees. |
| Algebra's server tries to take more than the owner allowed agents | The token program refuses any transfer past what is left of the limit. |
| Algebra's payer key is stolen | It can move at most what is left of each vault's limit; owners revoke from the Vault page or any Solana wallet. |
| An owner tries to make the payer sign more than a fee | The server signs only a transaction holding exactly one approve to itself or one revoke, with a bounded compute budget; 20 changes an hour per account. |
| Script injection in the console (XSS) | The real risk of any web wallet: a strict CSP, no third-party scripts in the console, keys only in memory while unlocked, auto-lock. Independent review before mainnet. |
| The owner forgets the password and loses the phrase | Nothing can recover it. The phrase is shown once at creation and the owner confirms two of its words. |
