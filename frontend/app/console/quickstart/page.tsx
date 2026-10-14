import { GetStarted } from "@/components/console/get-started";
import { QuickstartFlow } from "@/components/console/quickstart-flow";

// The guided run is the page: a pass, a key, a preview, a receipt and the code to
// call it from a project, in the sandbox (simulated USDC, no wallet). Below it,
// for the person who would rather use the chat or free test money on Solana's
// test network: the demo chat, then the three real steps (GetStarted).
export default function QuickstartPage() {
  return (
    <div className="mx-auto max-w-5xl">
      <QuickstartFlow />
      <section aria-labelledby="beyond-the-guide" className="mx-auto mt-12 max-w-3xl border-t border-border pt-10">
        <h2 id="beyond-the-guide" className="font-display text-xl font-semibold text-foreground">
          Prefer the chat, or ready for test money?
        </h2>
        <p className="mt-1 text-sm leading-relaxed text-muted">
          Ask the agent in demo mode with no wallet, then go real on Solana&apos;s test network in three steps. A signed receipt at the end, and no address to copy anywhere.
        </p>
        <GetStarted />
      </section>
    </div>
  );
}
