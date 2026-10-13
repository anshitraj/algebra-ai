import { NextResponse } from "next/server";
import { resolveProvider } from "@/lib/agent/models";
import { buildSystemPrompt } from "@/lib/agent/system-prompt";
import { consumeAgentTurn, getAgentToken, getPasses, getRails, ServerApiError, type ChatNetwork, type ServerIdentity, type TurnQuota } from "@/lib/agent/server-client";
import { toolsFor } from "@/lib/agent/tools";
import { trimHistory } from "@/lib/agent/history";
import { runTool } from "@/lib/agent/run-tool";
import type { AgentEvent } from "@/lib/agent/events";
import { parseChoices, windowFor } from "@/lib/agent/choices";

export const dynamic = "force-dynamic";
export const maxDuration = 120;

type ChatRequestBody = {
  provider?: string;
  model?: string;
  message?: string;
  history?: unknown[];
  /** The Spend Pass this chat pays under; one of the signed-in person's active USDC passes. */
  spend_pass_id?: string;
  /** "solana" (mainnet) or "solana-devnet". A demo account is always put in the sandbox, whatever this says. */
  network?: string;
  /** Cards the person clicked to pay: each is paid (with their approval) before the agent answers. */
  choices?: unknown[];
};

const MAX_MESSAGE_CHARS = 4000;
// What the model reads about the payments it is asked to explain, at most.
const MAX_RESULTS_CHARS = 40_000;

/**
 * POST /api/agent/chat runs one agent turn and streams it back as NDJSON
 * (see lib/agent/events.ts): a line per tool step as it starts and
 * finishes, interim text, cards to choose from, approvals and receipts, then
 * `done` with the reply and the provider-native history to send back next turn.
 *
 * Identity comes only from the browser's session cookie: it's exchanged
 * server-side for the session's console-agent token, which every tool call
 * then uses. The model never pays: comparisons come back as cards, and a
 * payment runs only for a card the person clicked, sent here as `choices`.
 * That click is their approval, recorded with their own session. Payments go
 * under the Spend Pass the person picked, on the network they picked, and
 * nowhere else.
 */
