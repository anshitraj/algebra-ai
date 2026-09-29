"use client";

import { useState } from "react";
import { storeFor } from "@/lib/stores";
import { Logo } from "./logo";

/**
 * A store's own icon as a small app-icon tile. Unknown stores, and any icon
 * that fails to load, fall back to a plain monogram — never a guessed logo.
 * The simulated demo stores show Algebra's own mark, since that's who runs them.
 */
export function StoreLogo({ store, size = 24, className = "" }: { store: string | undefined; size?: number; className?: string }) {
  const [failed, setFailed] = useState(false);
  const s = storeFor(store);
  const radius = Math.round(size * 0.26);
  const box = `inline-flex shrink-0 items-center justify-center overflow-hidden ${className}`;

  if (store === "mock" || store === "demo_checkout" || /^demo/i.test(store ?? "")) {
    return (
      <span className={`${box} bg-primary-tint text-primary`} style={{ width: size, height: size, borderRadius: radius }} aria-hidden="true">
        <Logo size={Math.round(size * 0.7)} />
      </span>
    );
  }

  if (s && !failed && s.icon.kind === "mark") {
    const inner = Math.round(size * 0.62);
    return (
      <span className={box} style={{ width: size, height: size, borderRadius: radius, background: s.color }} role="img" aria-label={s.name}>
        <span
          style={{
            width: inner,
            height: inner,
            background: "#fff",
            WebkitMask: `url(${s.icon.src}) center / contain no-repeat`,
            mask: `url(${s.icon.src}) center / contain no-repeat`,
          }}
        />
      </span>
    );
  }

  if (s && !failed) {
    return (
      // Remote brand icons of varying hosts: a plain img with a monogram fallback.
      // eslint-disable-next-line @next/next/no-img-element
      <img
        src={s.icon.src}
        alt={s.name}
        width={size}
        height={size}
        loading="lazy"
        referrerPolicy="no-referrer"
        onError={() => setFailed(true)}
        className={`${box} border border-border bg-white object-contain`}
        style={{ width: size, height: size, borderRadius: radius }}
      />
    );
  }

  // A site that isn't a store we know (reddit.com, desidime.com): its own icon.
  if (!s && !failed && store && /^[a-z0-9-]+(\.[a-z0-9-]+)+$/i.test(store)) {
    return (
      // eslint-disable-next-line @next/next/no-img-element
      <img
        src={`https://www.google.com/s2/favicons?sz=64&domain=${store}`}
        alt=""
        width={size}
        height={size}
        loading="lazy"
        referrerPolicy="no-referrer"
        onError={() => setFailed(true)}
        className={`${box} border border-border bg-white object-contain`}
        style={{ width: size, height: size, borderRadius: radius, padding: Math.round(size * 0.14) }}
      />
    );
  }

  const name = s?.name ?? store ?? "?";
  return (
    <span
      className={`${box} font-semibold text-white`}
      style={{ width: size, height: size, borderRadius: radius, background: s?.color ?? "var(--color-muted)", fontSize: Math.round(size * 0.46) }}
      aria-label={name}
      role="img"
    >
      {name.trim().charAt(0).toUpperCase()}
    </span>
  );
}
