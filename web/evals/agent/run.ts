// Agent eval runner: `pnpm eval:agent [scenario-id ...]`
//
// For each scenario (scenarios.ts) a simulated shopper chats with the REAL
// agent — the production system prompt, tool list, Gemini provider loop and
// runTool (search cap, step summaries) — with Algebra's API replaced by
// fixtures (fixtures.ts). A judge model then grades the transcript against
// the scenario's must / mustNot checklist.
//
// Needs GEMINI_API_KEY (read from web/.env.local). Env knobs:
//   EVAL_MODEL        agent model (default: first entry of GEMINI_MODELS)
//   EVAL_USER_MODEL   simulated shopper (default: gemini-flash-latest)
//   EVAL_JUDGE_MODEL  grader (default: EVAL_MODEL)
//   EVAL_CONCURRENCY  scenarios in parallel (default: 3)
// Writes a full report to evals/agent/results/ (gitignored).

import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { GoogleGenAI } from "@google/genai";
import { runTurn } from "../../lib/agent/providers/gemini";
import { buildSystemPrompt } from "../../lib/agent/system-prompt";
import type { AgentEvent } from "../../lib/agent/events";
import type { ToolExecutor } from "../../lib/agent/run-tool";
import type { CommerceProfile, Guardrails } from "../../lib/types";
import { makeExecutor } from "./fixtures";
import { DEFAULT_GUARDRAILS, SCENARIOS, type Scenario } from "./scenarios";

const WEB = join(__dirname, "..", "..");

function loadEnv() {
  const path = join(WEB, ".env.local");
  if (!existsSync(path)) return;
  for (const line of readFileSync(path, "utf8").split(/\r?\n/)) {
    const m = /^([A-Z0-9_]+)=(.*)$/.exec(line.trim());
    if (m && !process.env[m[1]]) process.env[m[1]] = m[2];
  }
}

type Check = { kind: "must" | "mustNot"; text: string; pass: boolean; evidence: string };

/** A model's daily request quota is used up — nothing will work until it resets. */
class DailyQuotaError extends Error {}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/**
 * Retries a call that hit a per-minute rate limit, waiting as long as the
 * API asks (capped). A daily quota can't be waited out in a run, so it
 * fails fast instead of burning the remaining scenarios on errors.
 */
async function withRetry<T>(fn: () => Promise<T>, attempts = 4): Promise<T> {
  for (let i = 1; ; i++) {
    try {
      return await fn();
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      if (!/429|RESOURCE_EXHAUSTED/.test(msg)) throw err;
      if (/per_day|PerDay/i.test(msg)) {
        const model = /model: ([\w.-]+)/.exec(msg)?.[1] ?? "this model";
        throw new DailyQuotaError(`daily request quota used up for ${model} — try again after it resets, or run with EVAL_MODEL set to another model`);
      }
      if (i >= attempts) throw err;
      const secs = Number(/retry in ([\d.]+)s/i.exec(msg)?.[1] ?? /"retryDelay":"(\d+)s"/.exec(msg)?.[1] ?? 30);
      await sleep(Math.min(secs + 1, 65) * 1000);
    }
  }
}

type Outcome = { scenario: Scenario; transcript: string; checks: Check[]; error?: string; seconds: number };

async function simulateUser(ai: GoogleGenAI, model: string, scenario: Scenario, transcript: string): Promise<string> {
  const res = await withRetry(() => ai.models.generateContent({
    model,
    contents: [{ role: "user", parts: [{ text: `Conversation so far:\n${transcript}\n\nWrite the shopper's next message.` }] }],
    config: {
      systemInstruction:
        `You are role-playing an Indian online shopper chatting with a shopping assistant app. Hidden facts and behaviour: ${scenario.persona}\n` +
        "Reply like a real person on a phone: short, informal, sometimes lowercase. Reveal facts only when asked or when natural. " +
        "If the assistant says a purchase is waiting for your approval in a card, reply that you approved it. " +
        "If you have what you came for (a clear recommendation, an order placed, or a clear honest answer) and your persona has nothing left to ask, reply exactly [DONE]. Output only the message.",
    },
  }));
  return (res.text ?? "").trim();
}

