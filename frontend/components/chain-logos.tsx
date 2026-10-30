// Chain marks: Solana, Base and Arc (the chains the vault and the catalogs
// speak), and the chains that are coming next. Solana's is its three-bar logo,
// Base's its blue square, Arc's its arch in Arc's own gradient.

import { useId } from "react";

/** The Solana mark (three bars, Solana's gradient). */
export function SolanaMark({ size = 18, className = "" }: { size?: number; className?: string }) {
  const id = useId();
  return (
    <svg viewBox="0 0 397.7 311.7" width={size} height={(size * 311.7) / 397.7} className={className} role="img" aria-label="Solana">
      <defs>
        <linearGradient id={id} x1="360.9" y1="351.5" x2="141.2" y2="-69.3" gradientUnits="userSpaceOnUse">
          <stop offset="0" stopColor="#00FFA3" />
          <stop offset="1" stopColor="#DC1FFF" />
        </linearGradient>
      </defs>
      <path fill={`url(#${id})`} d="M64.6 237.9c2.4-2.4 5.7-3.8 9.2-3.8h317.4c5.8 0 8.7 7 4.6 11.1l-62.7 62.7c-2.4 2.4-5.7 3.8-9.2 3.8H6.5c-5.8 0-8.7-7-4.6-11.1l62.7-62.7z" />
      <path fill={`url(#${id})`} d="M64.6 3.8C67.1 1.4 70.4 0 73.8 0h317.4c5.8 0 8.7 7 4.6 11.1l-62.7 62.7c-2.4 2.4-5.7 3.8-9.2 3.8H6.5c-5.8 0-8.7-7-4.6-11.1L64.6 3.8z" />
      <path fill={`url(#${id})`} d="M333.1 120.1c-2.4-2.4-5.7-3.8-9.2-3.8H6.5c-5.8 0-8.7 7-4.6 11.1l62.7 62.7c2.4 2.4 5.7 3.8 9.2 3.8h317.4c5.8 0 8.7-7 4.6-11.1l-62.7-62.7z" />
    </svg>
  );
}

/** "Powered by Solana": the mark and the name. */
export function PoweredBySolana({ className = "" }: { className?: string }) {
  return (
    <a
      href="https://solana.com"
      target="_blank"
      rel="noopener noreferrer"
      className={`inline-flex items-center gap-2 rounded-xl border border-border bg-surface px-3 py-2 transition-colors hover:border-border-strong ${className}`}
    >
      <span className="text-[0.68rem] font-medium tracking-wide text-muted uppercase">Powered by</span>
      <SolanaMark size={18} />
      <span className="text-sm font-semibold tracking-tight text-foreground">Solana</span>
    </a>
  );
}

/** The Base mark (the blue square). */
export function BaseMark({ size = 14, className = "" }: { size?: number; className?: string }) {
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} className={className} role="img" aria-label="Base">
      <rect width="24" height="24" rx="5" fill="#0000FF" />
    </svg>
  );
}

/** The Arc mark (Circle's chain: the arch, in Arc's gradient). */
export function ArcMark({ size = 14, className = "" }: { size?: number; className?: string }) {
  const id = useId();
  return (
    <svg viewBox="0 0 31 32" width={(size * 31) / 32} height={size} className={className} role="img" aria-label="Arc">
      <defs>
        <linearGradient id={id} x1="0" y1="32" x2="31" y2="0" gradientUnits="userSpaceOnUse">
          <stop stopColor="#1C2998" />
          <stop offset="0.53" stopColor="#3E2B63" />
          <stop offset="1" stopColor="#942753" />
        </linearGradient>
      </defs>
      <path
        fill={`url(#${id})`}
        d="M0 32C0.260374 24.166 1.59328 16.8547 3.82135 11.1696C6.64316 3.96673 10.728 0 15.3227 0C19.9174 0 24.0016 3.96673 26.824 11.1696C28.292 14.9157 29.372 19.3668 30.0119 24.2089C30.0691 24.6414 30.1178 25.0809 30.1678 25.5195C30.184 25.5466 30.1938 25.5718 30.1905 25.5923C30.1905 25.5923 30.5666 27.9326 30.6465 32H30.604C30.0462 31.5439 23.4681 26.3931 12.5636 27.8845C12.7282 26.0457 12.9544 24.2565 13.2467 22.5415C13.2617 22.4538 13.2789 22.3692 13.2942 22.2821C17.5711 22.1536 21.3146 22.6486 24.1853 23.2972C24.1746 23.2293 24.1657 23.1594 24.1547 23.0918C23.5647 19.4302 22.6941 16.0779 21.5717 13.2131C19.7364 8.52888 17.3416 5.61852 15.3227 5.61852C13.3038 5.61852 10.909 8.52888 9.07379 13.2131C8.62954 14.3462 8.22512 15.5545 7.86244 16.8291C7.35258 18.615 6.92424 20.5296 6.58214 22.5413C6.0758 25.5124 5.75944 28.6987 5.64292 32H0Z"
      />
    </svg>
  );
}

