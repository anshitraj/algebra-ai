"use client";

import { useCallback, useEffect, useState } from "react";
import * as api from "@/lib/api-client";
import type { SpendPass } from "@/lib/types";
import type { ClassRow, KillSwitchState, NewProviderRule, PassControls, Simulation } from "@/lib/routing-types";
import { formatUSDC } from "@/lib/money";
import { networkLabel, useNetwork } from "@/lib/network";
import { Button, ErrorNote, Field, Input, PageHeader, Panel, Skeleton, StatusBadge } from "@/components/console/ui";
import { IconBan, IconCheck, IconShield, IconStop, Spinner } from "@/components/icons";

/**
 * The spend firewall: what stops an agent before money moves, and a way to ask
 * "would this pass?" without paying anything.
 */
export default function FirewallPage() {
  const [passes, setPasses] = useState<SpendPass[] | null>(null);
  const [kill, setKill] = useState<KillSwitchState | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const [p, k] = await Promise.all([api.listPasses(), api.getKillSwitch()]);
      setPasses((p.passes ?? []).filter((x) => x.active && x.currency === "USDC"));
      setKill(k);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't load your passes");
    }
  }, []);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- fetching from the REST API on mount
    load();
  }, [load]);

  return (
    <div className="mx-auto max-w-5xl">
      <PageHeader
        title="Spend firewall"
        description="Your rules, enforced before every payment. Set spending speed, control new providers, or pause every agent at once."
      />
      {error && <ErrorNote>{error}</ErrorNote>}

      <KillSwitch state={kill} onChange={(k) => { setKill(k); load(); }} />

      <section className="mt-8">
        <h2 className="text-sm font-medium text-foreground">Per-pass controls</h2>
        <div className="mt-3 space-y-3">
          {!passes && !error && <Skeleton className="h-24" />}
          {passes?.length === 0 && <p className="text-sm text-muted">No active USDC Spend Pass. Create one under Spend passes.</p>}
          {passes?.map((p) => <PassControlsCard key={p.id} pass={p} onSaved={load} />)}
        </div>
      </section>

      <Simulator passes={passes ?? []} />
    </div>
  );
}

