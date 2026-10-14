// Pure helpers behind the comparison cards in the chat, kept out of the component so they can be tested.

import type { Recommendation } from "../routing-types";
import type { OptionGroup } from "./events";

/**
 * When every offer in a comparison is refused, the reasons they share, each
 * once, so the list can say it one time instead of on every row. Null when at
 * least one offer can be paid or approved.
 */
export function sharedRefusal(recs: Recommendation[]): string[] | null {
  if (recs.length === 0 || recs.some((r) => r.decision !== "BLOCK")) return null;
  const seen = new Set<string>();
  const out: string[] = [];
  for (const r of recs) {
    for (const reason of r.reasons ?? []) {
      const code = reason.split(":")[0].trim();
      if (seen.has(code)) continue;
      seen.add(code);
      out.push(reason);
    }
  }
  return out;
}

export type AutoPick = { group: OptionGroup; rec: Recommendation; ways: string[] };

/** The most comparisons paid in one go without asking, so a long list of work is never paid in a single stroke. */
export const MAX_AUTO_PAY = 3;

/**
 * What to pay at once for a person who turned "ask before paying" off: per
 * comparison, the recommended (balanced) offer, and only when the Spend Pass
 * allows it outright (not one that waits for a yes, not one refused, not a
 * price that is only a listing), within what the pass has left. The server
 * still checks every payment; this only decides which cards are pressed.
 */
export function autoPicks(groups: OptionGroup[], remainingMinor: number | undefined): AutoPick[] {
  const picks: AutoPick[] = [];
  let left = remainingMinor ?? Number.POSITIVE_INFINITY;
  for (const g of groups) {
    if (picks.length >= MAX_AUTO_PAY) break;
    const rec = g.recommendations.find((r) => r.strategy === "balanced");
    if (!rec || rec.decision !== "ALLOW" || rec.price_source === "unlisted") continue;
    if (rec.total_minor > left) continue;
    left -= rec.total_minor;
    const ways = g.recommendations.filter((r) => r.candidate_id === rec.candidate_id && r.strategy).map((r) => r.strategy as string);
    picks.push({ group: g, rec, ways: ways.length ? ways : ["balanced"] });
  }
  return picks;
}
