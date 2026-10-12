# Diagrams

Six diagrams of what is built, as Mermaid source (GitHub renders it). Every box
names a module in this repository; the paths are under `backend/` unless they
start with `solana-program/` or `frontend/`.

## 1. Architecture

```mermaid
flowchart TB
  subgraph Clients
    APP["Customer application<br/>REST, Algebra API key"]
    AGENT["AI agent<br/>MCP at /mcp, Spend Pass token"]
    CONSOLE["Console and public site<br/>frontend/ (Next.js)"]
  end

  subgraph API["cmd/api: one Go process"]
    REST["internal/api/v1<br/>REST transport"]
    MCP["internal/mcpserver<br/>MCP transport"]
    TEN["app/tenancy_service<br/>organizations, projects, API keys"]
    EXEC["app/execution_service<br/>router: candidates, quotes, ranking"]
    GUARD["app/execution_guards<br/>health, listing, outlier, trust"]
    PASS["app/spend_pass_service<br/>policy: ALLOW / HOLD / BLOCK"]
    ECON["app/economic_service<br/>coordinator: reserve, begin, authorize, complete"]
    INTEL["app/intel_service<br/>Provider Intelligence registry"]
    RCPT["app/economic_receipts<br/>Ed25519 receipts"]
    VAULT["app/result_vault<br/>kept answers, sealed"]
  end

  subgraph Rails["providers/"]
    X402["x402client + solanax402<br/>x402 exact on Solana"]
    CHAN["paychan<br/>x402 upto, payment channels"]
    JUP["jupiter<br/>swap, simulate before sign"]
    SBX["sandboxpay<br/>simulated rail and sellers"]
  end

  subgraph Catalogs["providers/catalog, paysh, bazaar"]
    C1["Pay.sh"]
    C2["Circle Agent Marketplace"]
    C3["PayAI"]
    C4["Coinbase x402 Bazaar"]
  end

  PG[("PostgreSQL<br/>authoritative state")]
  REDIS[("Redis<br/>rate limits")]
  SOL["Solana<br/>USDC, Spend Pass program 46gQ…9tdS"]
  PROV["Paid API providers"]

  APP --> REST
  AGENT --> MCP
  CONSOLE --> REST
  REST --> TEN
  REST --> EXEC
  MCP --> EXEC
  EXEC --> GUARD
  EXEC --> PASS
  EXEC --> ECON
  GUARD --> INTEL
  ECON --> X402
  ECON --> CHAN
  ECON --> JUP
  ECON --> SBX
  ECON --> RCPT
  EXEC --> VAULT
  INTEL --> Catalogs
  X402 --> SOL
  CHAN --> SOL
  JUP --> SOL
  X402 --> PROV
  API --> PG
  REST --> REDIS
```

## 2. Discovery and routing

```mermaid
flowchart TD
  REQ["POST /api/v1/execute<br/>capability, input, budget, strategy"] --> RESOLVE["execution_resolve<br/>class of work or one endpoint"]
  RESOLVE --> CANDS["Candidates<br/>operator providers, catalog classes,<br/>web discovery, caller's own"]
  CANDS --> NET{"Network allowed for this<br/>key, pass and request?"}
  NET -- no --> REJ["rejected: other network"]
  NET -- yes --> HEALTH{"health_service<br/>last free probe ok?"}
  HEALTH -- no --> REJ2["rejected: provider_down"]
  HEALTH -- yes --> TRUST{"intel_service<br/>blocked in the registry?"}
  TRUST -- yes --> REJ3["rejected: PROVIDER_BLOCKED"]
  TRUST -- no --> QUOTE["Unpaid 402 request to each,<br/>in parallel (x402client)"]
  QUOTE --> PRICE{"Live price within 5% of listing<br/>and under 10x the class's usual?"}
  PRICE -- no --> REJ4["rejected: price_above_listing<br/>or price_outlier"]
  PRICE -- yes --> PAYEE{"Payee equals the pinned payee?"}
  PAYEE -- no --> REJ5["rejected: PAYEE_CHANGED,<br/>endpoint blocked"]
  PAYEE -- yes --> RANK["domain/routing Rank<br/>cheapest, fastest or auto"]
  RANK --> PLAN["Plan: ranked offers, plan hash"]
  PLAN --> TRY["Attempt rank 1"]
  TRY --> OK{"Delivered?"}
  OK -- yes --> DONE["Answer + receipt"]
  OK -- "no, and the coordinator proves<br/>no money moved" --> NEXT["Attempt next rank"]
  NEXT --> OK
  OK -- "outcome unknown" --> FREEZE["Intent frozen: reconcile from the chain"]
```

## 3. Solana payment through an on-chain Spend Pass

```mermaid
sequenceDiagram
  autonumber
  participant O as Owner wallet
  participant P as Spend Pass program (46gQ…9tdS)
  participant A as Algebra API
  participant W as Algebra payer wallet
  participant S as x402 provider
  participant C as Solana

  O->>P: create_pass (limits, agent, expiry) + deposit USDC
  Note over P: vault token account owned by the pass PDA
  A->>A: API key and pass resolved, policy ALLOW, quote accepted
  A->>A: x402 payment signed by the payer, not yet released
  A->>P: pull(amount, intent) signed by the agent key
  P->>P: checks cap per call, budget, window, expiry, frozen, revoked, destination
  P->>W: USDC for exactly one payment
  A->>C: wait until the pull is confirmed
  A->>S: request with the signed x402 payment
  S->>C: co-signs as fee payer and submits
  A->>C: settlement proven from chain state
  S-->>A: the answer
  A->>A: verify answer, commit intent, sign receipt
  alt payment never landed
    A->>P: refund(amount, intent) back to the vault, from chain proof
  end
  O->>P: set_frozen / withdraw / revoke / close_pass at any time
```