function KillSwitch({ state, onChange }: { state: KillSwitchState | null; onChange: (k: KillSwitchState) => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const engaged = !!state?.engaged;
  async function flip() {
    setBusy(true);
    try {
      onChange(await api.setKillSwitch(!engaged));
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't change the kill switch");
    } finally {
      setBusy(false);
    }
  }
  return (
    <div
      className={`mt-6 flex flex-wrap items-center gap-4 rounded-2xl border px-5 py-4 ${
        engaged ? "border-danger/40 bg-danger-tint" : "border-border bg-surface"
      }`}
    >
      <span className={`flex h-11 w-11 shrink-0 items-center justify-center rounded-full ${engaged ? "bg-danger text-danger-tint" : "bg-primary-tint text-primary"}`}>
        {engaged ? <IconStop size={20} /> : <IconShield size={20} />}
      </span>
      <div className="min-w-0 flex-1">
        <p className="font-display text-base font-semibold text-foreground">{engaged ? "Kill switch is on" : "Kill switch"}</p>
        <p className="mt-0.5 text-sm text-muted">
          {state === null
            ? "Checking…"
            : engaged
              ? `All ${state.frozen_passes} passes are frozen. Nothing is reserved or paid, including an attempt already running.`
              : `Freeze every agent at once (${state.live_passes} live passes${state.frozen_passes ? `, ${state.frozen_passes} frozen` : ""}). It takes effect at the next check, even right before a payment is signed.`}
        </p>
        {error && <p className="mt-1 text-sm text-danger">{error}</p>}
      </div>
      <Button variant={engaged ? "secondary" : "danger"} onClick={flip} disabled={busy || state === null || state.live_passes === 0}>
        {busy ? <Spinner size={14} /> : engaged ? <IconCheck size={14} /> : <IconBan size={14} />}
        {engaged ? "Lift the kill switch" : "Freeze every pass"}
      </Button>
    </div>
  );
}

const RULES: { id: NewProviderRule; label: string; hint: string }[] = [
  { id: "cap", label: "Cap each call", hint: "A provider you've never paid may be paid, up to the cap per call, until it's been paid once." },
  { id: "approve", label: "Ask me", hint: "Paying a provider you've never paid waits for your approval; the router tries ones you have paid first." },
  { id: "allow", label: "Allow", hint: "New providers are paid like any other." },
];

function PassControlsCard({ pass, onSaved }: { pass: SpendPass; onSaved: () => void }) {
  const c = pass.controls;
  const [perMin, setPerMin] = useState(String(c?.max_calls_per_minute ?? 60));
  const [perProvider, setPerProvider] = useState(String(c?.max_calls_per_provider_per_minute ?? 20));
  const [rule, setRule] = useState<NewProviderRule>(c?.new_providers ?? "cap");
  const [cap, setCap] = useState(String((c?.new_provider_cap_minor_units ?? 50_000) / 1_000_000));
  const [busy, setBusy] = useState<"" | "save" | "freeze">("");
  const [msg, setMsg] = useState<string | null>(null);
  const frozen = !!pass.frozen_at;

  async function save() {
    setBusy("save");
    try {
      const body: PassControls = {
        max_calls_per_minute: Math.max(1, Math.round(Number(perMin) || 0)),
        max_calls_per_provider_per_minute: Math.max(1, Math.round(Number(perProvider) || 0)),
        new_providers: rule,
        new_provider_cap_minor_units: Math.max(0, Math.round((Number(cap) || 0) * 1_000_000)),
      };
      await api.setPassControls(pass.id, body);
      setMsg("Saved");
      onSaved();
    } catch (e) {
      setMsg(e instanceof Error ? e.message : "Couldn't save");
    } finally {
      setBusy("");
    }
  }

  async function freeze() {
    setBusy("freeze");
    try {
      await api.freezePass(pass.id, !frozen);
      onSaved();
    } catch (e) {
      setMsg(e instanceof Error ? e.message : "Couldn't change it");
    } finally {
      setBusy("");
    }
  }

  return (
    <Panel>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="min-w-0">
          <p className="flex items-center gap-2 text-sm font-medium text-foreground">
            {pass.label} {frozen && <StatusBadge status="DENIED" label="frozen" />}
          </p>
          <p className="mt-0.5 text-xs text-muted">
            {formatUSDC(pass.remaining_minor_units)} left of {formatUSDC(pass.budget_minor_units)}
            {pass.allowed_merchants.length > 0 ? ` · only ${pass.allowed_merchants.join(", ")}` : ""}
          </p>
        </div>
        <Button variant={frozen ? "secondary" : "danger"} onClick={freeze} disabled={busy !== ""}>
          {busy === "freeze" ? <Spinner size={14} /> : frozen ? <IconCheck size={14} /> : <IconBan size={14} />} {frozen ? "Unfreeze" : "Freeze"}
        </Button>
      </div>
      <div className="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Field label="Paid calls per minute" hint="Across all providers. Stops a looping agent.">
          <Input type="number" min={1} value={perMin} onChange={(e) => setPerMin(e.target.value)} />
        </Field>
        <Field label="Per provider per minute">
          <Input type="number" min={1} value={perProvider} onChange={(e) => setPerProvider(e.target.value)} />
        </Field>
        <Field label="Providers you've never paid" hint={RULES.find((r) => r.id === rule)?.hint}>
          <select
            value={rule}
            onChange={(e) => setRule(e.target.value as NewProviderRule)}
            className="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-sm text-foreground"
          >
            {RULES.map((r) => (
              <option key={r.id} value={r.id}>
                {r.label}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Cap per call (USDC)">
          <Input type="number" min={0} step="0.001" value={cap} disabled={rule !== "cap"} onChange={(e) => setCap(e.target.value)} />
        </Field>
      </div>
      <div className="mt-4 flex items-center gap-3">
        <Button onClick={save} disabled={busy !== ""}>
          {busy === "save" && <Spinner size={14} />} Save controls
        </Button>
        {msg && <span className="text-sm text-muted">{msg}</span>}
      </div>
    </Panel>
  );
}

function Simulator({ passes }: { passes: SpendPass[] }) {
  const { network } = useNetwork();
  const [classes, setClasses] = useState<ClassRow[]>([]);
  const [passId, setPassId] = useState("");
  const [capability, setCapability] = useState("token.price");
  const [input, setInput] = useState('{"mint":"EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"}');
  const [budget, setBudget] = useState("0.05");
  const [strategy, setStrategy] = useState("auto");
  const [live, setLive] = useState(true);
  const [busy, setBusy] = useState(false);
  const [sim, setSim] = useState<Simulation | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api.listClasses().then((r) => setClasses(r.classes)).catch(() => {});
    const want = new URLSearchParams(window.location.search).get("class");
    // eslint-disable-next-line react-hooks/set-state-in-effect -- reading the link's ?class= once on mount
    if (want) setCapability(want);
  }, []);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- default to the first pass once they load
    if (!passId && passes.length > 0) setPassId(passes[0].id);
  }, [passes, passId]);

  useEffect(() => {
    const c = classes.find((x) => x.id === capability);
    // eslint-disable-next-line react-hooks/set-state-in-effect -- a class brings its own sample input
    if (c) setInput(JSON.stringify(c.sample));
  }, [capability, classes]);

  async function run() {
    setBusy(true);
    setError(null);
    try {
      let parsed: unknown;
      try {
        parsed = JSON.parse(input);
      } catch {
        throw new Error("The input isn't valid JSON");
      }
      setSim(
        await api.simulatePolicy({
          capability,
          input: parsed,
          budget_max_minor: Math.round((Number(budget) || 0) * 1_000_000),
          spend_pass_id: passId,
          constraints: { allowed_networks: [network] },
          provider_policy: { strategy },
          live_quotes: live,
        }),
      );
    } catch (e) {
      setSim(null);
      setError(e instanceof Error ? e.message : "The dry run failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="mt-10">
      <h2 className="text-sm font-medium text-foreground">Would this pass?</h2>
      <p className="mt-1 text-sm text-muted">
        A dry run of exactly what an agent&apos;s request would meet: your pass, its controls, the router&apos;s guards and the ranking. Nothing is
        reserved or paid; with live prices, each provider gets the same unpaid request a quote is.
      </p>
      <Panel className="mt-3">
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
          <Field label="Spend Pass">
            <select value={passId} onChange={(e) => setPassId(e.target.value)} className="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-sm text-foreground">
              {passes.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.label}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Work" hint="A class of work: every provider that does it is compared.">
            <select value={capability} onChange={(e) => setCapability(e.target.value)} className="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-sm text-foreground">
              {classes.length === 0 && <option value={capability}>{capability}</option>}
              {classes.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.title} ({c.id}) · {c.routable} providers
                </option>
              ))}
            </select>
          </Field>
          <Field label="Input">
            <Input value={input} onChange={(e) => setInput(e.target.value)} className="font-mono" />
          </Field>
          <div className="grid grid-cols-2 gap-4">
            <Field label="Budget (USDC)">
              <Input type="number" min={0} step="0.001" value={budget} onChange={(e) => setBudget(e.target.value)} />
            </Field>
            <Field label="Strategy">
              <select value={strategy} onChange={(e) => setStrategy(e.target.value)} className="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-sm text-foreground">
                <option value="auto">Best overall</option>
                <option value="cheapest">Cheapest</option>
                <option value="fastest">Fastest</option>
              </select>
            </Field>
          </div>
        </div>
        <div className="mt-4 flex flex-wrap items-center gap-4">
          <Button onClick={run} disabled={busy || !passId}>
            {busy && <Spinner size={14} />} Run the dry run
          </Button>
          <label className="flex items-center gap-2 text-sm text-muted">
            <input type="checkbox" checked={live} onChange={(e) => setLive(e.target.checked)} /> Ask providers for live prices (unpaid)
          </label>
          <span className="text-xs text-muted">on {networkLabel(network)}</span>
        </div>
        {error && <div className="mt-3"><ErrorNote>{error}</ErrorNote></div>}
      </Panel>
      {sim && <SimulationResult sim={sim} />}
    </section>
  );
}

