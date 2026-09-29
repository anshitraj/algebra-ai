import type { Metadata } from "next";
import { Suspense } from "react";
import { Nav } from "@/components/nav";
import { Footer } from "@/components/footer";
import { VerifyReceipt } from "./verify-receipt";

export const metadata: Metadata = {
  title: "Verify a spend receipt — Algebra",
  description: "Check that a purchase an AI agent made was really authorized by the person — no account needed.",
};

export default function VerifyPage() {
  return (
    <>
      <Nav />
      <main className="pt-14 pb-24 md:pt-20">
        <div className="mx-auto max-w-2xl px-6">
          <h1 className="font-display text-[2rem] leading-tight font-semibold tracking-tight text-foreground md:text-[2.4rem]">
            Verify a spend receipt
          </h1>
          <p className="mt-3 max-w-xl text-[1rem] leading-relaxed text-muted">
            When an AI agent buys through Algebra, the order carries a receipt signed by Algebra: proof that the person authorized exactly this
            purchase — the store, the items, the amount — and whether their own rules or they themselves said yes. Paste one to check it. It
            carries nothing personal, and you don&apos;t need an account.
          </p>
          <div className="mt-8">
            <Suspense>
              <VerifyReceipt />
            </Suspense>
          </div>
          <p className="mt-10 text-sm text-muted">
            Verifying in code? Receipts are compact JWS signed with Ed25519. Fetch the public key set at{" "}
            <a href="/.well-known/jwks.json" className="font-medium text-primary underline decoration-primary/30 underline-offset-4">
              /.well-known/jwks.json
            </a>{" "}
            or POST <code className="font-mono text-xs">{`{"receipt": "…"}`}</code> to <code className="font-mono text-xs">/api/v1/receipts/verify</code>.
          </p>
        </div>
      </main>
      <Footer />
    </>
  );
}
