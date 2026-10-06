/**
 * A provider's monogram: its first letter on the primary tint. Providers'
 * own logos are never fetched or imitated.
 */
export function ProviderMark({ name, size = 28, className = "" }: { name: string; size?: number; className?: string }) {
  const letter = (name.trim().match(/[A-Za-z0-9]/)?.[0] ?? "?").toUpperCase();
  return (
    <span
      aria-hidden="true"
      className={`font-display inline-flex shrink-0 items-center justify-center bg-primary-tint font-semibold text-primary ${className}`}
      style={{ width: size, height: size, borderRadius: Math.round(size * 0.28), fontSize: Math.round(size * 0.46) }}
    >
      {letter}
    </span>
  );
}
