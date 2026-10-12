# Mainnet readiness

**Status on 2026-10-12: not ready for customer funds. Mainnet is read-only.**
Algebra can discover mainnet providers, ask them for real prices and dry-run a
request against a pass. It will not sign a mainnet payment. This is deliberate:
a devnet demonstration that is true is worth more than a mainnet release that
is not safe.

Devnet readiness is a separate matter and is covered in [DEMO.md](DEMO.md) and
[SPEND_PASS_PROGRAM.md](SPEND_PASS_PROGRAM.md): devnet works today, with
transactions to prove it.

## What mainnet can do today

| Capability | State | How to see it |
|---|---|---|
| Discover mainnet-compatible APIs | Works | Providers and Provider intelligence in the console; `GET /api/v1/intel/endpoints?network=solana` |
| Get unpaid quotes from them | Works | `POST /api/v1/policy/simulate` with `"allowed_networks":["solana"]` and `"live_quotes":true`; MCP `algebra.quote` |
| Compare cost, speed and health | Works | the same dry run; Routing in the console |
| Explain why execution is unavailable | Works | every refusal carries a code: `MAINNET_EXECUTION_DISABLED`, `network_not_allowed`, `NETWORK_NOT_ALLOWED` |
| Build and *simulate* a real payment without sending it | Works | `backend/cmd/x402-dryrun` |
| Pay a provider on mainnet | **Off** | needs every gate below |
| On-chain Spend Pass on mainnet | **Not deployed** | the program account does not exist on mainnet (checked against the cluster on 2026-10-12) |

## The gates in the code

A mainnet payment has to pass all of these. Each is independent, so one
mistake in configuration does not open the door.

| Gate | Where | Default |
|---|---|---|
| `SOLANA_ALLOW_MAINNET=yes`, or the server refuses to start with a mainnet wallet | `internal/platform/config/config.go` | unset |
| `MAINNET_EXECUTION=on`, or every mainnet payment is refused with `MAINNET_EXECUTION_DISABLED` | `EconomicService`, tested by `TestEconomic_NoMainnetPaymentIsSignedUnlessEnabled` | off |
| An API key or pass made for `sandbox` or `devnet` can never pay on mainnet | `domain/tenancy`, `TestEconomic_AProjectPassPaysOnlyInItsEnvironment`, `TestTenancyOverHTTP_AKeyRunsInItsProjectAndEnvironmentOnly` | always |
| Demo accounts pay in the sandbox only | `TestDemoAccountOverHTTP_PaysInTheSandboxOnly` | always |
| A hard ceiling per payment: 1 USDC unless `SOLANA_MAX_PAYMENT_USDC` says less | the Solana rail | 1 USDC |
| `SPEND_PASS_ANY_OWNER` cannot include mainnet: a pass's owner must be a wallet the person signed in with | config, refused at startup | always |
| Mainnet and devnet payments never cross: the rail checks the network it signed for against the intent's | `AuthorizePayment`, code `NETWORK_NOT_ALLOWED` | always |
| The USDC mint and token program are fixed per cluster, not taken from the provider | `internal/domain/chain`, the rail | always |
| Settlement is read from chain state, never from the provider's word | `providers/solanax402` | always |

## What must be done before a funded mainnet test

Nothing on this list is done unless it says so. The order is the order to do
them in.

### 1. The program

- [ ] **Independent security review** of `solana-program/programs/spend-pass`.
      It is about 600 lines with unit and LiteSVM tests of every limit, but
      nobody outside the team has read it.
- [ ] **Deploy to mainnet** from a wallet holding the rent (1.331 SOL locked,
      about 2.7 SOL needed during the deploy). Not done, and not to be done
      without the owner's explicit go-ahead.
- [ ] **Verify the deployed program** against the source (a verifiable build,
      and the program ID pinned in configuration), so the server refuses to
      use anything else.
- [ ] **Move the upgrade authority off the server.** On devnet the upgrade
      authority is `GhpmTWT8…EoPA`, which is *the same key the server signs
      payments and pulls with*. That is acceptable for a devnet demo and
      unacceptable for real funds: whoever takes the server could replace the
      program. On mainnet the authority must be a multisig (Squads) or the
      program made immutable.

### 2. The signer

- [ ] **Isolate the production signer.** Today the payer key is a keypair file
      or an environment variable, and one key pays for every organization. The
      on-chain pass bounds what that key can pull from each vault, which is the
      main protection; but between a pull and its payment the money sits in the
      payer's account for a few seconds.
- [ ] A KMS-backed signer exists as unmerged local work (branch
      `local/wip-2026-10-12`, `backend/internal/platform/gcpkms`). It has not
      been reviewed, tested against real KMS, or merged. Until it or an
      equivalent is, there is no hardware-backed key.
- [ ] Decide between one payer per organization and one shared payer with
      `SPEND_PASS_REQUIRED=mainnet`. The second is the minimum: with it, the
      payer never holds money that is not a pass's.

### 3. Operations

- [ ] `SPEND_PASS_REQUIRED=mainnet` set, and tested on mainnet with a few cents.
- [ ] A paid RPC endpoint (`SOLANA_MAINNET_RPC_URL`); the public one rate-limits.
- [ ] Priority fees estimated per transaction. Today a fixed
      `SPEND_PASS_PRIORITY_MICROLAMPORTS` (10,000) is used for pulls and refunds.
- [ ] Per-organization budgets on mainnet above the pass's own (a ceiling an
      organization cannot raise by issuing passes).
- [ ] Monitoring and alerts: payer balance, pulls not settled, intents stuck in
      `UNKNOWN` or `RECONCILING`, refund failures, catalog refresh failures.
      The reconciler and the health endpoint exist; alerting does not.
- [ ] A written incident runbook: the kill switch (`POST /api/v1/me/killswitch`,
      and `set_frozen` from the owner's wallet, which the server cannot undo),
      turning `MAINNET_EXECUTION` off, rotating the agent key with `set_agent`.
- [ ] Mainnet-specific rate limits (today: 120 requests a minute per token,
      the same on every network).

### 4. The first payment

- [ ] One payment of a few cents to one provider, from a dedicated wallet, by
      the operator, following the runbook in [EXECUTION.md](EXECUTION.md).
- [ ] Its transaction, receipt and reconciliation checked by hand before
      anything else is enabled.

## Known limits that are not blockers but should be said

- A pull and its payment are two transactions. The program bounds the amount
  and the destination; it cannot make them atomic without breaking x402.
- Swaps (`solana.swap`) have only run against a fake cluster. They must stay
  off on mainnet until one has landed on devnet or mainnet and been checked.
- Provider trust starts at `unverified` for all 6,131 registered endpoints
  but one. On mainnet the new-provider rule of every pass should be `approve`
  or `cap` until a provider has been paid and has delivered.
- Base-only and fiat-billed providers are listed and cannot be paid.

## What will not happen without the owner saying so

No mainnet deployment, no mainnet transaction, no transfer of funds, no change
of a wallet's or a program's authority, and no switching on of
`MAINNET_EXECUTION`. The code refuses the last by default; the others need keys
and SOL the server does not have.
