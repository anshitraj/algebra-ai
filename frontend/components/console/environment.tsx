import type { Environment } from "@/lib/platform-types";

/** What each environment can and can't do, in the order a person meets them: Demo, Devnet, Mainnet. Demo money is never shown as real, devnet USDC never as mainnet USDC. The API's id for Demo is "sandbox". */
export const ENVIRONMENTS: { id: Environment; label: string; detail: string }[] = [
  { id: "sandbox", label: "Demo", detail: "Simulated providers and simulated USDC (the API calls it the sandbox). No chain, no money." },
  { id: "devnet", label: "Devnet", detail: "Real Solana devnet transactions with test USDC. Explorer-verifiable." },
  { id: "mainnet", label: "Mainnet", detail: "Real USDC on Solana. Off unless the operator enables mainnet execution." },
];

export function EnvBadge({ env }: { env: Environment | string | undefined }) {
  const tone = env === "mainnet" ? "bg-danger-tint text-danger" : env === "devnet" ? "bg-accent-tint text-accent" : "bg-border text-muted";
  const label = ENVIRONMENTS.find((e) => e.id === env)?.label ?? env ?? "—";
  return <span className={`inline-flex shrink-0 items-center rounded-full px-2 py-0.5 text-xs font-medium whitespace-nowrap ${tone}`}>{label}</span>;
}
