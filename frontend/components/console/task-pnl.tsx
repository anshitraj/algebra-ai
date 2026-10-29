"use client";

import { useState } from "react";
import * as api from "@/lib/api-client";
import type { AgentPnL, JobPass, ReplayReport } from "@/lib/types";
import { usdcNumber } from "@/lib/money";
import { toMinor } from "@/lib/wallet/chains";
import { IconBan, IconCheck, Spinner } from "@/components/icons";
import styles from "./task-pnl.module.css";

const u = (minor: number) => usdcNumber(minor);
const signed = (minor: number) => `${minor >= 0 ? "+" : "−"}${u(Math.abs(minor))}`;

/** An agent's money over the last 30 days: what it paid, wasted, saved, and its tasks' P&L. */
export function AgentPnLSummary({ pnl }: { pnl: AgentPnL }) {
  if (pnl.paid_minor === 0 && pnl.saved_minor === 0 && pnl.tasks === 0) return null;
  return (
    <dl className={styles.summary} aria-label={`Last ${pnl.days} days`}>
      <div>
        <dt>Paid</dt>
        <dd>{u(pnl.paid_minor)}</dd>
      </div>
      <div>
        <dt>Accepted</dt>
        <dd>
          {pnl.accepted}
          {pnl.accepted > 0 && <small> · {u(pnl.cost_per_accepted_minor)} each</small>}
        </dd>
      </div>
      {pnl.wasted_minor > 0 && (
        <div data-tone="bad">
          <dt>Wasted</dt>
          <dd>{u(pnl.wasted_minor)}</dd>
        </div>
      )}
      {pnl.saved_minor > 0 && (
        <div data-tone="good">
          <dt>Saved by reuse</dt>
          <dd>{u(pnl.saved_minor)}</dd>
        </div>
      )}
      {pnl.valued_tasks > 0 && (
        <div data-tone={pnl.profit_minor >= 0 ? "good" : "bad"}>
          <dt>
            P&amp;L · {pnl.valued_tasks} task{pnl.valued_tasks === 1 ? "" : "s"} with a value
          </dt>
          <dd>{signed(pnl.profit_minor)}</dd>
        </div>
      )}
    </dl>
  );
}

