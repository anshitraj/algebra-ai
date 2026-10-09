import Link from "next/link";
import { Container } from "./container";
import { Logo } from "./logo";
import { DecisionStrip } from "./decision-strip";

export function Hero() {
  return (
    <section className="relative overflow-hidden pt-16 pb-8 md:pt-20 md:pb-12">
      <div aria-hidden="true" className="brand-glow pointer-events-none absolute inset-x-0 -top-32 h-[640px]" />
      <Container className="relative grid gap-14 md:grid-cols-[1.15fr_0.85fr] md:items-center md:gap-10">
        <div>
          <p className="inline-flex items-center gap-2 rounded-full border border-border bg-surface px-3 py-1 font-mono text-xs text-muted">
            <span aria-hidden="true" className="h-1.5 w-1.5 rounded-full bg-primary" />
            Solana · x402 · USDC
          </p>
          <h1 className="font-display mt-5 text-[2.75rem] leading-[1.04] font-semibold tracking-tight text-balance text-foreground md:text-[3.75rem]">
            Give agents permission to spend,
            <span className="text-primary"> not access to money.</span>
          </h1>
          <p className="mt-6 max-w-xl text-[1.0625rem] leading-relaxed text-muted">
            Algebra is the policy-aware execution layer for autonomous agents on Solana. Your agent asks for an outcome.
            Algebra finds the best paid API across Pay.sh, Circle&apos;s Agent Marketplace, PayAI and Coinbase&apos;s Bazaar, checks your Spend Pass, pays in USDC over x402, verifies what came back
            and signs a receipt. The agent never holds a key.
          </p>

          <div className="mt-9 flex flex-wrap items-center gap-4">
            <Link
              href="/signup"
              className="rounded-xl bg-primary px-6 py-3 text-sm font-medium text-primary-tint shadow-[0_8px_20px_-10px_color-mix(in_srgb,var(--color-primary)_85%,transparent)] transition-transform hover:scale-[1.02] active:scale-[0.98]"
            >
              Get started
            </Link>
            <a
              href="#get-started"
              className="text-sm font-medium text-foreground underline decoration-border-strong underline-offset-4 transition-colors hover:decoration-foreground"
            >
              See the four steps
            </a>
          </div>

          <div className="mt-10 flex items-center gap-3 text-xs text-muted">
            <Logo size={18} className="opacity-70" />
            <span>Policy decisions are computed server-side and cannot be overridden by an LLM.</span>
          </div>
        </div>

        <div className="flex justify-center md:justify-end">
          <DecisionStrip />
        </div>
      </Container>
    </section>
  );
}
