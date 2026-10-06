import { Container } from "./container";
import { ProviderLogo } from "./provider-logo";
import { FEATURED_PROVIDERS } from "@/lib/paysh";

/** The paid APIs an agent can reach, right under the hero. */
export function ProviderStrip() {
  return (
    <section aria-labelledby="provider-strip-heading" className="pt-4 pb-10 md:pt-6 md:pb-14">
      <Container>
        <p id="provider-strip-heading" className="text-center text-sm text-muted">
          Paid APIs your agent can call today, from Pay.sh, Circle&apos;s Agent Marketplace and PayAI
        </p>
        <ul className="mx-auto mt-6 flex max-w-4xl flex-wrap items-center justify-center gap-x-7 gap-y-4 md:gap-x-9">
          {FEATURED_PROVIDERS.map((p) => (
            <li key={p.id} className="flex items-center gap-2.5">
              <ProviderLogo name={p.name} host={p.host} size={30} />
              <span className="text-[0.95rem] font-medium tracking-tight text-foreground/85">{p.name}</span>
            </li>
          ))}
        </ul>
      </Container>
    </section>
  );
}
