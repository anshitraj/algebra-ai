import { createHash } from "node:crypto";
import type { Choice } from "./events";
import { isPick } from "../strategy";

// The cards a person clicked to pay, as the chat route receives them. Server-side only.

export const MAX_CHOICES = 8;
export const MAX_CHOICE_USDC = 5;

/**
 * parseChoices validates the cards a person clicked. They come from the
 * browser, so every field is checked and bounded; the server still applies
 * the pass, the router's guards and the price limit to each payment.
 */
export function parseChoices(raw: unknown): Choice[] | string {
  if (raw === undefined || raw === null) return [];
  if (!Array.isArray(raw) || raw.length > MAX_CHOICES) return `Choose at most ${MAX_CHOICES} cards at once.`;
  const out: Choice[] = [];
  for (const c of raw) {
    const o = (c ?? {}) as Record<string, unknown>;
    const str = (v: unknown, max: number) => (typeof v === "string" && v.trim() && v.trim().length <= max ? v.trim() : "");
    const group = str(o.group, 64);
    const work = str(o.work, 200);
    const provider = str(o.provider, 200);
    const max = typeof o.max_price_usdc === "number" ? o.max_price_usdc : NaN;
    const input = o.input && typeof o.input === "object" && !Array.isArray(o.input) ? (o.input as Record<string, unknown>) : null;
    if (!group || !work || !provider || !input || !Number.isFinite(max) || max <= 0 || max > MAX_CHOICE_USDC) {
      return "A chosen card is incomplete. Compare again and pick once more.";
    }
    out.push({ group, work, provider, input, max_price_usdc: max, strategy: isPick(o.strategy) ? o.strategy : undefined, label: str(o.label, 200) || provider });
  }
  return out;
}

/** A card's payment is named after its comparison and provider, so clicking the same card twice pays once. */
export function windowFor(c: Pick<Choice, "group" | "provider">): string {
  const provider = createHash("sha256").update(c.provider).digest("hex").slice(0, 10);
  return `card:${c.group.replace(/[^A-Za-z0-9]/g, "").slice(0, 24)}:${provider}`;
}
