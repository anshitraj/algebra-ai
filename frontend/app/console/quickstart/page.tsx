import { PageHeader } from "@/components/console/ui";
import { GetStarted } from "@/components/console/get-started";
import { QuickstartFlow } from "@/components/console/quickstart-flow";

// Two ways in, in the order a new person needs them. First the plain one: try a
// question in practice mode, then go real on Solana's test network in three
// steps (GetStarted). Below it, folded away, the guided sandbox run for a
// developer: a pass, a key, a preview, a receipt, then the code to call it
// from their own project (QuickstartFlow, which the landing page's demo uses too).
export default function QuickstartPage() {
  return (
    <div className="mx-auto max-w-5xl">
      <div className="mx-auto max-w-3xl">
        <PageHeader
          title="Quickstart"
          description="Try a question in practice mode first, then go real in three steps with free test money. A signed receipt at the end, and no address to copy anywhere."
        />
        <GetStarted />
      </div>

      <details className="mt-10 rounded-lg border border-border bg-surface open:pb-5">
        <summary className="cursor-pointer px-5 py-4 text-sm font-semibold text-foreground">For developers: a sandbox API key, a preview and the code</summary>
        <div className="px-2 sm:px-5">
          <QuickstartFlow />
        </div>
      </details>
    </div>
  );
}
