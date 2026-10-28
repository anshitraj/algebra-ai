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

Not in this stage. Agents will spend only through permissions the chain enforces with a cap (an SPL `Approve` of a
capped amount to Algebra's payer on Solana; a capped USDC allowance on Base and Arc), pulled just in time. A
backend policy is never the only thing between an agent and the owner's money. See the roadmap.

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
| Script injection in the console (XSS) | The real risk of any web wallet: a strict CSP, no third-party scripts in the console, keys only in memory while unlocked, auto-lock. Independent review before mainnet. |
| The owner forgets the password and loses the phrase | Nothing can recover it. The phrase is shown once at creation and the owner confirms two of its words. |
