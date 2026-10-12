import type { Metadata } from "next";
import Link from "next/link";
import { Container } from "@/components/container";
import { DemoRequestForm } from "@/components/demo-request-form";

export const metadata: Metadata = {
  title: "Book a demo — Algebra",
  description: "Tell us what your agents need to pay for. We'll show you Algebra running it on Solana devnet and reply by email.",
};

export default function BookDemoPage() {
  return (
    <Container>
      <div className="grid gap-12 lg:grid-cols-[minmax(0,5fr)_minmax(0,6fr)]">
        <header>
          <p className="font-mono text-xs tracking-widest text-muted uppercase">Book a demo</p>
          <h1 className="mt-3 font-display text-4xl font-semibold tracking-tight text-balance text-foreground md:text-5xl">See your use case run, end to end.</h1>
          <p className="mt-5 text-[1.05rem] leading-relaxed text-foreground/90">
            Tell us what your agents need to buy. We&apos;ll walk through it on Solana devnet: the providers Algebra finds, the price each one asks, the limits that
            decide, the payment and the signed receipt.
          </p>
          <h2 className="mt-10 font-display text-lg font-semibold text-foreground">What happens next</h2>
          <ol className="mt-3 list-decimal space-y-2 pl-5 text-[0.975rem] leading-relaxed text-foreground/90">
            <li>Your request is saved for our team. Nothing is sent to a third party.</li>
            <li>We reply by email to the address you give, usually within two business days.</li>
            <li>We agree a time with you by email. There is no calendar to pick from here.</li>
          </ol>
          <p className="mt-8 text-sm leading-relaxed text-muted">
            Just want to try it? <Link href="/signup" className="text-primary underline">Start building free</Link>: the sandbox needs no wallet and no card.
            Please don&apos;t put API keys, wallet keys or passwords in this form.
          </p>
        </header>
        <DemoRequestForm />
      </div>
    </Container>
  );
}