/** A task's P&L: what it was worth, what it paid, what that came to; the value can be set here. */
export function TaskPnLLine({ job, onChanged }: { job: JobPass; onChanged: (j: JobPass) => void }) {
  const p = job.pnl;
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState(p.value_minor !== undefined ? u(p.value_minor) : "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const minor = value.trim() === "" ? null : toMinor(value, 6);

  async function save(clear = false) {
    setBusy(true);
    setError(null);
    try {
      onChanged(await api.setJobValue(job.id, clear ? null : minor === null ? null : Number(minor)));
      setEditing(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't save it");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className={styles.task}>
      <p className={styles.line}>
        {p.value_minor !== undefined && p.profit_minor !== undefined ? (
          <>
            Worth <strong>{u(p.value_minor)}</strong> · paid {u(p.paid_minor)} ·{" "}
            <strong data-tone={p.profit_minor >= 0 ? "good" : "bad"}>{signed(p.profit_minor)} USDC</strong>
          </>
        ) : (
          <>
            Paid <strong>{u(p.paid_minor)} USDC</strong> · no value set
          </>
        )}
        {p.accepted > 0 && <> · {p.accepted} accepted, {u(p.cost_per_accepted_minor)} each</>}
        {p.wasted_minor > 0 && <span data-tone="bad"> · {u(p.wasted_minor)} on answers that weren&apos;t usable</span>}
        {p.saved_minor > 0 && <span data-tone="good"> · {u(p.saved_minor)} saved by reusing {p.reused} answer{p.reused === 1 ? "" : "s"}</span>}
      </p>
      {editing ? (
        <div className={styles.edit}>
          <label>
            Worth (USDC)
            <input value={value} onChange={(e) => setValue(e.target.value)} inputMode="decimal" autoFocus />
          </label>
          <button type="button" onClick={() => save()} disabled={busy || (value.trim() !== "" && minor === null)}>
            {busy && <Spinner size={11} />} Save
          </button>
          {p.value_minor !== undefined && (
            <button type="button" onClick={() => save(true)} disabled={busy}>
              Clear
            </button>
          )}
          <button type="button" onClick={() => setEditing(false)} disabled={busy}>
            Cancel
          </button>
        </div>
      ) : (
        <button type="button" className={styles.link} onClick={() => setEditing(true)}>
          {p.value_minor !== undefined ? "Change what it's worth" : "Say what it's worth"}
        </button>
      )}
      {error && <p className={styles.error}>{error}</p>}
    </div>
  );
}

const OUTCOME: Record<string, string> = {
  caught: "Caught",
  held: "Waits for you",
  outranked: "Ranked below a better offer",
  would_pay: "Would be paid",
  falls_back: "Falls back",
  stops: "Stops, nothing paid",
};

const seller = (id: string) => id.replace(/^[a-z]+:/, "");

/** What a replay found: the verdict, then each purchase's scenarios. */
function ReplayResult({ report }: { report: ReplayReport }) {
  const total = report.safe + report.unsafe;
  return (
    <>
      {total === 0 ? (
        <p className={styles.note}>Nothing to test yet: no seller lists a price for the kinds of work agents usually buy on this pass&apos;s networks.</p>
      ) : (
        <p className={styles.verdict} data-tone={report.unsafe === 0 ? "good" : "bad"}>
          {report.unsafe === 0 ? <IconCheck size={13} /> : <IconBan size={13} />}
          {report.unsafe === 0
            ? `${report.safe} of ${report.safe} scenarios safe: ${u(report.protected_minor)} USDC asked by misbehaving sellers, none of it paid.`
            : `${report.unsafe} of ${total} scenarios would pay a misbehaving seller.`}
        </p>
      )}
      {report.purchases.map((p) => (
        <div key={p.intent_id ?? p.capability} className={styles.purchase}>
          <small>
            {report.preset
              ? `${p.title ?? p.capability} · usually ${u(p.paid_minor)} USDC from ${seller(p.provider)}`
              : `${p.capability} · paid ${u(p.paid_minor)} to ${seller(p.provider)}`}
          </small>
          <ul>
            {p.findings.map((f) => (
              <li key={f.scenario} data-safe={f.safe || undefined}>
                <span className={styles.mark} aria-hidden="true">
                  {f.safe ? <IconCheck size={11} /> : <IconBan size={11} />}
                </span>
                <span>
                  <strong>{f.title}</strong>
                  <small>
                    {OUTCOME[f.outcome] ?? f.outcome}: {f.detail}
                  </small>
                </span>
              </li>
            ))}
          </ul>
        </div>
      ))}
      {report.skipped?.map((s) => (
        <p key={s.capability} className={styles.note}>
          Not tested: {s.title}, {s.reason}.
        </p>
      ))}
      <p className={styles.note}>
        {report.preset
          ? "On paper only: nothing was contacted or paid. This pass's rules and Algebra's guards decided each one, asking with the most the pass allows for one purchase."
          : "On paper only: nothing was contacted or paid. The task's own Spend Pass rules and Algebra's guards decided each one."}
      </p>
    </>
  );
}

/** Replay: the task's purchases run again on paper against sellers that misbehave. */
export function ReplayPanel({ jobId, paid }: { jobId: string; paid: boolean }) {
  const [report, setReport] = useState<ReplayReport | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function run() {
    setBusy(true);
    setError(null);
    try {
      setReport(await api.replayJob(jobId));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't replay it");
    } finally {
      setBusy(false);
    }
  }

  if (!paid) return null;
  return (
    <div className={styles.replay}>
      {!report ? (
        <button type="button" className={styles.run} onClick={run} disabled={busy}>
          {busy ? <Spinner size={11} /> : null} Replay under chaos
        </button>
      ) : (
        <ReplayResult report={report} />
      )}
      {error && <p className={styles.error}>{error}</p>}
    </div>
  );
}

/**
 * "Test this pass": the pass's own rules and Algebra's guards against sellers
 * that overcharge, lie, appear from nowhere or go down, before an agent meets
 * one. It works on a pass with no task yet. Nothing is contacted or paid.
 */
export function PassTestPanel({ passId, off }: { passId: string; off: boolean }) {
  const [report, setReport] = useState<ReplayReport | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function run() {
    setBusy(true);
    setError(null);
    try {
      setReport(await api.replayPass(passId));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't test it");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className={styles.passTest} aria-label="Test this pass">
      <div className={styles.passTestHead}>
        <div>
          <h4>Test this pass</h4>
          <p>See what it does when a seller overcharges, lies about its price or goes down. On paper: nothing is paid.</p>
        </div>
        <button type="button" className={styles.run} onClick={run} disabled={busy || off} title={off ? "Turn the pass on to test it" : undefined}>
          {busy ? <Spinner size={11} /> : null} {report ? "Test again" : "Run the test"}
        </button>
      </div>
      {error && (
        <p className={styles.error} role="alert">
          {error}
        </p>
      )}
      {report && (
        <div className={styles.replay} aria-live="polite">
          <ReplayResult report={report} />
        </div>
      )}
    </section>
  );
}
