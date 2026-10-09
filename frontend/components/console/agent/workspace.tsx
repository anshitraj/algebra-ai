"use client";

import Link from "next/link";
import { Logo } from "@/components/logo";
import { useCallback, useEffect, useRef, useState } from "react";
import { useSearchParams } from "next/navigation";
import { AnimatePresence, motion } from "motion/react";
import type { AgentEvent, AskedQuestion } from "@/lib/agent/events";
import { firstName, useSession } from "@/lib/session";
import * as api from "@/lib/api-client";
import type { SpendPass } from "@/lib/types";
import { formatUSDC } from "@/lib/money";
import { networkLabel, useNetwork } from "@/lib/network";
import { IconArrowUp, IconRefresh, IconShield, IconStop } from "@/components/icons";
import { useConsoleData } from "../console-data";
import { ApprovalCard, type ApprovalOutcome } from "./approval-card";
import { ModelPicker, type ModelChoice, type ProviderEntry } from "./model-picker";
import { answerText, QuestionCard } from "./question-card";
import { ThinkingLine } from "./live-activity";
import { RichText } from "./rich-text";
import { PassPanel, RecentCallsPanel } from "./side-panels";
import { Timeline, type Step } from "./timeline";

type Turn = {
  id: string;
  user: string;
  steps: Step[];
  notes: string[];
  approvals: { intentId: string; provider?: string; resolved?: ApprovalOutcome }[];
  /** Clarifying questions the agent asked (ask_user), and the picks sent back. */
  questions?: AskedQuestion[];
  answered?: string[];
  reply?: string;
  error?: string;
  status: "running" | "done" | "error" | "stopped";
};

const SUGGESTIONS: Record<string, string[]> = {
  solana: [
    "Who are the top holders of BONK? Pay at most 0.05 USDC",
    "Check whether a Solana token looks risky before I buy it",
    "Find an API that reads text from an image, and what it costs",
    "Search the web for today's Solana news, under 0.02 USDC",
  ],
  "solana-devnet": [
    "Which paid APIs take devnet USDC?",
    "Make a test call on devnet for under 0.01 USDC",
    "Find a devnet API for token prices and call it",
    "Show me what a paid call returns, with its receipt",
  ],
};

// Catalog searches answer from cache in milliseconds. Their discovery panel
// still plays one pass through the catalogs, so the person sees where Algebra
// looked, before the results replace it.
const MIN_VISIBLE_MS: Record<string, number> = { search_providers: 3000, get_provider_endpoints: 1400 };

const MODEL_KEY = "algebra:agent-model";
const PASS_KEY = "algebra:agent-pass";
// v2: the Solana chat. Chats saved by the earlier shopping agent aren't restored.
const CHAT_KEY = "algebra:agent-chat:v2";
// Provider history carries every tool result, so a long chat can get big.
// Keep the newest turns that fit; the conversation matters more than its tail.
const CHAT_MAX_BYTES = 400_000;
const EASE = [0.16, 1, 0.3, 1] as const;

type SessionTotals = { spent: number; calls: number; steps: number };
type SavedChat = { turns: Turn[]; history: unknown[]; lockedProvider: string | null; session: SessionTotals };