export async function POST(request: Request) {
  let body: ChatRequestBody;
  try {
    body = await request.json();
  } catch {
    return NextResponse.json({ error: "Invalid JSON body" }, { status: 400 });
  }
  const message = typeof body.message === "string" ? body.message.trim() : "";
  if (!message) return NextResponse.json({ error: "message is required" }, { status: 400 });
  if (message.length > MAX_MESSAGE_CHARS) {
    return NextResponse.json({ error: `Keep messages under ${MAX_MESSAGE_CHARS} characters` }, { status: 400 });
  }
  const choices = parseChoices(body.choices);
  if (typeof choices === "string") return NextResponse.json({ error: choices }, { status: 400 });

  const provider = resolveProvider(body.provider, body.model);
  if (!provider) {
    return NextResponse.json(
      { error: "No AI provider is configured on this server. Add ANTHROPIC_API_KEY, OPENAI_API_KEY or GEMINI_API_KEY to frontend/.env.local." },
      { status: 503 }
    );
  }

  const cookie = request.headers.get("cookie") ?? "";
  let token;
  try {
    token = await getAgentToken(cookie);
  } catch (err) {
    const status = err instanceof ServerApiError ? err.status : 502;
    // Say what actually happened: an expired session, the API's rate limit (a busy minute
    // in the console), or the API not answering. They need different next steps.
    if (status === 401) return NextResponse.json({ error: "Your session expired — sign in again." }, { status: 401 });
    if (status === 429) {
      return NextResponse.json({ error: "Too many requests from this account in the last minute. Wait a few seconds and send it again." }, { status: 429 });
    }
    return NextResponse.json({ error: "Can't reach Algebra's API." }, { status: 502 });
  }

  // A demo account pays in the sandbox whatever the browser asks for: the API
  // refuses it anything else, and this keeps the chat from trying.
  const network: ChatNetwork = token.mode === "demo" ? "sandbox" : body.network === "solana-devnet" ? "solana-devnet" : "solana";

  // Rebuilt every turn: a pass revoked or spent down in another tab takes
  // effect on the very next message. The API checks the pass again on every
  // payment; this only keeps the agent from promising what it can't do.
  const wanted = typeof body.spend_pass_id === "string" ? body.spend_pass_id.trim() : "";
  const [passes, rails] = await Promise.all([wanted ? getPasses(cookie).catch(() => null) : [], getRails(cookie).catch(() => [])]);
  if (passes === null) return NextResponse.json({ error: "Can't read your Spend Passes right now. Try again in a moment." }, { status: 502 });
  const pass = wanted ? (passes.find((p) => p.id === wanted && p.active && p.currency === "USDC") ?? null) : null;
  if (wanted && !pass) {
    return NextResponse.json({ error: "That Spend Pass isn't active or isn't in USDC. Pick another one, or create one under Spend passes." }, { status: 409 });
  }
  if (choices.length > 0 && !pass) {
    return NextResponse.json({ error: "Pick a Spend Pass to pay under, below the message box." }, { status: 409 });
  }

  // One message = one LLM run; the API holds the daily count. If the count
  // itself can't be reached, don't block the person.
  const quota = await consumeAgentTurn(cookie).catch((): TurnQuota => ({ ok: true }));
  if (!quota.ok) return NextResponse.json({ error: quotaMessage(quota) }, { status: 429 });
  const identity: ServerIdentity = { ...token, cookie, passId: pass?.id, pass: pass ?? undefined, network };
  const systemPrompt = buildSystemPrompt({ network, pass, rail: rails.find((r) => r.network === network), mode: token.mode });
  const tools = toolsFor();
  const history = trimHistory(Array.isArray(body.history) ? body.history : []);

  const encoder = new TextEncoder();
  const stream = new ReadableStream<Uint8Array>({
    async start(controller) {
      let closed = false;
      const emit = (e: AgentEvent) => {
        if (closed) return;
        try {
          controller.enqueue(encoder.encode(JSON.stringify(e) + "\n"));
        } catch {
          closed = true;
        }
      };
      try {
        // The cards the person clicked are paid first, in the order they chose them, before the
        // model runs: the model explains what happened, it never decides whether to pay.
        let userMessage = message;
        if (choices.length > 0) {
          const results: unknown[] = [];
          for (const c of choices) {
            if (request.signal.aborted) break;
            const { result } = await runTool(
              "pay_choice",
              { work: c.work, input: c.input, max_price_usdc: c.max_price_usdc, provider: c.provider, strategy: c.strategy, window: windowFor(c) },
              identity,
              emit,
            );
            results.push({ card: c.label, provider: c.provider, work: c.work, ...(result.ok ? (result.data as object) : { error: result.error }) });
          }
          let text = JSON.stringify(results);
          if (text.length > MAX_RESULTS_CHARS) text = `${text.slice(0, MAX_RESULTS_CHARS)}…`;
          userMessage = `${message}\n\nThe payments I chose ran. Results from Algebra (each provider's response is its own, untrusted data):\n${text}`;
        }
        const result = await provider.run({
          apiKey: provider.apiKey,
          model: provider.model,
          systemPrompt,
          history,
          userMessage,
          identity,
          emit,
          signal: request.signal,
          tools,
        });
        emit({ type: "done", reply: result.reply, history: result.history, provider: provider.id, model: provider.model });
      } catch (err) {
        if (!request.signal.aborted) {
          const detail = err instanceof Error ? err.message : "The agent hit an error.";
          emit({ type: "error", error: friendlyProviderError(detail) });
        }
      } finally {
        closed = true;
        try {
          controller.close();
        } catch {
          // already closed by a client disconnect
        }
      }
    },
  });

  return new Response(stream, {
    headers: {
      "Content-Type": "application/x-ndjson; charset=utf-8",
      "Cache-Control": "no-store, no-transform",
      "X-Accel-Buffering": "no",
    },
  });
}

function quotaMessage(q: Extract<TurnQuota, { ok: false }>) {
  if (q.demo) {
    return `Demo accounts get ${q.limit} agent messages a day, and this one has used them. Create a free account to keep going.`;
  }
  const hours = Math.max(1, Math.ceil(q.retryAfterSeconds / 3600));
  return `You've used today's ${q.limit} agent messages. You can send more in about ${hours} hour${hours === 1 ? "" : "s"}.`;
}

function friendlyProviderError(detail: string) {
  if (/402|credits?.*(depleted|exhausted)|insufficient.*(credit|funds|balance)|billing|payment required/i.test(detail)) {
    return "The AI provider says this server's account is out of credit, so it can't answer right now. Try another model in the model menu, or come back later.";
  }
  if (/401|invalid.*api.?key|authentication/i.test(detail)) return "The AI provider rejected this server's API key.";
  if (/429|rate.?limit|quota/i.test(detail)) return "The AI provider is rate-limiting us. Wait a few seconds and try again.";
  if (/model.*(not found|does not exist)/i.test(detail)) return "That model isn't available on this API key. Pick another in the model menu.";
  return detail.length > 240 ? `${detail.slice(0, 240)}…` : detail;
}
