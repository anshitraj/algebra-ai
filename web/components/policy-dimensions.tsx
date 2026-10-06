import { Container } from "./container";

const rows = [
  {
    word: "WHO",
    body: "The agent, and the Spend Pass it acts under.",
    field: "Reservation.ExecutorAgentID / ExecutorPassID",
  },
  {
    word: "WHAT",
    body: "The outcome it asked for: a capability and a hash of its input.",
    field: "Intent.Capability / InputHash",
  },
  {
    word: "WHERE",
    body: "The provider that would be paid, which the pass must allow.",
    field: "Reservation.ProviderID",
  },
  {
    word: "HOW MUCH",
    body: "The quote against the budget, and what the pass has already used.",
    field: "QuoteMinor / PassExposure",
  },
  {
    word: "WITH WHAT",
    body: "The rail and the asset: USDC at Circle's real address on Solana, and nothing else.",
    field: "Rail / Network / Asset",
  },
  {
    word: "WHY",
    body: "The category the pass allows, such as digital services.",
    field: "Pass.AllowedCategories",
  },
  {
    word: "UNDER WHAT CONDITIONS",
    body: "Per-call and total budget, the ask-me line, expiry, and one live attempt per intent.",
    field: "Pass{MaxPerPurchase, Budget, ApproveAbove, ExpiresAt}",
  },
];

export function PolicyDimensions() {
  return (
    <section id="policy" className="pt-20 pb-6 md:pt-28 md:pb-10">
      <Container>
        <div className="grid gap-10 md:grid-cols-[0.9fr_1.1fr] md:gap-16">
          <div>
            <h2 className="font-display text-3xl font-semibold tracking-tight text-foreground md:text-4xl">
              Seven questions, asked every time.
            </h2>
            <p className="mt-4 max-w-sm text-[1.0625rem] leading-relaxed text-muted">
              <code className="font-mono text-sm text-foreground">
                Spend Pass + intent
              </code>{" "}
              is the full context a decision is made from, assembled by
              Algebra from persisted state and never supplied by the agent.
            </p>
          </div>

          <dl>
            {rows.map((row) => (
              <div
                key={row.word}
                className="grid gap-1 border-b border-border py-5 first:pt-0 last:border-b-0 sm:grid-cols-[minmax(0,10rem)_1fr] sm:gap-6"
              >
                <dt className="font-display text-sm font-semibold tracking-tight text-primary">
                  {row.word}
                </dt>
                <dd>
                  <p className="text-sm leading-relaxed text-foreground">
                    {row.body}
                  </p>
                  <p className="mt-1.5 font-mono text-xs text-muted">
                    {row.field}
                  </p>
                </dd>
              </div>
            ))}
          </dl>
        </div>
      </Container>
    </section>
  );
}
