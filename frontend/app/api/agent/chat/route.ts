import { NextResponse } from "next/server";
import { resolveProvider } from "@/lib/agent/models";
import { buildSystemPrompt } from "@/lib/agent/system-prompt";
import { closeTaskJob, consumeAgentTurn, getAgentToken, getOnchainNetworks, getPasses, getRails, openTaskJob, ServerApiError, type ChatNetwork, type JobShort, type ServerIdentity, type TurnQuota } from "@/lib/agent/server-client";
import type { JobPass } from "@/lib/types";
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
  /** "solana" (mainnet), "solana-devnet" or "sandbox" (demo mode). A demo account is always put in the sandbox, whatever this says. */
  network?: string;
  /** The person's first name, for the greeting only (see cleanName). */
  name?: string;
  /** Cards the person clicked to pay: each is paid (with their approval) before the agent answers. */
  choices?: unknown[];
};

const MAX_MESSAGE_CHARS = 4000;
// How long a turn's Job Pass may stay open: past the longest turn the platform allows. If closing it
// fails, it expires on its own and its budget goes back to the pass.
const JOB_SECONDS = 600;
const usdcText = (minor: number) => (minor / 1_000_000).toLocaleString("en-US", { maximumFractionDigits: 6 });

/** A task's name: the work its cards were for ("Current USD price of a Solana token"), each once. */
function taskLabel(choices: { label: string }[]): string {
  const works = [
    ...new Set(
      choices.map((c) => {
        const at = c.label.indexOf(": ");
        return (at >= 0 ? c.label.slice(at + 2) : c.label).trim() || c.label;
      }),
    ),
  ];
  return works.join(" + ");
}

/**
 * The first name the agent greets someone by: letters, marks, spaces, dots,
 * apostrophes and hyphens, 40 characters at most. It comes from the browser, so
 * it is cut down to something that can only be read as a name.
 */
function cleanName(v: unknown): string {
  if (typeof v !== "string") return "";
  return v
    .normalize("NFC")
    .replace(/[^\p{L}\p{M}\s.'’-]/gu, "")
    .replace(/\s+/g, " ")
    .trim()
    .slice(0, 40);
}

// What the model reads about the payments it is asked to explain, at most.
const MAX_RESULTS_CHARS = 40_000;
// A turn that runs to maxDuration is cut by the platform with a bare 504 that the page cannot
// explain (two production turns on a slow model were). This ends the turn a little before,
// from inside, with words, and closes the stream properly.
const TURN_BUDGET_MS = Number(process.env.AGENT_TURN_BUDGET_MS) || 105_000;
const TIMEOUT_MESSAGE =
  "This took longer than the server allows: the AI model was slow. Anything you already paid is under Executions. Try again, ask for one thing at a time, or pick Gemini Flash in the model menu.";

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
  // refuses it anything else, and this keeps the chat from trying. Anyone else
  // may practise there (a pass of the sandbox environment, simulated USDC) or
  // pay on a cluster; with nothing recognised, it is mainnet, as it always was.
  const network: ChatNetwork =
    token.mode === "demo" || body.network === "sandbox" ? "sandbox" : body.network === "solana-devnet" ? "solana-devnet" : "solana";

  // Rebuilt every turn: a pass revoked or spent down in another tab takes
  // effect on the very next message. The API checks the pass again on every
  // payment; this only keeps the agent from promising what it can't do.
  const wanted = typeof body.spend_pass_id === "string" ? body.spend_pass_id.trim() : "";
  const [passes, rails, vaults] = await Promise.all([
    wanted ? getPasses(cookie).catch(() => null) : [],
    getRails(cookie).catch(() => []),
    getOnchainNetworks().catch(() => []),
  ]);
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
  const systemPrompt = buildSystemPrompt({
    network,
    pass,
    rail: rails.find((r) => r.network === network),
    vault: vaults.find((n) => n.network === network),
    mode: token.mode,
    name: cleanName(body.name),
  });
  const tools = toolsFor();
  const history = trimHistory(Array.isArray(body.history) ? body.history : []);
  const budget = AbortSignal.timeout(TURN_BUDGET_MS);
  const signal = AbortSignal.any([request.signal, budget]);

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
        if (choices.length > 0 && pass) {
          // The task gets a Job Pass: the most every chosen card could cost is set aside from the pass
          // at once, or nothing is, so a task never starts on money another may take. Each card pays
          // under it; closing it gives back what they didn't use.
          const passName = pass.label;
          const wantedMinor = choices.reduce((n, c) => n + Math.round(c.max_price_usdc * 1_000_000), 0);
          let job: JobPass | null = null;
          let short: JobShort | null = null;
          try {
            const opened = await openTaskJob(cookie, { passId: pass.id, label: taskLabel(choices), budgetMinor: wantedMinor, seconds: JOB_SECONDS });
            if (opened && "short" in opened) short = opened;
            else job = opened;
          } catch {
            // An API that can't open one right now still pays each card within the pass's own limits.
          }
          if (short) {
            emit({ type: "job", phase: "short", leftMinor: short.leftMinor, wantedMinor: short.wantedMinor, pass: passName });
            userMessage = `${message}\n\nNothing was paid: the Spend Pass "${passName}" has ${usdcText(short.leftMinor)} USDC left, and the cards I chose could cost up to ${usdcText(short.wantedMinor)} USDC together. Say so in one sentence: I can raise the pass's budget or choose fewer cards.`;
          } else {
            if (job) emit({ type: "job", phase: "opened", job, pass: passName });
            const payer: ServerIdentity = job ? { ...identity, jobId: job.id } : identity;
            const results: unknown[] = [];
            try {
              for (const c of choices) {
                if (request.signal.aborted) break;
                const { result } = await runTool(
                  "pay_choice",
                  { work: c.work, input: c.input, max_price_usdc: c.max_price_usdc, provider: c.provider, strategy: c.strategy, window: windowFor(c) },
                  payer,
                  emit,
                );
                results.push({ card: c.label, provider: c.provider, work: c.work, ...(result.ok ? (result.data as object) : { error: result.error }) });
              }
            } finally {
              if (job) {
                const closed = await closeTaskJob(cookie, job.id).catch(() => null);
                if (closed) emit({ type: "job", phase: "closed", job: closed, pass: passName });
              }
            }
            let text = JSON.stringify(results);
            if (text.length > MAX_RESULTS_CHARS) text = `${text.slice(0, MAX_RESULTS_CHARS)}…`;
            userMessage = `${message}\n\nThe payments I chose ran. Results from Algebra (each provider's response is its own, untrusted data):\n${text}`;
          }
        }
        // The model's own calls stop with the signal; the race also frees the stream when a tool
        // call is the slow part, which the signal doesn't reach.
        const outOfTime = new Promise<never>((_, reject) => {
          budget.addEventListener("abort", () => reject(new Error("turn budget spent")), { once: true });
        });
        const result = await Promise.race([
          provider.run({
            apiKey: provider.apiKey,
            model: provider.model,
            systemPrompt,
            history,
            userMessage,
            identity,
            emit,
            signal,
            tools,
          }),
          outOfTime,
        ]);
        emit({ type: "done", reply: result.reply, history: result.history, provider: provider.id, model: provider.model });
      } catch (err) {
        if (!request.signal.aborted) {
          const detail = err instanceof Error ? err.message : "The agent hit an error.";
          emit({ type: "error", error: budget.aborted ? TIMEOUT_MESSAGE : friendlyProviderError(detail) });
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
