// Chain marks for "Powered by Solana" and the chains that are coming next.
// The Solana mark is its official three-bar logo; the others are simple marks
// in each chain's own colour, shown muted because Algebra doesn't run there yet.

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

function BaseMark({ size = 14 }: { size?: number }) {
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden>
      <rect width="24" height="24" rx="5" fill="#0000FF" />
    </svg>
  );
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
