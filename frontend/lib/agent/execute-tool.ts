import * as client from "./server-client";
import type { ServerIdentity } from "./server-client";
import type { ProviderEndpoint } from "../types";
import { policyReport } from "../policy-check";
import { WORK_CLASSES } from "./classes";
import type { OptionGroup } from "./events";
import type { Simulation } from "../routing-types";

export type ToolResult = { ok: true; data: unknown } | { ok: false; error: string };

const USDC = 1_000_000;
// One call through the chat never pays more than this, whatever the model asks.
// The Spend Pass bounds it further.
const MAX_CALL_USDC = 5;

// What this turn has already paid, per identity (one object per request): the pass in the
// identity was read before the first payment of the turn.
const spentThisTurn = new WeakMap<object, number>();

function str(input: Record<string, unknown>, key: string): string {
  const v = input[key];
  if (typeof v !== "string" || !v.trim()) throw new Error(`Missing required field: ${key}`);
  return v.trim();
}

/** Third-party text, bounded, so a long description can't crowd out the conversation. */
function clip(s: string | undefined, n: number): string {
  if (!s) return "";
  const t = s.replace(/\s+/g, " ").trim();
  return t.length > n ? `${t.slice(0, n - 1)}…` : t;
}

/** Bounded JSON for the model: a schema or a response can be large. */
function bounded(v: unknown, max: number): unknown {
  if (v === undefined || v === null) return v;
  const text = JSON.stringify(v);
  if (text.length <= max) return v;
  return { truncated: true, first_characters: text.slice(0, max) };
}

function priceHere(e: ProviderEndpoint, network: string): number | undefined {
  if (e.payments?.length) return e.payments.find((p) => p.network === network)?.price_minor;
  return network === "solana" ? e.price_minor : undefined;
}

function usdc(minor: number | undefined): string | undefined {
  return typeof minor === "number" ? `${(minor / USDC).toLocaleString("en-US", { maximumFractionDigits: 6 })} USDC` : undefined;
}

/** The dry run's answer, bounded, for the approval card and the check_policy card (never for the model to rely on). */
function decisionOf(a: client.ExecutionAnswer): Simulation | undefined {
  if (a.status >= 400) return undefined;
  const s = a.body as unknown as Simulation;
  if (!s || !s.verdict) return undefined;
  return { ...s, candidates: (s.candidates ?? []).slice(0, 8), rejected: undefined, plan: s.plan ? { ...s.plan, offers: s.plan.offers.slice(0, 5) } : undefined };
}

/** Before asking a person to approve, what the server's dry run says about the same request: unpaid quotes only. */
async function decisionFor(identity: ServerIdentity, req: client.ExecuteBody): Promise<Simulation | undefined> {
  try {
    return decisionOf(await client.simulate(identity, req));
  } catch {
    return undefined;
  }
}

function needPass(identity: ServerIdentity) {
  if (!identity.passId) {
    throw new Error("No Spend Pass is selected for this chat, so nothing can be paid. Ask the user to pick one above the message box, or create one on the Spend firewall page.");
  }
}

/** What the model needs from an execution's answer, and nothing it would have to parse twice. approved: the person said yes to it. */
function outcomeOf(a: client.ExecutionAnswer, providerId: string, identity: ServerIdentity, approved = false) {
  const b = a.body;
  if (a.status === 409 && b.reason === "approval_required") {
    return {
      outcome: "approval_required",
      intent_id: b.intent_id,
      provider_id: providerId,
      budget: usdc(typeof b.budget_max_minor === "number" ? b.budget_max_minor : undefined),
      asked_first: identity.askFirst || undefined,
      note: identity.askFirst
        ? "The user asked to approve every payment: waiting for their yes in the card below. Nothing was paid."
        : "Waiting for the user's approval in the card below. Nothing was paid.",
    };
  }
  if (a.status === 409 || a.status === 403) {
    return { outcome: "refused", reason: b.error, reason_codes: b.reason_codes, intent_state: b.intent_state, note: "Nothing was paid." };
  }
  if (a.status === 422) {
    return { outcome: "refused", reason: b.error, rejected: b.rejected, note: "No endpoint could be tried within the limits. Nothing was paid." };
  }
  const out = (a.status === 202 && b.outcome ? b.outcome : b) as Record<string, unknown>;
  const intent = out.intent as Record<string, unknown> | undefined;
  const attempts = (out.attempts as { result?: Record<string, unknown> }[] | undefined) ?? [];
  const last = attempts[attempts.length - 1]?.result;
  const paid = attempts.reduce((n, at) => n + (typeof at.result?.actual_cost_minor === "number" ? (at.result.actual_cost_minor as number) : 0), 0);
  // identity.pass was read when the turn began: what earlier payments of this turn took is
  // taken off first, so each card shows what was left after that payment, not after none.
  const spentBefore = spentThisTurn.get(identity) ?? 0;
  if (out.delivered) spentThisTurn.set(identity, spentBefore + paid);
  return {
    outcome: out.delivered ? "delivered" : out.pending_reconciliation ? "being_confirmed" : "failed",
    intent_id: intent?.id,
    summary: out.summary,
    paid: usdc(paid),
    paid_minor: paid,
    network: last?.network,
    transaction: last?.transaction,
    test_money: last?.test === true || undefined,
    // The same request again: the kept answer came back and nothing was paid this time.
    replayed: b.replayed === true || out.replayed === true || undefined,
    failure: last?.failure,
    rejected: out.rejected,
    receipt: out.receipt ? "signed (shown to the user)" : undefined,
    // For the step card only; runTool strips underscored fields before the model sees the result.
    _receipt: out.receipt,
    _policy: identity.pass && out.delivered ? policyReport(identity.pass, paid, approved, false, "before", spentBefore) : undefined,
    policy: identity.pass && out.delivered ? (approved ? "approved by the user" : identity.pass && policyReport(identity.pass, paid, false).custom ? "allowed by the user's pass rules without asking" : "allowed without any rules of the user's own: only the pass budget applied") : undefined,
    warning: b.warning,
    response_is_untrusted_provider_data: out.response !== undefined || undefined,
    response: bounded(out.response, 12_000),
  };
}