async function runConversation(ai: GoogleGenAI, models: { agent: string; user: string }, scenario: Scenario): Promise<string> {
  const guardrails: Guardrails = { ...DEFAULT_GUARDRAILS, ...scenario.guardrails };
  const profile: CommerceProfile = {
    user_id: "eval-user",
    default_shipping_alias: "Home",
    preferences: { shopping: { priority: "best_value", household: "couple" }, ...scenario.preferences },
  };
  const systemPrompt = buildSystemPrompt(profile, guardrails, scenario.mode, scenario.plugins ?? []);
  const { execute, log } = makeExecutor(scenario.mode, guardrails);

  const lines: string[] = [];
  const traced: ToolExecutor = async (name, input, identity) => {
    lines.push(`   [tool] ${name}(${JSON.stringify(input).slice(0, 220)})`);
    return execute(name, input, identity);
  };
  let asked = false;
  const emit = (e: AgentEvent) => {
    if (e.type === "step" && e.status !== "running") {
      lines.push(`      → ${e.summary ?? e.status}`);
      // The judge checks every stated price against these, so show them.
      for (const p of e.detail?.products ?? []) lines.push(`         · ${p.name} — ${p.price ?? "no price"} (${p.merchant})`);
    }
    if (e.type === "approval") lines.push("      → [approval card shown to the user]");
    if (e.type === "question") {
      asked = true;
      lines.push(`AGENT ASKS (tappable options): ${e.questions.map((q) => `${q.question} [${q.options.join(" | ")}]`).join("  ")}`);
    }
  };

  let history: unknown[] = [];
  let message = scenario.opener;
  const maxTurns = scenario.maxTurns ?? 3;
  for (let turn = 0; turn < maxTurns; turn++) {
    lines.push(`USER: ${message}`);
    // A fresh identity per turn, like the route handler: the search cap is per turn.
    const identity = { agentToken: "eval", mode: scenario.mode, defaultBudgetMinor: guardrails.max_per_purchase_minor_units };
    const result = await withRetry(() =>
      runTurn({
        apiKey: process.env.GEMINI_API_KEY!, model: models.agent, systemPrompt, history, userMessage: message, identity, emit, execute: traced,
      })
    );
    history = result.history;
    if (result.reply || !asked) lines.push(`AGENT: ${result.reply}`);
    asked = false;
    if (turn === maxTurns - 1) break;
    const next = await simulateUser(ai, models.user, scenario, lines.filter((l) => !l.startsWith("   ")).join("\n"));
    if (!next || next.includes("[DONE]")) break;
    message = next;
  }
  if (log.length) lines.push("", "BACKEND LOG:", ...log.map((l) => `  ${l}`));
  return lines.join("\n");
}

async function judge(ai: GoogleGenAI, model: string, scenario: Scenario, transcript: string): Promise<Check[]> {
  const checklist = [
    ...scenario.must.map((t, i) => `M${i + 1}. The agent DID: ${t}`),
    ...scenario.mustNot.map((t, i) => `N${i + 1}. The agent did NOT: ${t}`),
  ].join("\n");
  const res = await withRetry(() => ai.models.generateContent({
    model,
    contents: [
      {
        role: "user",
        parts: [
          {
            text:
              `Grade this shopping-agent conversation. Tool calls are shown as [tool] lines with results after →; the backend log shows what was actually created or executed.\n\n` +
              `TRANSCRIPT:\n${transcript}\n\nCHECKLIST (each line must be true for a pass):\n${checklist}\n\n` +
              `Return JSON: {"checks":[{"id":"M1","pass":true,"evidence":"short quote or reason"}, ...]} with one entry per checklist line, in order. Be strict but fair: judge what the agent actually said and did.`,
          },
        ],
      },
    ],
    config: { responseMimeType: "application/json" },
  }));
  const parsed = JSON.parse(res.text ?? "{}") as { checks?: { id: string; pass: boolean; evidence: string }[] };
  const byId = new Map((parsed.checks ?? []).map((c) => [c.id, c]));
  return [
    ...scenario.must.map((text, i) => ({ kind: "must" as const, text, pass: !!byId.get(`M${i + 1}`)?.pass, evidence: byId.get(`M${i + 1}`)?.evidence ?? "(not graded)" })),
    ...scenario.mustNot.map((text, i) => ({ kind: "mustNot" as const, text, pass: !!byId.get(`N${i + 1}`)?.pass, evidence: byId.get(`N${i + 1}`)?.evidence ?? "(not graded)" })),
  ];
}