const REASON_TEXT: Record<string, string> = {
  PASS_FROZEN: "the pass is frozen by the kill switch",
  PASS_RATE_LIMITED: "the pass made as many paid calls this minute as it may",
  PASS_NEW_PROVIDER_NEEDS_APPROVAL: "a provider you've never paid needs your approval",
  PASS_NEW_PROVIDER_OVER_CAP: "a provider you've never paid asks more than your cap",
  PASS_BUDGET_EXCEEDED: "over the pass's budget",
  PASS_PER_PURCHASE_LIMIT: "over the pass's per-call limit",
  PASS_APPROVAL_REQUIRED: "at or above your ask-me line",
  PASS_MERCHANT_NOT_ALLOWED: "the pass doesn't allow this provider",
  no_provider_passes: "no provider passed every check",
};

function SimulationResult({ sim }: { sim: Simulation }) {
  const tone = sim.verdict === "ALLOW" ? "border-primary/40" : sim.verdict === "DENY" ? "border-danger/40" : "border-accent/40";
  const passed = sim.candidates.filter((c) => c.verdict !== "DENY");
  const refused = sim.candidates.filter((c) => c.verdict === "DENY");
  return (
    <div className={`mt-4 rounded-2xl border ${tone} bg-surface p-5`}>
      <div className="flex flex-wrap items-center gap-3">
        <StatusBadge status={sim.verdict} />
        <p className="text-sm text-foreground">
          {sim.verdict === "DENY"
            ? "Algebra would refuse this."
            : sim.would_pay
              ? `Algebra would pay ${sim.would_pay.provider} ${formatUSDC(sim.would_pay.price_minor)}${sim.would_pay.price_source === "live" ? " (live price)" : " (listed price)"}${sim.verdict === "REQUIRE_APPROVAL" ? ", after your approval" : ""}.`
              : "Algebra would go ahead."}
        </p>
      </div>
      {sim.reasons && sim.reasons.length > 0 && (
        <ul className="mt-2 list-disc pl-5 text-sm text-muted">
          {sim.reasons.map((r) => (
            <li key={r}>{REASON_TEXT[r] ?? r}</li>
          ))}
        </ul>
      )}
      <p className="mt-2 text-xs text-muted">
        Pass: {formatUSDC(sim.pass.remaining_minor)} left · {sim.pass.calls_last_minute} paid calls in the last minute of {sim.pass.controls.max_calls_per_minute} · new
        providers: {sim.pass.controls.new_providers}
      </p>

      {sim.plan && sim.plan.offers.length > 0 && (
        <div className="mt-5">
          <p className="text-xs font-medium text-muted">The plan ({sim.plan.mode.toLowerCase()}): tried in order, falling back only when an attempt is proven to have moved no money</p>
          <ol className="mt-2 space-y-2">
            {sim.plan.offers.map((o) => (
              <li key={o.rank} className="rounded-xl border border-border px-4 py-3">
                <div className="flex flex-wrap items-center gap-3">
                  <span className="font-mono text-xs text-muted">#{o.rank}</span>
                  <span className="text-sm font-medium text-foreground">{o.provider}</span>
                  <span className="text-sm tabular-nums text-foreground">{formatUSDC(o.cost_minor)}</span>
                  <span className="ml-auto flex items-center gap-2 text-xs text-muted">
                    score
                    <span className="h-1.5 w-24 overflow-hidden rounded-full bg-border">
                      <span className="block h-full rounded-full bg-primary" style={{ width: `${Math.round(o.score * 100)}%` }} />
                    </span>
                    <span className="tabular-nums">{o.score.toFixed(2)}</span>
                  </span>
                </div>
                {o.notes && o.notes.length > 0 && <p className="mt-1 text-xs text-muted">{o.notes.join(" · ")}</p>}
              </li>
            ))}
          </ol>
        </div>
      )}

      {refused.length > 0 && (
        <div className="mt-5">
          <p className="text-xs font-medium text-muted">
            Refused ({refused.length}) · {passed.length} passed
          </p>
          <ul className="mt-2 divide-y divide-border rounded-xl border border-border">
            {refused.map((c, i) => (
              <li key={`${c.provider}-${i}`} className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 px-4 py-2 text-sm">
                <span className="text-foreground">{c.provider}</span>
                <span className="text-xs tabular-nums text-muted">
                  {c.price_minor > 0 ? formatUSDC(c.price_minor) : "price not stated"} {c.network ? `· ${networkLabel(c.network)}` : ""}
                </span>
                <span className="w-full text-xs text-danger sm:w-auto sm:flex-1 sm:text-right">{(c.reasons ?? []).join("; ")}</span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
