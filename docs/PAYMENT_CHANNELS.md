# Payment channels

What Algebra does with Solana payment channels today, what it does not, and what
the missing part would take. Written so that a decision about sessions can be made
on facts.

## What exists

**x402 `upto`**, over the Solana Foundation's payment-channels program
(`providers/paychan`, `providers/solanax402/upto.go`). For work whose price is
not known until it is done (an LLM answer billed by the token, say):

1. The seller's 402 offers an `upto` option with a **ceiling**.
2. Algebra's payer signs the channel's **open**, escrowing the ceiling.
3. The seller serves the call, then settles **what it really cost** from a signed
   voucher. In the same transaction the program pays the seller and refunds the
   payer's remainder (`settle_and_seal` + `distribute`).
4. Algebra records as spent what the chain shows the seller was paid, never the
   ceiling (`Rail.Settlement`).

Under a Spend Pass vault the ceiling is what is pulled from the vault for the
call; what was not spent returns to the vault (`onchain_passes.go`, `refund`).
The demo seller `meter` (`llm.chat`) does this on devnet.

**The payer's way out.** A seller that takes the open and never settles leaves the
ceiling escrowed. The program gives the payer an exit that needs nobody's help:

```
open ──request_close (payer)──▶ closing ──grace period, then seal (anyone)──▶ sealed ──withdraw_payer (payer)──▶ money back
```

`paychan.NextRecovery` reads the channel and says which step is next;
`solanax402.Rail.RecoverEscrow` takes it; the vault service asks for it on every
sweep once an attempt is `CHANNEL_RECOVERY_AFTER` (default 10 minutes) past its
deadline, and refunds the vault when the money is back. Until the remainder is
withdrawn a sealed channel is **not** final (`Settlement` is Pending), so nothing is
refunded from an empty payer. The demo seller `meterstall`
(`DEMO_PROVIDER_STALL=on`) opens a channel and never settles it, to watch this
happen on devnet. Do not assume every flow refunds in the same transaction as the
call: that is true when the seller settles; when it does not, the payer waits out the
channel's grace period.

## What does not exist

* **MPP sessions.** `routing.ExecMPP` is a name; no runner is registered for it, and
  nothing in the repo speaks MPP. A session channel (many calls, signed cumulative
  vouchers, one settlement) is not built and has not been tried against a real
  seller.
* **Batched or streamed settlement** of any kind.

## The use cases, and where each stands

| Use case | Should Algebra use a channel? | State | Priority |
|---|---|---|---|
| AI inference billed by tokens | Yes: x402 `upto` | Built; exit for a stalled seller built | High |
| Hundreds of calls to the same paid API | Yes: an MPP session, or a batched settlement | Not built | High |
| Streaming data billed by usage | Yes: a metered session | Not built | Medium |
| Weather for 10,000 locations | Only if the seller supports sessions | Not built; depends on the seller | Medium |

A channel is **one payer and one seller**. Choosing several APIs in one checkout (a
forecast from one seller, a token price from another) is several independent
payments, each with its own vault pull. Channels do not combine different sellers;
the useful later optimisation for a multi-select checkout is **one pull for the whole
basket**, a separate change.

## What a session would need

1. **A session intent.** The coordinator is built on one intent, one effect, one
   payment, committed at most once. A session is many calls under one escrow, so it
   needs its own model: a reservation for the ceiling that many calls consume,
   closed explicitly.
2. **Per-call identity** (a voucher id or sequence number), so a repeated call is
   recognised and never charged twice: today's duplicate-payment guard is per intent.
3. **A ceiling from the vault, once.** The session's ceiling is pulled from the vault
   when it opens, within the pass's per-call and total limits (a ceiling is a
   commitment), and what was not spent is refunded when it closes, by the same
   refund path, plus the exit above if the seller disappears.
4. **Policy in terms of a session:** the pass's per-call cap applies to a call, the
   ceiling to the session; the dry run and the cards must show both.
5. **Receipts:** one for the session (ceiling, spent, refund) and one per call.
6. **A seller to test against.** The first step is a demo seller that speaks the
   protocol, then the spec is read carefully against a real one before any claim is
   made about compatibility.

Until a seller that supports sessions exists in the catalogs Algebra reads, building
this would be building for no one; `upto` already covers metered work.
