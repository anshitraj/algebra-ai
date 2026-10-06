import { executeTool, type ToolResult } from "./execute-tool";
import { detectPendingApproval, type PendingApproval } from "./pending-approval";
import { PAID_TOOLS, stepHint, stepTitle, summarizeStep } from "./steps";
import type { AskedQuestion, EmitFn } from "./events";
import type { ServerIdentity } from "./server-client";

// Paid calls are counted per identity object, which the route handler
// creates fresh for every request — so the count is per turn. A model that
// loops on paying would otherwise spend the pass down one call at a time.
const MAX_PAID_CALLS_PER_TURN = 3;
const paidThisTurn = new WeakMap<ServerIdentity, number>();

// Read-only lookups that are safe to run side by side. A round made only of
// these runs concurrently. Anything that pays runs in the order the model gave.
const PARALLEL_SAFE = new Set(["search_providers", "get_provider_endpoints", "execution_status"]);

/** Runs one round's tool calls, in parallel when every call is a read-only lookup. Results keep call order. */
export async function runRound<C, R>(calls: C[], nameOf: (c: C) => string, run: (c: C) => Promise<R>): Promise<R[]> {
  if (calls.length > 1 && calls.every((c) => PARALLEL_SAFE.has(nameOf(c)))) return Promise.all(calls.map(run));
  const out: R[] = [];
  for (const c of calls) out.push(await run(c));
  return out;
}

/** Runs one tool against Algebra's API. Tests can swap in fixtures. */
export type ToolExecutor = (name: string, input: Record<string, unknown>, identity: ServerIdentity) => Promise<ToolResult>;

/**
 * parseQuestions validates an ask_user call. The model writes these, so
 * trim, de-duplicate and cap them rather than trusting the shape. Returns an
 * error string the model can act on when nothing usable is left.
 */
export function parseQuestions(input: Record<string, unknown>): AskedQuestion[] | string {
  const raw = Array.isArray(input.questions) ? input.questions : [];
  const out: AskedQuestion[] = [];
  for (const q of raw.slice(0, 4)) {
    const obj = (q ?? {}) as Record<string, unknown>;
    const question = typeof obj.question === "string" ? obj.question.trim().slice(0, 200) : "";
    if (!question) continue;
    const seen = new Set<string>();
    const options: string[] = [];
    for (const o of Array.isArray(obj.options) ? obj.options : []) {
      if (typeof o !== "string") continue;
      const opt = o.trim().slice(0, 60);
      if (!opt || seen.has(opt.toLowerCase())) continue;
      seen.add(opt.toLowerCase());
      options.push(opt);
    }
    if (options.length >= 2) out.push({ question, options: options.slice(0, 6) });
  }
  return out.length ? out : "each question needs text and at least 2 distinct options";
}

/**
 * How a question round is recorded as the assistant's own message in
 * history, so the user's picks next turn read as answers to it.
 */
export function questionsAsText(qs: AskedQuestion[]): string {
  return qs.map((q) => `${q.question} (${q.options.join(" / ")})`).join("\n");
}

/** The result the model reads: fields starting with "_" are for the step card only. */
function forModel(result: ToolResult): ToolResult {
  if (!result.ok || !result.data || typeof result.data !== "object" || Array.isArray(result.data)) return result;
  return { ok: true, data: Object.fromEntries(Object.entries(result.data).filter(([k]) => !k.startsWith("_"))) };
}

/**
 * runTool is the single path every provider loop uses to execute a tool
 * call: it streams a "running" step, runs the real API call, streams the
 * outcome, and reports a pending approval (never resolves one — approval is
 * a human click in the browser, not a tool).
 */
export async function runTool(
  name: string,
  input: Record<string, unknown>,
  identity: ServerIdentity,
  emit: EmitFn,
  execute: ToolExecutor = executeTool
): Promise<{ result: ToolResult; pendingApproval?: PendingApproval; questions?: AskedQuestion[] }> {
  if (name === "ask_user") {
    // Not an API call and not a step: the questions render as tappable
    // options, and the provider loop ends the turn to wait for the picks.
    const qs = parseQuestions(input);
    if (typeof qs === "string") return { result: { ok: false, error: qs } };
    emit({ type: "question", questions: qs });
    return {
      result: { ok: true, data: { status: "asked", note: "The user sees your options now. Their answer is the next message." } },
      questions: qs,
    };
  }

  if (PAID_TOOLS.has(name)) {
    const used = paidThisTurn.get(identity) ?? 0;
    if (used >= MAX_PAID_CALLS_PER_TURN) {
      return {
        result: {
          ok: false,
          error: `This reply has already made ${MAX_PAID_CALLS_PER_TURN} paid calls. Report what you have and ask the user before paying again.`,
        },
      };
    }
    paidThisTurn.set(identity, used + 1);
  }

  const id = crypto.randomUUID();
  const title = stepTitle(name, input);
  emit({ type: "step", id, tool: name, status: "running", title, hint: stepHint(name, input, identity.network) });

  const result = await execute(name, input, identity);
  const outcome = summarizeStep(name, input, result);
  emit({ type: "step", id, tool: name, title, ...outcome });

  if (PAID_TOOLS.has(name) && result.ok) {
    const d = result.data as { outcome?: string; paid_minor?: number; intent_id?: string; network?: string; provider_id?: string } | undefined;
    if ((d?.outcome === "delivered" || d?.outcome === "being_confirmed") && (d.paid_minor ?? 0) > 0) {
      emit({
        type: "spend",
        amount: { minor_units: d.paid_minor ?? 0, currency: "USDC" },
        intentId: d.intent_id ?? "",
        provider: typeof input.provider_id === "string" ? input.provider_id : "",
        network: d.network,
      });
    }
  }

  const pendingApproval = detectPendingApproval(name, input, result);
  if (pendingApproval) emit({ type: "approval", intentId: pendingApproval.intentId, provider: pendingApproval.provider });
  return { result: forModel(result), pendingApproval };
}