/** Restores the chat a reload would otherwise throw away. */
function loadChat(): SavedChat | null {
  try {
    const raw = window.localStorage.getItem(CHAT_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as SavedChat;
    if (!Array.isArray(parsed.turns) || parsed.turns.length === 0) return null;
    // A turn interrupted by the reload is no longer running.
    parsed.turns = parsed.turns.map((t) =>
      t.status === "running"
        ? { ...t, status: "stopped", steps: t.steps.map((s) => (s.status === "running" ? { ...s, status: "error", summary: "Interrupted by a page reload" } : s)) }
        : t
    );
    return parsed;
  } catch {
    return null; // corrupt or blocked storage — start fresh rather than break the page
  }
}

function saveChat(chat: SavedChat) {
  try {
    let payload = JSON.stringify(chat);
    let turns = chat.turns;
    // Drop the oldest turns until it fits, rather than losing the chat.
    while (payload.length > CHAT_MAX_BYTES && turns.length > 1) {
      turns = turns.slice(1);
      payload = JSON.stringify({ ...chat, turns, history: chat.history });
    }
    if (payload.length > CHAT_MAX_BYTES) {
      window.localStorage.removeItem(CHAT_KEY);
      return;
    }
    window.localStorage.setItem(CHAT_KEY, payload);
  } catch {
    // private mode or quota — the chat still works for this page load
  }
}

function uid() {
  return typeof crypto !== "undefined" && "randomUUID" in crypto ? crypto.randomUUID() : String(Math.random());
}

export function AgentWorkspace() {
  const { user } = useSession();
  const { refresh: refreshConsole } = useConsoleData();
  const { network, rails, refreshRails } = useNetwork();
  const params = useSearchParams();

  const [providers, setProviders] = useState<ProviderEntry[] | null>(null);
  const [choice, setChoice] = useState<ModelChoice>(null);
  const [lockedProvider, setLockedProvider] = useState<string | null>(null);
  const [turns, setTurns] = useState<Turn[]>([]);
  const [history, setHistory] = useState<unknown[]>([]);
  const [restored, setRestored] = useState(false);
  const [input, setInput] = useState(() => params.get("prompt") ?? "");
  const [running, setRunning] = useState(false);
  const [session, setSession] = useState<SessionTotals>({ spent: 0, calls: 0, steps: 0 });
  const [callsKey, setCallsKey] = useState(0);
  const [inspector, setInspector] = useState<"pass" | "activity">("pass");
  const [passes, setPasses] = useState<SpendPass[] | null>(null);
  const [passId, setPassId] = useState("");

  const abortRef = useRef<AbortController | null>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  const stickRef = useRef(true);
  const inputRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    fetch("/api/agent/providers")
      .then((r) => r.json())
      .then((d: { providers: ProviderEntry[] }) => {
        setProviders(d.providers);
        try {
          const saved = JSON.parse(window.localStorage.getItem(MODEL_KEY) ?? "null") as ModelChoice;
          const p = saved && d.providers.find((x) => x.id === saved.provider && x.available);
          if (p && p.models.some((m) => m.id === saved!.model)) setChoice(saved);
        } catch {
          // no saved choice — Auto
        }
      })
      .catch(() => setProviders([]));
  }, []);

  // The passes this chat can pay under: the person's active USDC passes.
  // Read again after every paid call, so the panel shows what's left.
  useEffect(() => {
    api
      .listPasses()
      .then((r) => {
        const usable = (r.passes ?? []).filter((p) => p.active && p.currency === "USDC");
        setPasses(usable);
        setPassId((cur) => {
          if (cur && usable.some((p) => p.id === cur)) return cur;
          let saved = "";
          try {
            saved = window.localStorage.getItem(PASS_KEY) ?? "";
          } catch {
            // storage blocked: the first pass
          }
          if (saved === "none") return "";
          return usable.find((p) => p.id === saved)?.id ?? usable[0]?.id ?? "";
        });
      })
      .catch(() => setPasses([]));
  }, [callsKey]);

  // A payment moves the wallet's balance.
  useEffect(() => {
    if (callsKey > 0) refreshRails();
  }, [callsKey, refreshRails]);

  useEffect(() => {
    // localStorage is unavailable during SSR, so restore after mount.
    const saved = loadChat();
    if (saved) {
      setTurns(saved.turns);
      setHistory(saved.history ?? []);
      setLockedProvider(saved.lockedProvider ?? null);
      if (saved.session) setSession({ spent: saved.session.spent ?? 0, calls: saved.session.calls ?? 0, steps: saved.session.steps ?? 0 });
    }
    setRestored(true);
    inputRef.current?.focus();
  }, []);

  // Persist after every change, so a reload mid-chat loses nothing.
  useEffect(() => {
    if (!restored) return;
    if (turns.length === 0) {
      try {
        window.localStorage.removeItem(CHAT_KEY);
      } catch {
        // nothing to clean up if storage was never writable
      }
      return;
    }
    saveChat({ turns, history, lockedProvider, session });
  }, [restored, turns, history, lockedProvider, session]);

  // Keep the newest content in view unless the user scrolled up to read.
  useEffect(() => {
    const el = scrollRef.current;
    if (el && stickRef.current) el.scrollTo({ top: el.scrollHeight, behavior: "smooth" });
  }, [turns]);

  const anyProvider = providers?.some((p) => p.available) ?? false;

  const updateTurn = useCallback((id: string, fn: (t: Turn) => Turn) => {
    setTurns((ts) => ts.map((t) => (t.id === id ? fn(t) : t)));
  }, []);

  const send = useCallback(
    async (raw: string) => {
      const text = raw.trim();
      if (!text || running) return;
      const id = uid();
      setTurns((ts) => [...ts, { id, user: text, steps: [], notes: [], approvals: [], status: "running" }]);
      setInput("");
      setRunning(true);
      stickRef.current = true;
      const ctrl = new AbortController();
      abortRef.current = ctrl;
      let finished = false;
      const started = new Map<string, number>();

      const handle = (e: AgentEvent) => {
        switch (e.type) {
          case "step":
            if (e.status === "running") {
              started.set(e.id, Date.now());
              updateTurn(id, (t) => ({
                ...t,
                steps: [...t.steps, { id: e.id, tool: e.tool, title: e.title, hint: e.hint, status: "running", startedAt: Date.now() }],
              }));
              setSession((s) => ({ ...s, steps: s.steps + 1 }));
            } else {
              // The step's own time, however long its animation is shown.
              const done = { status: e.status, summary: e.summary, detail: e.detail, endedAt: Date.now() };
              const apply = () => updateTurn(id, (t) => ({ ...t, steps: t.steps.map((s) => (s.id === e.id ? { ...s, ...done } : s)) }));
              // A result faster than one pass of its animation waits for the pass to end.
              const wait = (MIN_VISIBLE_MS[e.tool] ?? 0) - (Date.now() - (started.get(e.id) ?? 0));
              if (wait > 0) window.setTimeout(apply, wait);
              else apply();
            }
            break;
          case "text":
            updateTurn(id, (t) => ({ ...t, notes: [...t.notes, e.text] }));
            break;
          case "question":
            updateTurn(id, (t) => ({ ...t, questions: e.questions }));
            break;
          case "approval":
            updateTurn(id, (t) =>
              t.approvals.some((a) => a.intentId === e.intentId)
                ? t
                : { ...t, approvals: [...t.approvals, { intentId: e.intentId, provider: e.provider }] }
            );
            refreshConsole();
            break;
          case "spend":
            setSession((s) => ({ ...s, spent: s.spent + e.amount.minor_units, calls: s.calls + 1 }));
            setCallsKey((k) => k + 1);
            refreshConsole();
            break;
          case "done":
            finished = true;
            setHistory(e.history);
            setLockedProvider(e.provider);
            updateTurn(id, (t) => ({ ...t, reply: e.reply, status: "done" }));
            break;
          case "error":
            finished = true;
            updateTurn(id, (t) => ({ ...t, error: e.error, status: "error" }));
            break;
        }
      };

      try {
        const res = await fetch("/api/agent/chat", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            provider: choice?.provider,
            model: choice?.model,
            message: text,
            history,
            spend_pass_id: passId || undefined,
            network,
          }),
          signal: ctrl.signal,
        });
        if (!res.ok || !res.body) {
          const body = await res.json().catch(() => null);
          throw new Error(body?.error ?? "The agent couldn't start.");
        }
        const reader = res.body.getReader();
        const decoder = new TextDecoder();
        let buffer = "";
        for (;;) {
          const { value, done } = await reader.read();
          if (done) break;
          buffer += decoder.decode(value, { stream: true });
          let nl: number;
          while ((nl = buffer.indexOf("\n")) >= 0) {
            const line = buffer.slice(0, nl).trim();
            buffer = buffer.slice(nl + 1);
            if (line) {
              try {
                handle(JSON.parse(line) as AgentEvent);
              } catch {
                // ignore a malformed line rather than killing the turn
              }
            }
          }
        }
        if (!finished) updateTurn(id, (t) => ({ ...t, status: "error", error: "The connection dropped before the agent finished." }));
      } catch (err) {
        if (ctrl.signal.aborted) {
          updateTurn(id, (t) => ({
            ...t,
            status: "stopped",
            steps: t.steps.map((s) => (s.status === "running" ? { ...s, status: "error", summary: "Stopped", endedAt: Date.now() } : s)),
          }));
        } else {
          updateTurn(id, (t) => ({ ...t, status: "error", error: err instanceof Error ? err.message : "Something went wrong." }));
        }
      } finally {
        abortRef.current = null;
        setRunning(false);
        inputRef.current?.focus();
      }
    },
    [running, choice, history, passId, network, updateTurn, refreshConsole]
  );

  function newChat() {
    abortRef.current?.abort();
    setTurns([]);
    setHistory([]);
    setLockedProvider(null);
    setSession({ spent: 0, calls: 0, steps: 0 });
    setInput("");
    inputRef.current?.focus();
  }

  // One tap on a result says what a person would type: "Use Birdeye (circle:birdeye)."
  function onAsk(text: string) {
    if (running) return;
    send(text);
  }

  function onAnswered(turnId: string, picks: string[]) {
    const text = answerText(picks);
    if (!text || running) return;
    updateTurn(turnId, (t) => ({ ...t, answered: picks }));
    send(text);
  }

  function onApprovalResolved(turnId: string, intentId: string, o: ApprovalOutcome) {
    updateTurn(turnId, (t) => ({ ...t, approvals: t.approvals.map((a) => (a.intentId === intentId ? { ...a, resolved: o } : a)) }));
    refreshConsole();
    setCallsKey((k) => k + 1);
    send(o === "approved" ? "I approved it. Run it now." : "I cancelled it. Don't run this call.");
  }

  function pickModel(c: ModelChoice) {
    setChoice(c);
    try {
      window.localStorage.setItem(MODEL_KEY, JSON.stringify(c));
    } catch {
      // storage blocked — choice still applies for this page
    }
  }

  function pickPass(id: string) {
    setPassId(id);
    try {
      window.localStorage.setItem(PASS_KEY, id || "none");
    } catch {
      // storage blocked — the choice still applies for this page
    }
  }

  const title = turns[0]?.user ?? "New chat";
  const pass = passes === null ? undefined : (passes.find((p) => p.id === passId) ?? null);
  const rail = rails?.find((r) => r.network === network);

  return (
    <div className="agent-workspace grid h-full lg:grid-cols-[minmax(0,1fr)_304px]">
      <section className="flex min-h-0 min-w-0 flex-col">
        <header className="flex h-14 shrink-0 items-center gap-4 border-b border-border px-5">
          <h1 className="min-w-0 flex-1 truncate text-[0.95rem] font-medium text-foreground">{title}</h1>
          <div className="hidden items-center gap-4 text-xs text-muted sm:flex">
            <span>
              Paid <span className="font-mono text-primary tabular-nums">{formatUSDC(session.spent)}</span>
            </span>
            <span className="h-3 w-px bg-border-strong" aria-hidden="true" />
            <span>
              <span className="font-mono text-foreground tabular-nums">{session.calls}</span> paid call{session.calls === 1 ? "" : "s"}
            </span>
          </div>
          {turns.length > 0 && (
            <button
              type="button"
              onClick={newChat}
              className="inline-flex h-8 items-center gap-1.5 rounded-lg px-2.5 text-sm text-muted transition-colors hover:bg-primary-tint hover:text-foreground"
            >
              <IconRefresh size={15} /> New chat
            </button>
          )}
        </header>

        <div
          ref={scrollRef}
          onScroll={(e) => {
            const el = e.currentTarget;
            stickRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < 120;
          }}
          className="min-h-0 flex-1 overflow-y-auto"
        >
          <div className="mx-auto w-full max-w-[720px] px-5 py-8">
            {turns.length === 0 ? (
              <EmptyState
                name={firstName(user)}
                pass={pass}
                network={network}
                anyProvider={anyProvider}
                loadingProviders={providers === null}
                onPick={(s) => {
                  setInput(s);
                  inputRef.current?.focus();
                }}
              />
            ) : (
              <div className="space-y-10">
                {turns.map((t, i) => (
                  <TurnView
                    key={t.id}
                    turn={t}
                    latest={i === turns.length - 1}
                    onResolved={onApprovalResolved}
                    onAnswered={onAnswered}
                    onAsk={onAsk}
                    canAnswer={!running && i === turns.length - 1}
                  />
                ))}
              </div>
            )}
          </div>
        </div>

        <div className="shrink-0 px-5 pb-5">
          <form
            onSubmit={(e) => {
              e.preventDefault();
              send(input);
            }}
            className="mx-auto w-full max-w-[720px] rounded-2xl border border-border-strong bg-surface shadow-[0_10px_30px_-18px_rgba(11,16,32,0.35)] transition-[border-color,box-shadow] focus-within:border-primary/70 focus-within:shadow-[0_0_0_4px_color-mix(in_srgb,var(--color-primary)_10%,transparent),0_10px_30px_-18px_rgba(11,16,32,0.35)]"
          >
            <label htmlFor="agent-input" className="sr-only">
              Message the agent
            </label>
            <textarea
              id="agent-input"
              ref={inputRef}
              value={input}
              onChange={(e) => {
                setInput(e.target.value);
                const el = e.target;
                el.style.height = "auto";
                el.style.height = `${Math.min(el.scrollHeight, 200)}px`;
              }}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                  e.preventDefault();
                  send(input);
                }
              }}
              rows={2}
              maxLength={4000}
              disabled={!anyProvider && providers !== null}
              placeholder={
                anyProvider || providers === null
                  ? "Describe a task and a maximum spend…"
                  : "Connect an AI model to start"
              }
              className="block max-h-[200px] w-full resize-none bg-transparent px-4 pt-3.5 text-[0.95rem] leading-relaxed text-foreground placeholder:text-muted/80 focus:outline-none disabled:cursor-not-allowed"
            />
            <div className="flex items-center gap-2 px-2.5 pt-1 pb-2.5">
              <ModelPicker providers={providers} value={choice} onChange={pickModel} lockedProvider={lockedProvider} />
              <PassPicker passes={passes} value={passId} onChange={pickPass} disabled={running} />
              <span className="hidden text-xs text-muted/80 xl:ml-auto xl:inline">
                <kbd className="font-mono">Enter</kbd> to send
              </span>
              {running ? (
                <button
                  type="button"
                  onClick={() => abortRef.current?.abort()}
                  aria-label="Stop"
                  className="ml-auto flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-foreground text-background transition-transform active:scale-95 xl:ml-0"
                >
                  <IconStop size={16} />
                </button>
              ) : (
                <button
                  type="submit"
                  disabled={!input.trim() || !anyProvider}
                  aria-label="Send"
                  className="ml-auto flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-primary text-primary-tint transition-[transform,opacity] active:scale-95 disabled:opacity-35 xl:ml-0"
                >
                  <IconArrowUp size={17} strokeWidth={2} />
                </button>
              )}
            </div>
          </form>
          <p className="mx-auto mt-2 max-w-[720px] text-center text-xs text-muted/80">
            USDC on Solana {networkLabel(network).toLowerCase()} · Spend Pass checked on every request · Approvals stay with you
          </p>
        </div>
      </section>

      <aside className="agent-inspector hidden min-h-0 flex-col border-l border-border lg:flex">
        <div className="agent-inspector-tabs" aria-label="Agent details"><button onClick={() => setInspector("pass")} aria-pressed={inspector === "pass"}>Spend pass</button><button onClick={() => setInspector("activity")} aria-pressed={inspector === "activity"}>Recent calls</button></div>
        <div className="min-h-0 flex-1">{inspector === "pass" ? <PassPanel pass={pass} rail={rails === null ? undefined : (rail ?? { network, configured: false })} network={network} session={session} /> : <RecentCallsPanel refreshKey={callsKey} />}</div>
      </aside>
    </div>
  );
}

