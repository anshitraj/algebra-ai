import { Container } from "./container";
import { DashboardPreview } from "./dashboard-preview";

export function DashboardSection() {
  return (
    <section className="pt-6 pb-20 md:pt-10 md:pb-28">
      <Container>
        <div className="mx-auto max-w-xl text-center">
          <h2 className="font-display text-3xl font-semibold tracking-tight text-foreground md:text-4xl">
            Every decision, on the record.
          </h2>
          <p className="mt-4 text-[1.0625rem] leading-relaxed text-muted">
            The same console you use to approve a payment: the real coordinator events
            underneath it, and a signed receipt at the end, not a spinner standing in
            for what actually happened.
          </p>
        </div>

        <div className="mx-auto mt-12 max-w-4xl">
          <DashboardPreview />
        </div>
      </Container>
    </section>
  );
}
