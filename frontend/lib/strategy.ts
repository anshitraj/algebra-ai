// How Algebra picks among providers that can do the same work. The router
// ranks every provider that passed its guards (down, overcharging and
// trap-priced ones are already out) by one of these.

export type Pick = "cheapest" | "balanced" | "fastest";

export const PICKS: { id: Pick; label: string; hint: string }[] = [
  { id: "cheapest", label: "Cheapest", hint: "The lowest live price, fees included." },
  { id: "balanced", label: "Balanced", hint: "Price, speed, reliability, quality and price honesty together." },
  { id: "fastest", label: "Fastest", hint: "The shortest time to a verified result, judged from how each provider has actually performed." },
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
