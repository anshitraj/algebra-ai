# Demo runbook: routing, the spend firewall and payment channels on devnet

Everything here runs on your machine against Solana **devnet**: real
transactions, test money. It is the script for the demo video and for anyone
who wants to see the router and the spend firewall work end to end.

## What it shows

1. **Routing by class of work.** An agent asks for `token.price`, not a
   provider. Algebra groups every endpoint in Pay.sh, Circle, PayAI and
   Coinbase's Bazaar by the work it does (14 classes, about 110 routable
   endpoints), prices every provider of the class live, and ranks them.
2. **The router's guards.** A provider that is down, that asks more than its
   own listing, or that prices like a trap is refused before anything is paid.
3. **The spend firewall.** Kill switch (stops even a payment already in
   flight), calls per minute, and the new-provider rule (cap or ask). A dry run
   answers "would this pass, and who would be paid?" without paying.
4. **Solana payment channels.** A metered API over x402 `upto`: the wallet
   escrows a $0.05 ceiling in the payment-channels program, the provider
   settles what the call actually cost from a voucher, and the rest comes back
   in the same transaction.

## The demo providers (`backend/cmd/demo-provider`)

Paid x402 APIs on devnet, each labelled as a demo in every response:

| Provider | Work | Listed | Asks | What it demonstrates |
|---|---|---|---|---|
| `demo:alpha` | token.price | $0.002 | $0.002 | honest, fast |
| `demo:beta` | token.price | $0.001 | $0.001 | honest, cheapest, slow (1.2 s) |
| `demo:flaky` | token.price | $0.0005 | 503 | down: the health probe takes it out |
| `demo:greedy` | token.price | $0.001 | $0.004 | overcharges its listing: refused |
| `demo:trap` | token.price | $25 | $25 | honeypot price: refused |
| `demo:meter` | llm.chat | ceiling $0.05 | actual ~$0.003 | x402 `upto` through a payment channel |

The provider is its own facilitator: it checks each payment, co-signs it as
fee payer and submits it to devnet.

## One-time setup

1. Database and Redis: `scripts\dev-native.ps1 up`.
2. Algebra's devnet wallet (`.data/solana-devnet.json`): get **devnet USDC**
   at <https://faucet.circle.com> (Solana devnet). It needs no SOL: providers
   pay the fees.
3. The demo provider's wallet (`.data/demo-provider.json`, created on first
   start; its address is in the log): get **devnet SOL** at
   <https://faucet.solana.com>. It pays transaction fees and channel rent, and
   creates its own USDC account once funded.

## Run

From the Claude desktop app, start the launch configurations `demo-provider`,
`api-demo` (API on :8085 with the demo providers configured) and `web-demo`
(console on :3300). By hand:

```bash
go -C backend run ./cmd/demo-provider -key ../.data/demo-provider.json
```

```powershell
$env:ALGEBRA_DATA_DIR = (Join-Path (Get-Location) '.data'); $env:ECONOMIC_PROVIDERS = (go -C backend run ./cmd/demo-provider -print-config); $env:ALGEBRA_API_ADDR = '127.0.0.1:8085'; $env:PUBLIC_WEB_URL = 'http://localhost:3300'; $env:CORS_ALLOWED_ORIGINS = 'http://localhost:3300'; ./scripts/dev-native.ps1 api
```

```powershell
$env:ALGEBRA_API_URL = 'http://localhost:8085'; pnpm --dir frontend exec next dev --port 3300
```

Sign in, create a USDC Spend Pass (Spend passes), and switch the console to
**Devnet** at the top.

## The script (about 3 minutes)

1. **Routing** (`/console/routing`). The classes, how many providers do each,
   what the work usually costs. Open *Token price*, press *Probe now*: real
   providers (Alchemy, Birdeye, Allium) and the demo ones, with listed price,
   live 402 price, health and latency. greedy is flagged *over listing*, trap
   *trap price*, flaky *down*.
2. **Would it pass?** (`/console/firewall`, or the button on the class).
   Run the dry run for token.price on devnet: ALLOW, Algebra would pay
   demo:beta $0.001 (best overall), demo:alpha is the fallback; flaky, greedy
   and trap are refused with the reason for each. Nothing was paid.
3. **The agent** (`/console/agent`, pass selected). Ask *"What's the price of
   USDC? Use whoever is best."* The agent calls `route_work`: Algebra pays
   demo:beta on devnet, the step card links the transaction on Solana
   Explorer, and the signed receipt commits to the plan and the quote.
4. **Kill switch.** On the firewall page press *Freeze every pass*, ask the
   agent again: refused, nothing paid. A payment already being made when the
   switch is pressed is refused at the last check before it is signed. Lift it.
5. **New providers.** Set the pass's rule for providers you've never paid to
   *Ask me*, ask for something from a provider not yet paid: the intent waits
   for approval; after the tap it runs.
6. **Payment channel.** Ask the agent to use the metered LLM (`llm.chat`,
   demo:meter). Two explorer links: the channel open (escrow $0.05) and the
   settlement (settle_and_seal + distribute: the provider paid ~$0.003, the
   rest back to the wallet). The intent records what was actually paid, not the
   ceiling.

## Proof: real devnet payments through Algebra (2026-10-07)

An agent with a Spend Pass, calling `POST /api/v1/execute` against the demo
providers, paid in Circle's devnet USDC from Algebra's wallet:

