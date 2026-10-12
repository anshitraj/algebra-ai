import type { Metadata } from "next";
import Link from "next/link";
import { Container } from "@/components/container";
import { IconArrowRight, IconCheck } from "@/components/icons";

export const metadata: Metadata = {
  title: "Pricing — Algebra",
  description: "Free to build on. A seven-day Startup trial with no card. Provider and network costs are yours, paid from your own Spend Pass, and shown separately.",
};

// The plans mirror backend/internal/domain/tenancy/plans.go, which is what the
// API enforces (GET /api/v1/plans). Change both together.
type Plan = {
  id: string;
  name: string;
  price: string;
  cadence: string;
  blurb: string;
  projects: string;
  executions: string;
  cta: { label: string; href: string };
  featured?: boolean;
};

const PLANS: Plan[] = [
  { id: "free", name: "Free", price: "$0", cadence: "", blurb: "Build and test in the sandbox, and on Solana devnet.", projects: "1 project", executions: "100 on-chain executions a month", cta: { label: "Start building free", href: "/signup" } },
  { id: "builder", name: "Builder", price: "$19", cadence: "a month", blurb: "For one developer shipping an agent.", projects: "3 projects", executions: "2,000 on-chain executions a month", cta: { label: "Talk to us", href: "/book-demo?plan=builder" } },
  { id: "startup", name: "Startup", price: "$59", cadence: "a month", blurb: "For a team running agents in production.", projects: "10 projects", executions: "10,000 on-chain executions a month", cta: { label: "Try it for seven days", href: "/signup?next=/console/projects" }, featured: true },
  { id: "business", name: "Business", price: "$199", cadence: "a month", blurb: "For several products and teams under one organization.", projects: "50 projects", executions: "100,000 on-chain executions a month", cta: { label: "Talk to us", href: "/book-demo?plan=business" } },
  { id: "enterprise", name: "Enterprise", price: "Custom", cadence: "", blurb: "Your own limits, deployment and review process.", projects: "As many as you need", executions: "Volume agreed with you", cta: { label: "Book a demo", href: "/book-demo?plan=enterprise" } },
];

const EVERY_PLAN = [
  "Spend Passes: budget, per-call cap, approval line, allowed providers, calls per minute",
  "Kill switch, and the rule for providers never paid before",
  "Dry runs: ask what would happen without paying",
  "Provider guards: down, over its own listing, trap-priced, payee changed",
  "A signed receipt for every outcome, verifiable by anyone",
  "REST API and MCP server, with scoped API keys per environment",
  "Unlimited sandbox runs, never counted",
];