/** Whether ChainMark has a mark for a network. */
export function hasChainMark(network: string | undefined): boolean {
  return /^(solana|base|arc)/i.test(network ?? "");
}

/** A chain's mark by network name ("solana", "base-sepolia", "arc"…); null for a chain without one. */
export function ChainMark({ network, size = 14, className = "" }: { network: string | undefined; size?: number; className?: string }) {
  const n = (network ?? "").toLowerCase();
  if (n.startsWith("solana")) return <SolanaMark size={size} className={className} />;
  if (n.startsWith("base")) return <BaseMark size={size} className={className} />;
  if (n.startsWith("arc")) return <ArcMark size={size} className={className} />;
  return null;
}

/** A chain's name for people: "Solana", "Base", "Arc" (test networks say so). */
export function chainName(network: string | undefined): string {
  const n = (network ?? "").toLowerCase();
  if (n === "keyed") return "Your API key";
  const test = /devnet|sepolia|testnet/.test(n);
  const name = n.startsWith("solana") ? "Solana" : n.startsWith("base") ? "Base" : n.startsWith("arc") ? "Arc" : network ?? "";
  return test ? `${name} (test)` : name;
}

function EthereumMark({ size = 14 }: { size?: number }) {
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden>
      <path d="M12 1.5 5.5 12.2 12 16l6.5-3.8L12 1.5z" fill="#627EEA" />
      <path d="M12 17.3 5.5 13.4 12 22.5l6.5-9.1-6.5 3.9z" fill="#8FA4F0" />
    </svg>
  );
}

function ArbitrumMark({ size = 14 }: { size?: number }) {
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden>
      <path d="M12 1.8 21 7v10l-9 5.2L3 17V7l9-5.2z" fill="#213147" />
      <path d="m12.6 7.2 4.2 9.8-1.6.9-4.2-9.9 1.6-.8zm-3.1 2.4 3 7.3-1.6.9-2.7-6.6 1.3-1.6z" fill="#28A0F0" />
    </svg>
  );
}

function PolygonMark({ size = 14 }: { size?: number }) {
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden>
      <path d="M16.4 8.3a1.3 1.3 0 0 0-1.3 0l-2.9 1.7-2 1.1-2.9 1.7a1.3 1.3 0 0 1-1.3 0l-2.3-1.3a1.3 1.3 0 0 1-.6-1.1V7.8c0-.5.2-.9.6-1.1l2.3-1.3a1.3 1.3 0 0 1 1.3 0l2.3 1.3c.4.2.6.6.6 1.1v1.7l2-1.2V6.6c0-.5-.2-.9-.6-1.1L7.4 2.9a1.3 1.3 0 0 0-1.3 0L1.6 5.5c-.4.2-.6.6-.6 1.1v5.2c0 .5.2.9.6 1.1l4.5 2.6c.4.2.9.2 1.3 0l2.9-1.7 2-1.1 2.9-1.7a1.3 1.3 0 0 1 1.3 0l2.3 1.3c.4.2.6.6.6 1.1v2.6c0 .5-.2.9-.6 1.1l-2.3 1.3a1.3 1.3 0 0 1-1.3 0l-2.3-1.3a1.3 1.3 0 0 1-.6-1.1v-1.7l-2 1.2v1.7c0 .5.2.9.6 1.1l4.5 2.6c.4.2.9.2 1.3 0l4.5-2.6c.4-.2.6-.6.6-1.1v-5.2c0-.5-.2-.9-.6-1.1l-4.5-2.6z" fill="#7B3FE4" />
    </svg>
  );
}

const NEXT = [
  { name: "Base", mark: BaseMark },
  { name: "Arc", mark: ArcMark },
  { name: "Ethereum", mark: EthereumMark },
  { name: "Arbitrum", mark: ArbitrumMark },
  { name: "Polygon", mark: PolygonMark },
];

/** The chains Algebra plans next: listed, not live. */
export function ComingSoonChains({ className = "" }: { className?: string }) {
  return (
    <div className={className}>
      <p className="text-[0.68rem] font-medium tracking-wide text-muted uppercase">Coming soon</p>
      <ul className="mt-2 flex flex-wrap gap-1.5">
        {NEXT.map(({ name, mark: Mark }) => (
          <li key={name} className="inline-flex items-center gap-1.5 rounded-lg border border-dashed border-border px-2 py-1 text-xs text-muted">
            <span className="opacity-70 grayscale-[35%]">
              <Mark />
            </span>
            {name}
          </li>
        ))}
      </ul>
    </div>
  );
}