function TurnView({
  turn,
  latest,
  onResolved,
  onAnswered,
  onAsk,
  canAnswer,
}: {
  turn: Turn;
  latest: boolean;
  onResolved: (turnId: string, intentId: string, o: ApprovalOutcome) => void;
  onAnswered: (turnId: string, picks: string[]) => void;
  onAsk: (text: string) => void;
  canAnswer: boolean;
}) {
  const running = turn.status === "running";
  return (
    <div className="space-y-4">
      <motion.div initial={{ opacity: 0, y: 6 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.3, ease: EASE }} className="flex justify-end">
        <p className="max-w-[85%] rounded-2xl rounded-br-md bg-primary px-4 py-2.5 text-[0.95rem] leading-relaxed whitespace-pre-wrap text-primary-tint">
          {turn.user}
        </p>
      </motion.div>

      {turn.notes.length > 0 && (
        <div className="space-y-1 text-[0.95rem] leading-relaxed text-muted">
          {turn.notes.map((n, i) => (
            <RichText key={i} text={n} />
          ))}
        </div>
      )}

      <Timeline steps={turn.steps} running={running} defaultOpen={latest} onAsk={canAnswer && turn.status === "done" ? onAsk : undefined} />

      {running && turn.steps.length === 0 && !turn.questions && (
        <ThinkingLine lines={["Reading your request", "Checking your Spend Pass", "Planning which API to use"]} />
      )}

      {turn.questions && turn.questions.length > 0 && (
        <QuestionCard
          questions={turn.questions}
          answered={turn.answered}
          active={canAnswer && turn.status === "done"}
          onAnswer={(picks) => onAnswered(turn.id, picks)}
        />
      )}

      {turn.approvals.map((a) => (
        <ApprovalCard
          key={a.intentId}
          intentId={a.intentId}
          provider={a.provider}
          resolved={a.resolved}
          onResolved={(o) => onResolved(turn.id, a.intentId, o)}
        />
      ))}

      <AnimatePresence>
        {turn.reply && (
          <motion.div
            initial={{ opacity: 0, y: 6 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ duration: 0.35, ease: EASE }}
            className="text-[0.975rem] leading-relaxed text-foreground"
          >
            <RichText text={turn.reply} />
          </motion.div>
        )}
      </AnimatePresence>

      {turn.status === "stopped" && <p className="text-sm text-muted">Stopped. Nothing past the last completed step was done.</p>}
      {turn.error && (
        <p role="alert" className="rounded-xl bg-danger-tint px-4 py-3 text-sm text-danger">
          {turn.error}
        </p>
      )}
    </div>
  );
}

