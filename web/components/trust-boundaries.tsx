import { Container } from "./container";

const boundaries = [
  {
    pair: "User ↔ Agent",
    body: "User authentication is separate from agent authorization. Logging in as a person and being trusted as an agent are two different facts: an agent's token can't approve a payment, change a limit or issue itself more authority.",
  },
  {
    pair: "Agent ↔ Algebra",
    body: "An agent is bound to one Spend Pass and asks for an outcome, never for a payment. It never holds a key, a card or a payment value, and what a provider sends back is handed over as data to read, not instructions to follow.",
  },
  {
    pair: "Algebra ↔ Policy",
    body: "ALLOW / DENY / REQUIRE_APPROVAL is computed server-side from persisted state and logged. A DENY is terminal. The pass budget is checked under a database lock, so two requests can't both fit into the same remaining budget.",
  },
  {
    pair: "Algebra ↔ Providers",
    body: "Providers are untrusted, and so are the catalogs' listings. Algebra reaches them only at public addresses, asks for a price before paying and checks it again at payment, never follows a redirect with a payment attached, and refuses a token that merely calls itself USDC.",
  },
  {
    pair: "Algebra ↔ Solana",
    body: "Algebra pays from a wallet it controls, so the pass is the cap and a hard per-payment ceiling sits in the payment rail itself. A payment is proven from chain state, not from a provider's word, and an unknown outcome is never retried as if it had failed.",
  },
];

export function TrustBoundaries() {
  return (
    <section id="security" className="pt-20 pb-6 md:pt-28 md:pb-10">
      <Container>
        <h2 className="font-display max-w-lg text-3xl font-semibold tracking-tight text-foreground md:text-4xl">
          Five boundaries, never collapsed into one.
        </h2>
        <p className="mt-4 max-w-xl text-[1.0625rem] leading-relaxed text-muted">
          A control plane is only as trustworthy as the walls between its
          parts. Here is every wall.
        </p>

        <div className="relative mt-16 max-w-2xl">
          <div
            aria-hidden="true"
            className="absolute top-1 bottom-1 left-[7px] w-px bg-border-strong"
          />
          <ol className="space-y-10">
            {boundaries.map((b) => (
              <li key={b.pair} className="relative pl-9">
                <span
                  aria-hidden="true"
                  className="absolute top-1 left-0 flex h-[15px] w-[15px] items-center justify-center rounded-full bg-primary-tint ring-4 ring-background"
                >
                  <span className="h-[6px] w-[6px] rounded-full bg-primary" />
                </span>
                <h3 className="font-display text-base font-semibold tracking-tight text-foreground">
                  {b.pair}
                </h3>
                <p className="mt-1.5 text-sm leading-relaxed text-muted">
                  {b.body}
                </p>
              </li>
            ))}
          </ol>
        </div>
      </Container>
    </section>
  );
}