| What the agent asked | What Algebra did | Transaction |
|---|---|---|
| `token.price` (a class, no provider named) | priced 5 providers; refused trap (price outlier), flaky (503) and greedy (asks $0.004, lists $0.001); paid **demo:beta 0.001**, alpha kept as fallback | [4zt18fnB…](https://explorer.solana.com/tx/4zt18fnBYUk3hxq44qrb93xoVTBidKR53nMy2kHQWHzYQ4JwdVYs4LNnDSfJs2JAyjD8EEk8uxjZHszDcCXMoAQV?cluster=devnet) |
| `llm.chat`, metered (x402 `upto`) | escrowed a 0.05 ceiling in a payment channel; the provider settled **0.0042**; **0.0458 came back** in the same transaction; the intent records 0.0042 spent | open [3zmpMXTY…](https://explorer.solana.com/tx/3zmpMXTYbsdjhTZow1Qp72iYRCbxLjnK4WQzVRee6uG2TGRxBS8Ryte46t8v2eqykXERSt6rojavbWM6BFPaBvSH?cluster=devnet), settle [5V9ATU74…](https://explorer.solana.com/tx/5V9ATU749V6sGd96mpmDRP82YmiLRpk9MtvpyXkkderwKSDJGWCTgw9FkPz9bmKAFy64kwcz8JvWkdx3fMcrD21S?cluster=devnet) |
| `token.price` with the kill switch on | refused with 403 "frozen by its owner's kill switch"; nothing paid | none |
| `token.price` from demo:alpha, never paid before, pass rule "ask me" | intent moved to AWAITING_APPROVAL; after the person approved, paid **demo:alpha 0.002** | [3Jhety7a…](https://explorer.solana.com/tx/3Jhety7asFqxXtVUgJb95xivtAEcjntQvnNXgxUX5TA5GNxhSvC7k1WsKdsmx3Ezmjp3Mu7oSZ7kzY733GAHFcRc?cluster=devnet) |

## Payment channels, proven on devnet

`backend/cmd/paychan-smoke` runs the x402 `upto` flow through the real
payment-channels program with a throwaway token, so it needs devnet SOL but no
USDC:

```bash
go -C backend run ./cmd/paychan-smoke -payer ../.data/solana-devnet.json -provider ../.data/demo-provider.json
```

A run on 2026-10-07 (devnet):

| Step | Transaction |
|---|---|
| Test token, accounts | [31zFWSbT…](https://explorer.solana.com/tx/31zFWSbTvE6Mkap34hFbJgLkcpgSajpG4JLVdV4mPAaEpeyUBeNrr9aCbK1udvvNSViRaSG8hHwQEuARMJgA9Nac?cluster=devnet) |
| Channel open: 0.05 escrowed (payer signed, provider co-signed as fee payer) | [4sTVg2j5…](https://explorer.solana.com/tx/4sTVg2j5tX8KiH2qCd2KRJzzsGZbzwVEmpsLfNw52kwj5GqdTeEJqrbvp2vmuZUXwQD8xW4r81SSffWZA4FNm4Qq?cluster=devnet) |
| Voucher for 0.012345, settle_and_seal + distribute | [1y138m66…](https://explorer.solana.com/tx/1y138m66QKPTWsX5k5HSrbpS4XJKELzEe9b7w7349fXapPSxJ1QQ83yFp8k376krcFvmAgVxAuALXMnCwezc4x6?cluster=devnet) |

In the last transaction the escrow paid out 0.05: 0.012345 to the provider and
0.037655 back to the payer.

## The same thing over the API

With a pass's agent token (`Authorization: Bearer …`):

```bash
curl -s localhost:8085/api/v1/classes
```

```bash
curl -s -H "Authorization: Bearer $TOKEN" -H 'content-type: application/json' localhost:8085/api/v1/policy/simulate -d '{"capability":"token.price","input":{"mint":"EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"},"budget_max_minor":50000,"constraints":{"allowed_networks":["solana-devnet"]},"live_quotes":true}'
```

```bash
curl -s -H "Authorization: Bearer $TOKEN" -H 'content-type: application/json' localhost:8085/api/v1/execute -d '{"capability":"token.price","input":{"mint":"EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"},"budget_max_minor":50000,"window":"demo-1","constraints":{"allowed_networks":["solana-devnet"]}}'
```

The same tools are on the MCP server at `/mcp`: `algebra.classes`,
`algebra.simulate`, `algebra.execute`.

## Without a chain: the sandbox providers

With `ECONOMIC_SANDBOX=on` the API serves the same cast itself, paid through
the sandbox rail (no chain, no money, receipts marked `test`):
`sandbox:alpha`, `sandbox:beta`, `sandbox:flaky`, `sandbox:greedy` and
`sandbox:trap`, at `/api/v1/sandbox/x402/prices/{name}`, with the prices and
the misbehaviour in the table above. Ask for `token.price` with
`"constraints":{"allowed_networks":["sandbox"]}` and the router pays
sandbox:beta and refuses the other three with their reasons. This is what the
end-to-end tests drive over HTTP (`backend/test/e2e/router_firewall_http_test.go`):
routing and the guards, the dry run, the kill switch, the new-provider gate and
the per-minute cap.

## Mainnet

The `api-demo` launch configuration sets `SOLANA_ALLOW_MAINNET=yes`, so a
wallet at `.data/solana-mainnet.json` becomes the mainnet rail
(`go -C backend run ./cmd/solana-wallet -new -out ../.data/solana-mainnet.json` makes one).
Every payment is capped at 1 USDC unless `SOLANA_MAX_PAYMENT_USDC` says less.
Unfunded, it still answers dry runs against the live catalogs: switch the
console to **Mainnet** and use *Would it pass?*, or `POST /api/v1/policy/simulate`
with `"allowed_networks":["solana"]` and `"live_quotes":true`. A real mainnet
call needs real USDC in that wallet (a few cents is enough; x402 providers pay
the fees) and is the operator's to make.
