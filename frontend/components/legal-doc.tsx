import { Container } from "./container";
import { LEGAL } from "@/lib/legal";

/** Reading layout for the legal pages: one column at a comfortable measure. */
export function LegalDoc({ title, intro, children }: { title: string; intro: string; children: React.ReactNode }) {
  return (
    <Container>
      <article className="mx-auto max-w-[68ch]">
        <h1 className="font-display text-4xl font-semibold tracking-tight text-balance text-foreground md:text-5xl">{title}</h1>
        <p className="mt-3 text-sm text-muted">Last updated {LEGAL.updated}</p>
        <p className="mt-8 text-[1.05rem] leading-relaxed text-foreground">{intro}</p>
        <div className="legal-body mt-10 space-y-10">{children}</div>
      </article>
    </Container>
  );
}

export function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section>
      <h2 className="font-display text-xl font-semibold tracking-tight text-foreground">{title}</h2>
      <div className="mt-3 space-y-3 text-[0.975rem] leading-relaxed text-foreground/90 [&_a]:text-primary [&_a]:underline [&_li]:ml-5 [&_li]:list-disc [&_li]:pl-1 [&_ul]:space-y-1.5">
        {children}
      </div>
    </section>
  );
}

/** How to reach the business — only what's configured, never a placeholder. */
export function ContactLines() {
  return (
    <ul>
      <li>{LEGAL.entity}{LEGAL.address ? `, ${LEGAL.address}` : ""}</li>
      {LEGAL.supportEmail && (
        <li>
          Email: <a href={`mailto:${LEGAL.supportEmail}`}>{LEGAL.supportEmail}</a>
        </li>
      )}
      {(LEGAL.grievanceName || LEGAL.grievanceEmail) && (
        <li>
          Grievance Officer{LEGAL.grievanceName ? `: ${LEGAL.grievanceName}` : ""}
          {LEGAL.grievanceEmail && (
            <>
              {" "}
              — <a href={`mailto:${LEGAL.grievanceEmail}`}>{LEGAL.grievanceEmail}</a>
            </>
          )}
        </li>
      )}
      <li>
        Or see <a href="/contact">all the ways to reach us</a>.
      </li>
    </ul>
  );
}
