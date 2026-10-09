import Link from "next/link";
import { Nav } from "@/components/nav";
import { Footer } from "@/components/footer";
import { Container } from "@/components/container";

export default function NotFound() {
  return (
    <>
      <Nav />
      <main className="py-28 md:py-36">
        <Container>
          <div className="mx-auto max-w-md">
            <p className="font-mono text-sm text-muted">404</p>
            <h1 className="mt-2 font-display text-3xl font-semibold tracking-tight text-foreground">There&apos;s nothing at this address</h1>
            <p className="mt-3 text-[0.95rem] leading-relaxed text-muted">The link may be old, or the page moved. Your agents and Spend Passes are where you left them.</p>
            <div className="mt-7 flex gap-2.5">
              <Link href="/console" className="inline-flex h-10 items-center rounded-xl bg-primary px-4 text-sm font-medium text-primary-tint">
                Open the console
              </Link>
              <Link href="/" className="inline-flex h-10 items-center rounded-xl border border-border-strong px-4 text-sm font-medium text-foreground hover:bg-surface">
                Home
              </Link>
            </div>
          </div>
        </Container>
      </main>
      <Footer />
    </>
  );
}
