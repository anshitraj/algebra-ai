# kite-wallet — a worked integration example

"Kite" is a hypothetical wallet app that stores and provides its users'
cards. This program simulates Kite calling Algebra's standalone
policy-evaluation surface — `POST /api/v1/policy/evaluate-transaction` —
before letting a card charge to *any* store go through, gated by Kite's own
budget rules. Real HTTP calls against a real running `cmd/api`, not a
canned transcript.

It also imports `policy.Rules` directly from
`github.com/project-algebra/algebra/policy` (a public, non-`internal/`
package) to build Kite's own conditions — proof that the Go package surface
works, not just REST.

## Run it

```bash
go run ./cmd/api            # terminal 1 — needs DATABASE_URL / ALGEBRA_MASTER_KEY, see .env.example
go run ./examples/kite-wallet   # terminal 2
```

## What it does

1. `POST /api/v1/integrators` — Kite registers itself and gets a bearer
   token, shown exactly once (the B2B counterpart to `POST /agents`).
2. Three real transactions, evaluated against Kite's own rules (max $200/tx,
   approval required at $50+, gambling blocked outright — not Algebra's
   rules; Algebra never assumes a default budget policy for an integrator):
   - A $8.50 coffee — **ALLOW**.
   - A $50.00 purchase, exactly at Kite's own approval threshold —
     **REQUIRE_APPROVAL**.
   - A $100.00 charge in a blocked category — **DENY**.

See [docs/legacy/INTEGRATING.md](../../../docs/legacy/INTEGRATING.md) for the full
request/response reference.
