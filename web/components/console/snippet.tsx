"use client";

import { useState } from "react";

/** A small copy-to-clipboard button. */
export function Copy({ text, label = "Copy" }: { text: string; label?: string }) {
  const [done, setDone] = useState(false);
  return (
    <button
      type="button"
      onClick={() =>
        navigator.clipboard?.writeText(text).then(
          () => {
            setDone(true);
            window.setTimeout(() => setDone(false), 1500);
          },
          () => {}
        )
      }
      className="shrink-0 rounded-md border border-border-strong px-2 py-1 text-xs font-medium text-foreground hover:bg-primary-tint"
    >
      {done ? "Copied" : label}
    </button>
  );
}

/** A titled block of code with a copy button. */
export function Snippet({ title, code, note }: { title: string; code: string; note?: string }) {
  return (
    <div>
      <div className="flex items-center justify-between gap-2">
        <p className="text-sm font-medium text-foreground">{title}</p>
        <Copy text={code} />
      </div>
      <pre className="mt-1.5 overflow-x-auto rounded-lg border border-border bg-background px-3 py-2.5 font-mono text-xs leading-relaxed text-foreground">{code}</pre>
      {note && <p className="mt-1 text-xs text-muted">{note}</p>}
    </div>
  );
}