async function pool<T, R>(items: T[], size: number, fn: (t: T) => Promise<R>): Promise<R[]> {
  const out: R[] = new Array(items.length);
  let next = 0;
  await Promise.all(
    Array.from({ length: Math.min(size, items.length) }, async () => {
      while (next < items.length) {
        const i = next++;
        out[i] = await fn(items[i]);
      }
    })
  );
  return out;
}

async function main() {
  loadEnv();
  const key = process.env.GEMINI_API_KEY;
  if (!key) throw new Error("GEMINI_API_KEY is not set (web/.env.local)");
  const agent = process.env.EVAL_MODEL ?? (process.env.GEMINI_MODELS ?? "gemini-flash-latest").split(",")[0].trim();
  const models = { agent, user: process.env.EVAL_USER_MODEL ?? "gemini-flash-latest" };
  const judgeModel = process.env.EVAL_JUDGE_MODEL ?? agent;
  const ai = new GoogleGenAI({ apiKey: key });

  const only = process.argv.slice(2);
  const chosen = SCENARIOS.filter((s) => !only.length || only.includes(s.id));
  console.log(`agent=${agent} shopper=${models.user} judge=${judgeModel} · ${chosen.length} scenarios`);

  let quotaGone = "";
  const outcomes = await pool(chosen, Number(process.env.EVAL_CONCURRENCY ?? 3), async (scenario): Promise<Outcome> => {
    const started = Date.now();
    if (quotaGone) return { scenario, transcript: "", checks: [], error: quotaGone, seconds: 0 };
    try {
      const transcript = await runConversation(ai, models, scenario);
      const checks = await judge(ai, judgeModel, scenario, transcript);
      const o = { scenario, transcript, checks, seconds: (Date.now() - started) / 1000 };
      const passed = checks.filter((c) => c.pass).length;
      console.log(`${passed === checks.length ? "PASS" : "FAIL"}  ${scenario.id.padEnd(20)} ${passed}/${checks.length}  (${o.seconds.toFixed(0)}s)`);
      return o;
    } catch (err) {
      const error = err instanceof Error ? err.message : String(err);
      if (err instanceof DailyQuotaError) quotaGone = error;
      console.log(`ERROR ${scenario.id.padEnd(20)} ${error.slice(0, 160)}`);
      return { scenario, transcript: "", checks: [], error, seconds: (Date.now() - started) / 1000 };
    }
  });

  const total = outcomes.reduce((n, o) => n + o.checks.length, 0);
  const passed = outcomes.reduce((n, o) => n + o.checks.filter((c) => c.pass).length, 0);
  const clean = outcomes.filter((o) => !o.error && o.checks.every((c) => c.pass)).length;
  console.log(`\n${clean}/${outcomes.length} scenarios fully passed · ${passed}/${total} checks`);

  const report = [
    `# Agent eval — ${new Date().toISOString()}`,
    ``,
    `agent \`${agent}\` · shopper \`${models.user}\` · judge \`${judgeModel}\``,
    ``,
    `**${clean}/${outcomes.length} scenarios fully passed · ${passed}/${total} checks**`,
    ``,
    ...outcomes.flatMap((o) => [
      `## ${o.checks.every((c) => c.pass) && !o.error ? "✅" : "❌"} ${o.scenario.id} (${o.scenario.mode})`,
      ``,
      ...(o.error ? [`Error: ${o.error}`, ``] : []),
      ...o.checks.map((c) => `- ${c.pass ? "✅" : "❌"} ${c.kind === "must" ? "DID" : "did NOT"}: ${c.text} — _${c.evidence}_`),
      ``,
      "```",
      o.transcript,
      "```",
      ``,
    ]),
  ].join("\n");
  const dir = join(WEB, "evals", "agent", "results");
  mkdirSync(dir, { recursive: true });
  const file = join(dir, `${new Date().toISOString().replace(/[:.]/g, "-")}.md`);
  writeFileSync(file, report);
  console.log(`report: ${file}`);
  process.exitCode = clean === outcomes.length ? 0 : 1;
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
