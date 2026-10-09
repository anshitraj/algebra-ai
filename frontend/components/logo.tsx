"use client";

/** Two interlocking gates: the agent and its spending authority. */
export function Logo({ size = 32, className = "" }: { size?: number; className?: string }) {
  return (
    <svg viewBox="0 0 40 40" width={size} height={size} className={`algebra-mark ${className}`} fill="none" role="img" aria-label="Algebra">
      <path className="mark-left" d="M18 5H8v24h10V19H8" stroke="currentColor" strokeWidth="4" strokeLinejoin="round" />
      <path className="mark-right" d="M22 35h10V11H22v10h10" stroke="currentColor" strokeWidth="4" strokeLinejoin="round" />
    </svg>
  );
}