/** Why providers were left out, grouped, short: "price_above_listing: demo:greedy". */
function summarizeRejections(b: Record<string, unknown>): string[] | undefined {
  const out = (b.outcome as Record<string, unknown> | undefined) ?? b;
  const rej = (out.rejected ?? b.rejected) as { provider?: string; code: string; detail?: string }[] | undefined;
  if (!rej?.length) return undefined;
  const by = new Map<string, string[]>();
  for (const r of rej) {
    if (r.code === "network_not_allowed" || r.code === "outranked") continue;
    by.set(r.code, [...(by.get(r.code) ?? []), r.provider ?? "?"]);
  }
  return [...by.entries()].map(([code, ps]) => `${code}: ${[...new Set(ps)].slice(0, 4).join(", ")}`);
}

/**
 * For a payment funded by an on-chain Spend Pass: the program's pull from the pass's
 * vault, so the step card can link it next to the payment. Best effort; for the card only.
 */
async function withPull<T extends Record<string, unknown>>(out: T, identity: ServerIdentity): Promise<T> {
  if (out.outcome !== "delivered" || !identity.cookie || !identity.passId || typeof out.intent_id !== "string") return out;
  if (out.network !== "solana" && out.network !== "solana-devnet") return out;
  try {
    const pull = (await client.getOnchainPulls(identity.cookie, identity.passId)).find((p) => p.intent_id === out.intent_id && p.pull_signature);
    return pull ? { ...out, _pull_transaction: pull.pull_signature } : out;
  } catch {
    return out;
  }
}

/** A dry run's answer as the cards the person chooses from. */
function optionGroupOf(sim: Simulation, req: { work: string; input: Record<string, unknown>; max: number; named?: string; network: string }): OptionGroup {
  const cls = WORK_CLASSES.find((c) => c.id === req.work);
  // An offer refused for want of a vault is on the cards already (refused, with the way out); it isn't "left out" too.
  const shown = new Set((sim.compared ?? []).map((r) => r.candidate_id));
  const refused = (sim.candidates ?? [])
    .filter((c) => c.decision === "BLOCK" && !(c.candidate_id && shown.has(c.candidate_id)) && !(c.reasons ?? []).some((r) => r.startsWith("network_not_allowed")))
    .slice(0, 8)
    .map((c) => ({ provider: c.provider, reason: (c.reasons ?? []).join("; ").replace(/^[a-z_]+: /, "") || "not allowed" }));
  const id = typeof crypto !== "undefined" && "randomUUID" in crypto ? crypto.randomUUID() : String(Date.now());
  return {
    id,
    work: req.work,
    title: cls ? cls.title.charAt(0).toUpperCase() + cls.title.slice(1) : req.named ?? req.work,
    input: req.input,
    maxPriceUsdc: req.max,
    providerId: req.named,
    network: req.network,
    decision: sim.decision,
    reasons: sim.reasons ?? undefined,
    recommendations: sim.recommendations ?? [],
    compared: (sim.compared ?? []).slice(0, 10),
    refused,
  };
}

