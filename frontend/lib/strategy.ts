// How Algebra picks among providers that can do the same work. The router
// ranks every provider that passed its guards (down, overcharging and
// trap-priced ones are already out) by one of these.

export type Pick = "cheapest" | "balanced" | "fastest";

export const PICKS: { id: Pick; label: string; hint: string }[] = [
  { id: "cheapest", label: "Cheapest", hint: "The lowest live price, fees included." },
  { id: "balanced", label: "Balanced", hint: "Price, speed, reliability, quality and price honesty together." },
  {
    id: "fastest",
    label: "Fastest",
    hint: "The quickest measured response: Algebra's own paid calls when it has five or more, otherwise how fast the provider answered its price check. With no speed known at all, it falls back to price and says so.",
  },
];

export function isPick(v: unknown): v is Pick {
  return v === "cheapest" || v === "balanced" || v === "fastest";
}

/** The label for a strategy or router mode ("AUTO", "auto", "balanced" all read "Balanced"). */
export function pickLabel(v: unknown): string {
  const s = typeof v === "string" ? v.toLowerCase() : "";
  if (s === "auto" || s === "balanced" || s === "best_execution") return "Balanced";
  if (s === "cheapest") return "Cheapest";
  if (s === "fastest") return "Fastest";
  return "";
}

/** How a speed was measured, in words: "median of 8 paid calls", "price check". */
export function latencySourceText(source: string | undefined, calls?: number): string {
  switch (source) {
    case "observed":
      return calls ? `median of ${calls} paid calls` : "measured on paid calls";
    case "price_check":
      return "timed on its price check";
    case "estimate":
      return "estimated";
  }
  return "not measured yet";
}
