// The provider-native history the browser sends back each turn. It carries
// every tool result, so a long chat grows without bound — and every byte is
// re-sent to the model (and billed) on every message.

/** Longest history, serialized, the route will send to the model. */
export const MAX_HISTORY_CHARS = 250_000;

/** True for the message that starts a turn: the user's own words, not a tool result. */
function startsTurn(m: unknown): boolean {
  const msg = m as { role?: string; content?: unknown; parts?: { text?: unknown; functionResponse?: unknown }[] } | null;
  if (msg?.role !== "user") return false;
  if (typeof msg.content === "string") return true; // Anthropic / OpenAI
  if (Array.isArray(msg.parts)) return !msg.parts.some((p) => p?.functionResponse) && msg.parts.some((p) => typeof p?.text === "string"); // Gemini
  if (Array.isArray(msg.content)) return (msg.content as { type?: string }[]).every((c) => c?.type === "text"); // Anthropic text blocks
  return false;
}

/**
 * trimHistory drops the oldest whole turns until the history fits. It only
 * ever cuts at the start of a user turn: every provider rejects a tool
 * result whose call was cut off. One turn too big to keep at all means a
 * fresh start — the chat still works, it just forgets.
 */
export function trimHistory(history: unknown[], maxChars = MAX_HISTORY_CHARS): unknown[] {
  const sizes = history.map((m) => JSON.stringify(m ?? null).length + 1);
  let total = sizes.reduce((a, b) => a + b, 2);
  let start = 0;
  while (total > maxChars) {
    let next = start + 1;
    while (next < history.length && !startsTurn(history[next])) next++;
    if (next >= history.length) return [];
    for (let i = start; i < next; i++) total -= sizes[i];
    start = next;
  }
  return start === 0 ? history : history.slice(start);
}