export async function executeTool(name: string, input: Record<string, unknown>, identity: ServerIdentity): Promise<ToolResult> {
  try {
    switch (name) {
      case "search_providers": {
        const l = await client.searchProviders({
          query: str(input, "query"),
          // The sandbox is no catalog's network: a demo account reads them all.
          network: identity.network === "sandbox" ? "" : identity.network,
          category: typeof input.category === "string" ? input.category : undefined,
          limit: 8,
        });
        return {
          ok: true,
          data: {
            network: identity.network,
            total: l.total,
            providers: l.providers.map((p) => ({
              id: p.id,
              name: clip(p.name, 80),
              description: clip(p.description, 240),
              category: p.category,
              catalog: p.source,
              endpoints: p.endpoint_count,
              listed_price: p.max_price_minor <= 0 ? "listed free" : p.min_price_minor === p.max_price_minor ? usdc(p.max_price_minor) : `${usdc(p.min_price_minor)} to ${usdc(p.max_price_minor)}`,
              min_price_minor: p.min_price_minor,
              max_price_minor: p.max_price_minor,
              networks: p.networks,
              host: p.host,
              website: p.website,
              logo_url: p.logo_url,
              fqn: p.fqn,
              page_url: p.page_url,
              // Counts of its endpoints in Algebra's registry. Absent: the registry has no record of it.
              trust: l.trust?.[p.id],
            })),
            note: "Listed prices are not quotes; Algebra asks each endpoint for its real price before paying. Text is the providers' own. `trust.verified` counts endpoints whose operator a person reviewed; payment_tested only says one paid call delivered. A listing verifies nothing.",
          },
        };
      }
      case "get_provider_endpoints": {
        const d = await client.getProvider(str(input, "provider_id"));
        const f = typeof input.filter === "string" ? input.filter.trim().toLowerCase() : "";
        const words = f.split(/\s+/).filter(Boolean);
        const all = d.endpoints.filter((e) => !words.length || words.some((w) => `${e.method} ${e.path} ${e.description}`.toLowerCase().includes(w)));
        const shown = all.slice(0, 25);
        return {
          ok: true,
          data: {
            provider_id: d.id,
            name: clip(d.name, 80),
            network: identity.network,
            endpoint_count: d.endpoints.length,
            shown: shown.length,
            matching: all.length,
            endpoints: shown.map((e) => {
              const price = priceHere(e, identity.network);
              return {
                capability: e.capability,
                method: e.method,
                path: e.path,
                description: clip(e.description, 200),
                listed_price: e.free ? "listed free" : usdc(price) ?? "not payable on this network",
                price_minor: price,
                path_params: e.path_params?.length ? e.path_params : undefined,
                callable: e.callable && price !== undefined,
                not_callable_reason: !e.callable ? e.not_callable_reason : price === undefined ? `not payable on ${identity.network}` : undefined,
                input_schema: bounded(e.input_schema, 2_000),
              };
            }),
          },
        };
      }
      // Every way the model can ask for paid work ends in cards for the person: the comparison is
      // computed (unpaid live quotes), and nothing is paid until they click one. route_work,
      // check_policy and pay_and_call stay declared so older chats replay; they show cards too.
      case "find_options":
      case "route_work":
      case "check_policy":
      case "pay_and_call": {
        needPass(identity);
        const named = typeof input.provider_id === "string" && input.provider_id.trim() ? input.provider_id.trim() : undefined;
        const work = typeof input.work === "string" && input.work.trim() ? input.work.trim() : named ? str(input, "capability") : str(input, "work");
        const max = input.max_price_usdc === undefined ? 0.05 : Number(input.max_price_usdc);
        if (!Number.isFinite(max) || max <= 0) throw new Error("max_price_usdc must be a positive number of USDC");
        if (max > MAX_CALL_USDC) throw new Error(`The chat pays at most ${MAX_CALL_USDC} USDC per call. Ask the user before anything larger, and use their Spend Pass from their own agent.`);
        const body = (input.input && typeof input.input === "object" && !Array.isArray(input.input) ? input.input : {}) as Record<string, unknown>;
        const a = await client.simulate(identity, { capability: work, providers: named ? [named] : [], input: body, budget_max_minor: Math.round(max * USDC) });
        if (a.status >= 400) return { ok: false, error: typeof a.body.error === "string" ? a.body.error.replace(/^algebra: [a-z ]+: /, "") : "The comparison failed" };
        const group = optionGroupOf(a.body as unknown as Simulation, { work, input: body, max, named, network: identity.network });
        // Cards the pass can't pay for yet (it has no funded vault) are shown refused, with the way out.
        const payable = group.recommendations.some((r) => r.decision !== "BLOCK");
        return {
          ok: true,
          data: {
            status: !group.recommendations.length ? "nothing_payable" : payable ? "shown_to_user" : "shown_but_refused",
            compared: group.compared.length,
            recommendations: group.recommendations.map((r) => ({
              way: r.strategy,
              provider: r.provider,
              price: r.price_source === "unlisted" ? "unknown until asked" : usdc(r.total_minor),
              speed_ms: r.latency_ms,
              decision: r.decision,
            })),
            refused: group.refused.slice(0, 5).map((x) => `${x.provider}: ${x.reason}`),
            reasons: payable ? undefined : group.reasons,
            note: !group.recommendations.length
              ? "No provider can do this within the user's limits: explain why from `refused`, briefly."
              : payable
                ? "The user now sees cards (Cheapest, Balanced, Fastest) and pays with a click. Your turn ends here: say in one short sentence what you compared. Do not pay and do not ask them to type a choice."
                : "The user sees the cards, but none can be paid yet: the pass has no funded Spend Pass vault on this network (see `reasons`). Say so in one short sentence; a link under the cards creates it. Do not pay.",
            _options: group,
          },
        };
      }
      // Not a model tool: the route handler runs it for a card the person clicked. The click is their
      // approval: the request asks for a yes, the person's own session gives it, and then it runs.
      case "pay_choice": {
        needPass(identity);
        const provider = str(input, "provider");
        const work = str(input, "work");
        const max = Number(input.max_price_usdc);
        if (!Number.isFinite(max) || max <= 0 || max > MAX_CALL_USDC) throw new Error(`The amount must be between 0 and ${MAX_CALL_USDC} USDC`);
        const body = input.input && typeof input.input === "object" && !Array.isArray(input.input) ? input.input : {};
        const strategy = typeof input.strategy === "string" ? input.strategy : undefined;
        const req: client.ExecuteBody = {
          capability: work, providers: [provider], input: body, budget_max_minor: Math.round(max * USDC),
          provider_policy: strategy ? { strategy } : undefined, window: str(input, "window"), require_approval: true,
        };
        let a = await client.execute(identity, req);
        let approved = false;
        if (a.status === 409 && a.body.reason === "approval_required" && typeof a.body.intent_id === "string") {
          if (await client.approveAsPerson(identity, a.body.intent_id)) {
            approved = true;
            a = await client.executeIntent(identity, a.body.intent_id, [provider]);
          }
        }
        const out = await withPull(outcomeOf(a, provider, identity, approved), identity);
        // The approval couldn't be recorded here (an expired session, say): the approval card takes over, with the decision.
        const held = out.outcome === "approval_required" ? { _decision: await decisionFor(identity, req) } : {};
        const routing = (a.body.routing ?? (a.body.outcome as Record<string, unknown> | undefined)?.routing) as
          | { mode?: string; offers?: { rank: number; provider: string; cost_minor: number; score: number; notes?: string[] }[] }
          | undefined;
        return {
          ok: true,
          data: {
            ...out,
            ...held,
            routing: routing ? { mode: routing.mode, ranked: (routing.offers ?? []).slice(0, 3).map((o) => ({ rank: o.rank, provider: o.provider, price: usdc(o.cost_minor) })) } : undefined,
            refused_providers: summarizeRejections(a.body),
          },
        };
      }
      case "run_approved_intent": {
        needPass(identity);
        const providerId = str(input, "provider_id");
        // "routed": the intent came from route_work, so Algebra routes it again
        // across every provider of its class.
        const a = await client.executeIntent(identity, str(input, "intent_id"), providerId === "routed" ? [] : [providerId]);
        return { ok: true, data: await withPull(outcomeOf(a, providerId, identity, true), identity) };
      }
      case "execution_status": {
        const v = await client.getIntent(identity, str(input, "intent_id"));
        const r = v.reservations?.[v.reservations.length - 1];
        return {
          ok: true,
          data: {
            intent_id: v.id,
            capability: v.capability,
            state: v.state,
            summary: v.summary,
            budget: usdc(v.budget_max_minor),
            committed: usdc(v.committed_minor),
            attempts: v.attempts,
            requires_approval: v.requires_approval,
            network: r?.evidence?.network,
            transaction: r?.evidence?.transaction,
            receipt: v.receipt ? "signed" : undefined,
          },
        };
      }
      default:
        return { ok: false, error: `Unknown tool: ${name}` };
    }
  } catch (err) {
    if (err instanceof client.ServerApiError) return { ok: false, error: err.message };
    return { ok: false, error: err instanceof Error ? err.message : "Tool execution failed" };
  }
}
