import { Container } from "./container";

const REQUEST = `POST /api/v1/execute
Authorization: Bearer <spend_pass_token>

{
  "capability": "birdeye.data.get.x402-defi-token_security",
  "input": { "address": "So11111111111111111111111111111111111111112" },
  "providers": ["paysh:birdeye.data"],
  "budget_max_minor": 10000
}`;

const RESPONSE = `{
  "delivered": true,
  "stopped": "committed",
  "summary": "Committed: paid once and the result was received.",
  "intent": { "state": "COMMITTED", "commitment": "SETTLED", "committed_minor": 3000 },
  "response": { ...the provider's data, untrusted... },
  "receipt": "eyJhbGciOiJFZERTQSIs…"
}`;

export function IntegrateSection() {
  return (
    <section id="integrate" className="pt-20 pb-20 md:pt-28 md:pb-28">
      <Container>
        <div className="grid gap-12 md:grid-cols-2 md:items-center md:gap-16">
          <div>
            <h2 className="font-display text-3xl font-semibold tracking-tight text-foreground md:text-4xl">
              Bring your own agent.
            </h2>
            <p className="mt-4 text-[1.0625rem] leading-relaxed text-muted">
              Any agent that can make an HTTP call or speak MCP can use Algebra. Issue a Spend Pass in the console, hand
              the agent its token, and it asks for outcomes: <code className="font-mono text-sm text-foreground">algebra.execute</code>{" "}
              over MCP, or <code className="font-mono text-sm text-foreground">POST /api/v1/execute</code> over REST.
              It can browse what is on offer with{" "}
              <code className="font-mono text-sm text-foreground">algebra.discover_providers</code>.
            </p>
            <p className="mt-4 text-sm leading-relaxed text-muted">
              The pass is the agent&rsquo;s whole authority. It never receives a key, a card or a payment value, and every
              payment ends in a receipt that anyone can verify against Algebra&rsquo;s published keys.
            </p>
            <div className="mt-8 flex flex-wrap gap-4">
              <a
                href="https://github.com/anshitraj/algebra-ai/blob/main/docs/EXECUTION.md"
                className="rounded-full bg-primary px-5 py-2.5 text-sm font-medium text-primary-tint transition-transform hover:scale-[1.03] active:scale-[0.98]"
              >
                Read the execution guide
              </a>
              <a
                href="https://github.com/anshitraj/algebra-ai/blob/main/docs/ECONOMIC_COORDINATION.md"
                className="text-sm font-medium text-foreground underline decoration-border-strong underline-offset-4 transition-colors hover:decoration-foreground"
              >
                How a payment is never made twice
              </a>
            </div>
          </div>

          <div className="overflow-hidden rounded-2xl border border-border bg-surface shadow-[0_1px_2px_rgba(11,16,32,0.06),0_24px_48px_-24px_rgba(11,16,32,0.28)]">
            <div className="border-b border-border px-4 py-3">
              <span className="font-mono text-xs text-muted">curl -X POST /api/v1/execute</span>
            </div>
            <pre className="overflow-x-auto px-4 py-4 font-mono text-[0.75rem] leading-relaxed text-foreground">{REQUEST}</pre>
            <div className="border-t border-border bg-primary-tint/40 px-4 py-3">
              <span className="font-mono text-xs text-muted">response</span>
            </div>
            <pre className="overflow-x-auto px-4 py-4 font-mono text-[0.75rem] leading-relaxed text-primary">{RESPONSE}</pre>
          </div>
        </div>
      </Container>
    </section>
  );
}
