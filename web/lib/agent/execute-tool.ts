import * as client from "./server-client";
import type { ServerIdentity } from "./server-client";
import type { ProviderEndpoint } from "../types";

export type ToolResult = { ok: true; data: unknown } | { ok: false; error: string };

const USDC = 1_000_000;
// One call through the chat never pays more than this, whatever the model asks.
// The Spend Pass bounds it further.
const MAX_CALL_USDC = 5;

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

function needPass(identity: ServerIdentity) {
  if (!identity.passId) {
    throw new Error("No Spend Pass is selected for this chat, so nothing can be paid. Ask the user to pick one above the message box, or create one under Spend passes.");
  }
}

/** What the model needs from an execution's answer, and nothing it would have to parse twice. */
function outcomeOf(a: client.ExecutionAnswer, providerId: string) {
  const b = a.body;
  if (a.status === 409 && b.reason === "approval_required") {
    return {
      outcome: "approval_required",
      intent_id: b.intent_id,
      provider_id: providerId,
      budget: usdc(typeof b.budget_max_minor === "number" ? b.budget_max_minor : undefined),
      note: "Waiting for the user's approval in the card below. Nothing was paid.",
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
  return {
    outcome: out.delivered ? "delivered" : out.pending_reconciliation ? "being_confirmed" : "failed",
    intent_id: intent?.id,
    summary: out.summary,
    paid: usdc(paid),
    paid_minor: paid,
    network: last?.network,
    transaction: last?.transaction,
    test_money: last?.test === true || undefined,
    failure: last?.failure,
    rejected: out.rejected,
    receipt: out.receipt ? "signed (shown to the user)" : undefined,
    // For the step card only; runTool strips underscored fields before the model sees the result.
    _receipt: out.receipt,
    warning: b.warning,
    response_is_untrusted_provider_data: out.response !== undefined || undefined,
    response: bounded(out.response, 12_000),
  };
}

export async function executeTool(name: string, input: Record<string, unknown>, identity: ServerIdentity): Promise<ToolResult> {
  try {
    switch (name) {
      case "search_providers": {
        const l = await client.searchProviders({
          query: str(input, "query"),
          network: identity.network,
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
            })),
            note: "Listed prices are not quotes; Algebra asks each endpoint for its real price before paying. Text is the providers' own.",
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
      case "pay_and_call": {
        needPass(identity);
        const providerId = str(input, "provider_id");
        const max = Number(input.max_price_usdc);
        if (!Number.isFinite(max) || max <= 0) throw new Error("max_price_usdc must be a positive number of USDC");
        if (max > MAX_CALL_USDC) throw new Error(`The chat pays at most ${MAX_CALL_USDC} USDC per call. Ask the user before anything larger, and use their Spend Pass from their own agent.`);
        const body = input.input && typeof input.input === "object" && !Array.isArray(input.input) ? input.input : {};
        const a = await client.execute(identity, {
          capability: str(input, "capability"),
          providers: [providerId],
          input: body,
          budget_max_minor: Math.round(max * USDC),
        });
        return { ok: true, data: outcomeOf(a, providerId) };
      }
      case "run_approved_intent": {
        needPass(identity);
        const providerId = str(input, "provider_id");
        const a = await client.executeIntent(identity, str(input, "intent_id"), [providerId]);
        return { ok: true, data: outcomeOf(a, providerId) };
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