export default function PricingPage() {
  return (
    <Container>
      <header className="max-w-[60ch]">
        <p className="font-mono text-xs tracking-widest text-muted uppercase">Pricing</p>
        <h1 className="mt-3 font-display text-4xl font-semibold tracking-tight text-balance text-foreground md:text-5xl">Free to build on. Pay when your agents do real work.</h1>
        <p className="mt-5 text-[1.05rem] leading-relaxed text-foreground/90">
          Every plan includes the whole spend firewall. Plans differ in how many projects and on-chain executions they cover. Sandbox runs are free and
          unlimited on all of them.
        </p>
      </header>

      <div role="note" className="mt-8 max-w-[78ch] rounded-lg border border-border-strong bg-accent-tint px-5 py-4 text-sm leading-relaxed text-foreground">
        <strong className="font-semibold">Paid plans can&apos;t be bought online yet.</strong> There is no checkout here: no payment processor is connected for these
        plans. Free and the seven-day Startup trial work today, with no card. For a paid plan, <Link href="/book-demo" className="text-primary underline">talk to us</Link>.
      </div>

      <ul className="mt-10 grid gap-4 sm:grid-cols-2 lg:grid-cols-5">
        {PLANS.map((plan) => (
          <li key={plan.id} className={`flex flex-col rounded-lg border bg-surface p-5 ${plan.featured ? "border-primary" : "border-border"}`}>
            <div className="flex items-baseline justify-between gap-2">
              <h2 className="font-display text-lg font-semibold text-foreground">{plan.name}</h2>
              {plan.featured && <span className="rounded-full bg-primary-tint px-2 py-0.5 text-[0.7rem] font-medium text-primary">7-day trial</span>}
            </div>
            <p className="mt-3 text-foreground">
              <span className="font-display text-3xl font-semibold tracking-tight">{plan.price}</span>
              {plan.cadence && <span className="ml-1.5 text-sm text-muted">{plan.cadence}</span>}
            </p>
            <p className="mt-3 min-h-10 text-sm leading-snug text-muted">{plan.blurb}</p>
            <ul className="mt-4 space-y-2 border-t border-border pt-4 text-sm text-foreground/90">
              {[plan.projects, plan.executions].map((line) => (
                <li key={line} className="flex gap-2"><IconCheck size={14} className="mt-0.5 shrink-0 text-success" /><span>{line}</span></li>
              ))}
            </ul>
            <Link
              href={plan.cta.href}
              className={`mt-6 inline-flex min-h-10 items-center justify-center gap-2 rounded-md px-3 py-2 text-sm font-medium ${plan.featured || plan.id === "free" ? "bg-primary text-primary-tint hover:opacity-90" : "border border-border-strong text-foreground hover:bg-primary-tint"}`}
            >
              {plan.cta.label} <IconArrowRight size={14} />
            </Link>
          </li>
        ))}
      </ul>

      <div className="mt-14 grid gap-10 lg:grid-cols-2">
        <section aria-labelledby="trial">
          <h2 id="trial" className="font-display text-xl font-semibold tracking-tight text-foreground">The seven-day Startup trial</h2>
          <ul className="mt-4 space-y-2.5 text-[0.975rem] leading-relaxed text-foreground/90">
            <li>Start it from <Link href="/console/projects" className="text-primary underline">Projects &amp; API keys</Link> in the console. Owners and admins only.</li>
            <li>No card is asked for and nothing is charged.</li>
            <li>For seven days your organization has the Startup plan&apos;s limits.</li>
            <li>When it ends you are back on Free automatically. Nothing is deleted; projects beyond the Free limit stay, and you can&apos;t add more until you archive one or change plan.</li>
            <li>One trial per organization.</li>
          </ul>
        </section>

        <section aria-labelledby="costs">
          <h2 id="costs" className="font-display text-xl font-semibold tracking-tight text-foreground">What you pay, and to whom</h2>
          <dl className="mt-4 space-y-4 text-[0.975rem] leading-relaxed">
            <div>
              <dt className="font-medium text-foreground">Algebra&apos;s fee</dt>
              <dd className="text-foreground/90">The plan price above. Nothing is added to a provider&apos;s price.</dd>
            </div>
            <div>
              <dt className="font-medium text-foreground">Provider charges</dt>
              <dd className="text-foreground/90">What each paid API asks per call, in USDC, paid from your own Spend Pass straight to the provider. The receipt shows the exact amount and the transaction.</dd>
            </div>
            <div>
              <dt className="font-medium text-foreground">Network fees</dt>
              <dd className="text-foreground/90">Solana transaction fees. For x402 calls the provider&apos;s facilitator pays them. An on-chain Spend Pass adds a small fee per pull.</dd>
            </div>
          </dl>
          <p className="mt-4 text-sm text-muted">Algebra never pays a provider for you: a free plan does not include free paid APIs.</p>
        </section>
      </div>

      <section aria-labelledby="included" className="mt-14">
        <h2 id="included" className="font-display text-xl font-semibold tracking-tight text-foreground">On every plan</h2>
        <ul className="mt-4 grid gap-x-10 gap-y-2.5 text-[0.975rem] leading-relaxed text-foreground/90 md:grid-cols-2">
          {EVERY_PLAN.map((line) => (
            <li key={line} className="flex gap-2.5"><IconCheck size={15} className="mt-1 shrink-0 text-success" /><span>{line}</span></li>
          ))}
        </ul>
        <p className="mt-6 max-w-[78ch] text-sm leading-relaxed text-muted">
          &quot;On-chain executions&quot; are committed paid requests on Solana devnet or mainnet. Mainnet payments are switched off on this deployment until the mainnet
          safety checks are complete; discovery, quotes and dry runs against mainnet providers work today.
        </p>
        <p className="mt-3 max-w-[78ch] text-sm leading-relaxed text-muted">
          Two things are not built yet, so no plan sells them: inviting other people into an organization, and stopping requests at a plan&apos;s monthly execution
          allowance. Usage is counted and shown per project today; the project limit is enforced.
        </p>
      </section>
    </Container>
  );
}