/** Which Spend Pass the chat pays under. Only active USDC passes can pay for API calls. */
function PassPicker({
  passes,
  value,
  onChange,
  disabled,
}: {
  passes: SpendPass[] | null;
  value: string;
  onChange: (id: string) => void;
  disabled: boolean;
}) {
  if (passes === null) return null;
  if (passes.length === 0) {
    return (
      <Link href="/console/passes" className="inline-flex h-8 shrink-0 items-center rounded-lg px-2.5 text-xs font-medium text-primary hover:bg-primary-tint">
        Create a USDC pass to pay
      </Link>
    );
  }
  return (
    <label className="inline-flex h-8 min-w-0 items-center gap-1.5 rounded-lg px-2 text-xs text-muted hover:bg-primary-tint/60">
      <IconShield size={14} className="shrink-0 text-primary" />
      <span className="sr-only">Spend Pass</span>
      <select
        value={value}
        disabled={disabled}
        onChange={(e) => onChange(e.target.value)}
        className="max-w-[200px] min-w-0 truncate bg-transparent text-xs font-medium text-foreground focus:outline-none disabled:opacity-60"
      >
        <option value="">No pass: look, don&apos;t pay</option>
        {passes.map((p) => (
          <option key={p.id} value={p.id}>
            {p.label} · {formatUSDC(p.remaining_minor_units)} left
          </option>
        ))}
      </select>
    </label>
  );
}

