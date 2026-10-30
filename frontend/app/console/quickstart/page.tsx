import { QuickstartFlow } from "@/components/console/quickstart-flow";

// The guided run is the page: a pass, a key, a preview, a receipt and the code to
// call it from a project, in the sandbox (simulated USDC, no wallet).
export default function QuickstartPage() {
  return (
    <div className="mx-auto max-w-5xl">
      <QuickstartFlow />
    </div>
  );
}