## 4. Policy decisions

```mermaid
flowchart TD
  IN["Request under a Spend Pass"] --> KS{"Kill switch on,<br/>or pass frozen / revoked / expired?"}
  KS -- yes --> BLOCK["BLOCK"]
  KS -- no --> ENV{"Network belongs to the pass's<br/>environment? Mainnet enabled?"}
  ENV -- no --> BLOCK
  ENV -- yes --> PROVIDER{"Provider allowed by the pass?"}
  PROVIDER -- no --> BLOCK
  PROVIDER -- yes --> CAP{"Within per-call cap<br/>and remaining budget?"}
  CAP -- no --> BLOCK
  CAP -- yes --> VEL{"Within calls per minute<br/>(pass and provider)?"}
  VEL -- no --> BLOCK
  VEL -- yes --> NEW{"Provider never paid before?"}
  NEW -- "rule: approve" --> HOLD["HOLD: AWAITING_APPROVAL<br/>a person approves in the console"]
  NEW -- "rule: cap" --> CAPNEW{"At or under the<br/>new-provider cap?"}
  CAPNEW -- no --> HOLD
  CAPNEW -- yes --> LINE
  NEW -- "rule: allow, or paid before" --> LINE{"Above the approval line?"}
  LINE -- yes --> HOLD
  LINE -- no --> ALLOW["ALLOW"]
  ALLOW --> RECHECK["Re-checked just before the wallet signs<br/>(authority.rechecked)"]
  RECHECK --> ONCHAIN["On-chain pass: the program checks<br/>the same limits again in pull"]
```

The model never takes part in this decision. It can propose a request; the
pass, the coordinator and the program decide.

## 5. Provider verification

```mermaid
stateDiagram-v2
  [*] --> Unverified: listed by a catalog (payee pinned if the listing names one)
  Unverified --> Unverified: quoted (unpaid 402 returned terms)
  Unverified --> Verified: paid call delivered to the pinned payee
  Unverified --> Blocked: listing or quote names a different payee
  Verified --> Blocked: payee changed, or a person blocks it
  Unverified --> Blocked: a person blocks it
  Blocked --> Unverified: a person unblocks it (verification must be earned again)
  Verified --> Unverified: a person removes verification
```

```mermaid
flowchart LR
  JOB["scheduled_jobs row<br/>0 */6 * * * UTC"] --> CLAIM{"ClaimJob: lease taken<br/>by exactly one instance"}
  CLAIM -- lost --> SKIP["Another instance refreshes"]
  CLAIM -- won --> SRC["For each catalog:<br/>timeout, 3 retries, backoff"]
  SRC -- failed --> KEEP["Keep the last good listing,<br/>mark the source stale, record the error"]
  SRC -- ok --> MERGE["domain/intel Merge<br/>against the registry"]
  MERGE --> CH["provider_changes:<br/>added, removed, restored,<br/>price_changed, payee_changed"]
  MERGE --> REG["provider_endpoints"]
  CH --> SUS{"Payee changed, or price<br/>more than doubled?"}
  SUS -- yes --> FLAG["Marked suspicious;<br/>payee change blocks"]
  REG --> ROUTER["Router and payment-time check"]
```

## 6. Receipts and reconciliation

```mermaid
stateDiagram-v2
  [*] --> OPEN: intent.created
  OPEN --> AWAITING_APPROVAL: policy HOLD
  AWAITING_APPROVAL --> OPEN: a person approves
  OPEN --> RESERVED: reservation.acquired (budget held)
  RESERVED --> EXECUTING: execution.started
  EXECUTING --> COMMITTED: payment.confirmed + delivery verified
  EXECUTING --> OPEN: released, the rail proves nothing was paid
  EXECUTING --> UNKNOWN: timeout or ambiguous answer
  UNKNOWN --> RECONCILING: reconcile
  RECONCILING --> COMMITTED: the chain shows the payment
  RECONCILING --> OPEN: the payment can never land (blockhash expired)
  COMMITTED --> [*]: receipt.signed
```

```mermaid
flowchart LR
  COMMIT["Committed intent"] --> BODY["Receipt body:<br/>intent hash, plan hash, quote hash,<br/>provider, amount, network,<br/>transaction signature, result hash"]
  BODY --> SIGN["Ed25519 JWS<br/>app/economic_receipts"]
  SIGN --> STORE[("economic_receipts")]
  SIGN --> OUT["Returned with the answer"]
  OUT --> V1["POST /api/v1/receipts/verify"]
  OUT --> V2["/verify page"]
  OUT --> V3["cmd/verify-intent<br/>against /.well-known/jwks.json"]
  COMMIT --> KEEP["Answer kept sealed (AES-256-GCM)"]
  KEEP --> REPLAY["Same request again:<br/>replayed: true, no second payment"]
```