function EmptyState({
  name,
  pass,
  network,
  anyProvider,
  loadingProviders,
  onPick,
}: {
  name: string;
  pass: SpendPass | null | undefined;
  network: string;
  anyProvider: boolean;
  loadingProviders: boolean;
  onPick: (s: string) => void;
}) {
  const suggestions = SUGGESTIONS[network] ?? SUGGESTIONS.solana;
  const where = networkLabel(network).toLowerCase();
  return (
    <div className="agent-welcome pt-6 sm:pt-14">
      <div className="agent-welcome-mark"><Logo size={32} /></div>
      <p className="agent-welcome-eyebrow">A LITTLE AUTONOMY. A LOT OF POSSIBILITY.</p>
      <motion.h2
        initial={{ opacity: 0, y: 8 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.5, ease: EASE }}
        className="font-display text-[1.9rem] leading-[1.12] font-semibold tracking-tight text-balance text-foreground sm:text-[2.3rem]"
      >
        {name ? `What should I get done, ${name}?` : "What should I get done?"}
      </motion.h2>
      <motion.p
        initial={{ opacity: 0, y: 8 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.5, delay: 0.05, ease: EASE }}
        className="mt-3 flex items-start gap-2 text-[0.95rem] leading-relaxed text-muted"
      >
        <IconShield size={16} className="mt-1 shrink-0 text-primary" />
        <span>
          {pass === undefined
            ? "Every paid call runs through your Spend Pass first."
            : pass === null
              ? "No Spend Pass picked: I can find APIs and read their prices, and I'll pay once you pick a pass below."
              : `I pay from “${pass.label}” on Solana ${where}: ${formatUSDC(pass.remaining_minor_units)} left${
                  pass.approve_above_minor_units ? `, and I'll ask you from ${formatUSDC(pass.approve_above_minor_units)} a call` : ""
                }.`}
        </span>
      </motion.p>

      {!loadingProviders && !anyProvider ? (
        <div className="mt-10 rounded-2xl border border-dashed border-border-strong p-6">
          <p className="font-medium text-foreground">Connect an AI model to start</p>
          <p className="mt-2 text-sm leading-relaxed text-muted">
            This workspace needs a model provider configured by its administrator. You can also bring your own agent and connect it with a Spend Pass over REST or MCP.
          </p>
          <Link href="/console/connect" className="mt-4 inline-flex items-center gap-2 text-xs font-medium text-primary">Connect your own agent <IconArrowUp size={13} className="rotate-45" /></Link>
        </div>
      ) : (
        <div className="mt-10 grid gap-2.5 sm:grid-cols-2">
          {suggestions.map((s, i) => (
            <motion.button
              key={s}
              type="button"
              onClick={() => onPick(s)}
              initial={{ opacity: 0, y: 10 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ duration: 0.45, delay: 0.1 + i * 0.05, ease: EASE }}
              whileHover={{ y: -2 }}
              whileTap={{ scale: 0.98 }}
              className="rounded-2xl border border-border-strong bg-surface px-4 py-3.5 text-left text-[0.925rem] leading-snug text-foreground transition-[border-color,box-shadow] hover:border-primary/50 hover:shadow-[0_10px_24px_-18px_rgba(11,16,32,0.5)]"
            >
              {s}
            </motion.button>
          ))}
        </div>
      )}
    </div>
  );
}
