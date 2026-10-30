"use client";

import { useCallback, useEffect, useState } from "react";
import * as api from "@/lib/api-client";
import type { IssuedPass, PassConnect, SpendPass } from "@/lib/types";
import type { ClassRow, KillSwitchState, Simulation } from "@/lib/routing-types";
import { formatUSDC } from "@/lib/money";
import { networkLabel, useNetwork } from "@/lib/network";
import { Button, ErrorNote, Field, Input, PageHeader, Panel, Skeleton } from "@/components/console/ui";
import { PolicyGuide } from "@/components/console/policy-guide";
import { DecisionPanel } from "@/components/console/decision-panel";
import { CreatePass, IssuedCard } from "@/components/console/pass-create";
import { IconBan, IconCheck, IconPlus, IconShield, IconStop, Spinner } from "@/components/icons";

/**
 * Spend passes and their rules, on one page: a pass is what an agent holds, its
 * rules are what stops it before money moves, and a dry run asks "would this
 * pass?" without paying anything.
 */
export default function FirewallPage() {
  const [passes, setPasses] = useState<SpendPass[] | null>(null);
  const [connect, setConnect] = useState<PassConnect | null>(null);
  const [kill, setKill] = useState<KillSwitchState | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [issued, setIssued] = useState<IssuedPass | null>(null);

  const load = useCallback(async () => {
    try {
      const [p, k] = await Promise.all([api.listPasses(), api.getKillSwitch()]);
      setPasses((p.passes ?? []).filter((x) => x.active && x.currency === "USDC"));
      setConnect(p.connect);
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

  // Arriving from onboarding (?new=1): open the form straight away.
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- reading the URL once on arrival
    if (new URLSearchParams(window.location.search).get("new") === "1") setCreating(true);
  }, []);

  // Arriving from the chat (#pass-<id>): the passes load after the page, so the browser's own jump finds nothing.
  useEffect(() => {
    const id = window.location.hash.slice(1);
    if (!passes || !id.startsWith("pass-")) return;
    document.getElementById(id)?.scrollIntoView({ block: "center" });
  }, [passes]);

  return (
    <div className="mx-auto max-w-5xl">
      <PageHeader
        title="Spend Passes"
        description="Each agent holds a pass: a limit and your rules, checked before every payment. Switch one off, or stop every agent at once."
        actions={
          !creating && !issued ? (
            <Button onClick={() => setCreating(true)}>
              <IconPlus size={15} /> New pass
            </Button>
          ) : undefined
        }
      />
      {error && <ErrorNote>{error}</ErrorNote>}

      <KillSwitch state={kill} onChange={(k) => { setKill(k); load(); }} />

      {(issued || creating) && (
        <div className="mt-6 space-y-4">
          {issued && connect && (
            <IssuedCard
              pass={issued}
              connect={issued.connect ?? connect}
              onDone={() => {
                setIssued(null);
                load();
              }}
            />
          )}
          {creating && (
            <CreatePass
              onCancel={() => setCreating(false)}
              onCreated={(p) => {
                setCreating(false);
                setIssued(p);
              }}
            />
          )}
        </div>
      )}

      <section className="mt-8">
        <h2 className="text-sm font-medium text-foreground">Your agents</h2>
        <p className="mt-1 text-sm text-muted">Open a folder to read its rules; a paper inside opens that rule.</p>
        <div className="mt-4 grid grid-cols-1 items-start gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {!passes && !error && <Skeleton className="col-span-full h-80" />}
          {passes?.length === 0 && !creating && !issued && (
            <div className="col-span-full rounded-2xl border border-dashed border-border-strong px-6 py-12 text-center">
              <span className="mx-auto flex h-11 w-11 items-center justify-center rounded-xl bg-primary-tint text-primary">
                <IconShield size={20} />
              </span>
              <p className="font-display mt-4 text-lg font-semibold text-foreground">No Spend Pass yet</p>
              <p className="mx-auto mt-1.5 max-w-md text-sm text-muted">
                Give Claude 1 USDC a week for market data, asking you before every payment. Revoke it with one click.
              </p>
              <div className="mt-5">
                <Button onClick={() => setCreating(true)}>
                  <IconPlus size={15} /> Create your first pass
                </Button>
              </div>
            </div>
          )}
          {passes?.map((p) => <PolicyGuide key={p.id} pass={p} onSaved={load} />)}
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
      <div className="min-w-0 flex-[1_1_200px]">
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

function Simulator({ passes }: { passes: SpendPass[] }) {
  const { network } = useNetwork();
  const [classes, setClasses] = useState<ClassRow[]>([]);
  const [passId, setPassId] = useState("");
  const [capability, setCapability] = useState("token.price");
  const [input, setInput] = useState('{"mint":"EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"}');
  const [budget, setBudget] = useState("0.05");
  const [strategy, setStrategy] = useState("balanced");
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
                <option value="cheapest">Cheapest</option>
                <option value="balanced">Balanced</option>
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
      {sim && <SimulationResult sim={sim} pass={passes.find((p) => p.id === passId)} />}
    </section>
  );
}

function SimulationResult({ sim, pass }: { sim: Simulation; pass?: SpendPass }) {
  const passed = sim.candidates.filter((c) => c.verdict !== "DENY");
  const refused = sim.candidates.filter((c) => c.verdict === "DENY");
  return (
    <div className="mt-4">
      <DecisionPanel sim={sim} pass={pass} />
      <p className="mt-2 text-xs text-muted">
        Pass: {formatUSDC(sim.pass.remaining_minor)} left · {sim.pass.calls_last_minute} paid calls in the last minute of {sim.pass.controls.max_calls_per_minute} · new
        providers: {sim.pass.controls.new_providers}. A dry run: nothing was reserved or paid.
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
