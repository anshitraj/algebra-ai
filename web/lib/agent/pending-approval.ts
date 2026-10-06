// Shared by all three provider loops: after a paid call, check whether it is
// waiting for the person so the route handler can surface it to the UI.
// Approval itself never goes through a tool — the browser approves or
// cancels the intent with the person's own session.

export type PendingApproval = { intentId: string; provider?: string };

export function detectPendingApproval(
  toolName: string,
  _toolInput: Record<string, unknown>,
  result: { ok: boolean; data?: unknown }
): PendingApproval | undefined {
  if ((toolName !== "pay_and_call" && toolName !== "run_approved_intent") || !result.ok) return undefined;
  const d = result.data as { outcome?: string; intent_id?: unknown; provider_id?: unknown } | undefined;
  if (d?.outcome !== "approval_required" || typeof d.intent_id !== "string" || !d.intent_id) return undefined;
  return { intentId: d.intent_id, provider: typeof d.provider_id === "string" ? d.provider_id : undefined };
}
