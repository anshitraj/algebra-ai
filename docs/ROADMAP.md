# Algebra roadmap (2026-10-29)

**Algebra makes every agent payment accountable.** One layer to authorize purchases, pick reliable providers, stop
wasted spending, and prove what agents received.

| Authorize | Execute | Verify |
|---|---|---|
| Before spending: Spend Pass and Job Pass limits, ask rules, allowed recipients | With safeguards: probe before paying, price and payee checks, smart routing, safe retry | After paying: signed receipt, result kept, refunds of credits when nothing usable came back |

The person sees three things: **Vault** (money), **Agents** (Spend Passes, with Job Passes inside each agent's tasks)
and **Activity** (what was bought, what it cost, what came back). Everything else is backend.

## Decisions (the user's, 2026-10-29)

1. **Wallet: in-house, non-custodial.** Owner wallets are generated in the browser with open-source libraries
   (no wallet-as-a-service). The key is encrypted with authenticated encryption under a password-derived key and never
   sent to Algebra's backend. Wallet recovery is separate from sign-in. One signer interface, EVM and Solana.
   Recovery, import, export and withdrawal are all supported.
2. **Agents spend only through on-chain-enforced, limited permissions.** Backend policy is never the only thing
   between an agent and unrestricted signing:
   - Solana: SPL Token `approve` (delegate with a capped amount) or the Spend Pass program; Algebra pulls just in time,
     then pays (pull-before-pay, already built for the Anchor pass).
   - EVM (Base, Arc): USDC `approve` to Algebra's spender with a capped allowance, pulled just in time; x402 `exact`
     paid with EIP-3009 from the payer.
   - Circle Gateway (one USDC balance across Solana, Base and Arc; deposits on any chain, spends on any chain in
     under 500 ms) is the cross-chain layer; CCTP is the fallback when a seller is on a chain the balance is not.
3. **Vault → Spend Pass → Job Pass.** The Vault is the funded balance. A Spend Pass is an agent's reusable budget
   and rules. A Job Pass is a temporary budget for one task, drawn from its parent pass and shared by every sub-agent
   the task delegates to (three sub-agents with a $3 job can spend $3 together, not $9). Sub-vaults and passes are
   logical allocations, never separate on-chain wallets. Enforced: atomic reservations, parent-child limits,
   expiry, revocation, release of what is unused.
4. **Testnet and Mainnet only** in the switch. Testnet is Solana devnet with Algebra's demo sellers (and any real
   devnet seller), paid from Algebra's test wallet; every account starts with **10 USDC and 5 SOL in test credits**.
   Demo accounts (no sign-up) keep the simulated sandbox.
5. **Real-money mainnet deposits stay off** until the security architecture is independently reviewed. Mainnet code
   is built behind that switch.

## Facts this rests on (live scan 2026-10-29, unpaid probes only)

- Circle Agent Marketplace: 2,833 endpoints, 124 hosts; 859 plain x402, 1,912 Gateway-only. Plain payable: Solana
  718, Base 708, Arc (`eip155:5042`) 141. Through Gateway: Arc 2,046, Base 2,029.
- Pay.sh: 76 providers, 817 endpoints, Solana.
- One unpaid request per host (194): 168 answered a valid x402 v2 challenge; 112 of 118 Circle hosts matched their
  listing; 3 did not (a 10x price rise, a changed payee, a usage-priced zero); median 402 in 715 ms.
- Real testnet sellers are few (QuickNode devnet RPC, one more), so Testnet needs Algebra's demo sellers.

## Build order

| # | What | State |
|---|---|---|
| 1 | Working endpoints: import Base and Arc listings too (shown, paid once a rail exists), health per endpoint, price and payee checks | in progress |
| 2 | Recovery and refunds: unknown outcome blocks new payments and reconciles (built); credits come back when a paid call delivered nothing usable | in progress |
| 3 | Console: chat is the default page, Testnet/Mainnet switch, Solana/Base/Arc marks, Spend Pass folders (on/off, limit bar, delete), answer cards per kind of request | in progress |
| 4 | Browser wallet: generate, encrypt, recover, import, export, withdraw; EVM + Solana signer | next |
| 5 | Capped on-chain permissions on Solana and Base; Base x402 rail (EIP-3009); Circle Gateway deposits | next |
| 6 | Job Pass: task budget, expiry, sub-agents share the parent budget, unknown recipients need approval, duplicate detection | next |
| 7 | Multi-select and smart routing by cost per accepted result (failures and retries counted), purchase memory (reuse a fresh, reusable result instead of paying twice; catch loops) | next |
| 8 | Task P&L per agent; Replay (financial chaos tests before an agent ships) | later |
| 9 | Web discovery tools (TinyFish and others) as paid or keyed sources | later, needs the user's API keys |
