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
        <>
          <p className={styles.verdict} data-tone={report.unsafe === 0 ? "good" : "bad"}>
            {report.unsafe === 0 ? <IconCheck size={13} /> : <IconBan size={13} />}
            {report.unsafe === 0
              ? `${report.safe} of ${report.safe} scenarios safe: ${u(report.protected_minor)} USDC asked by misbehaving sellers, none of it paid.`
              : `${report.unsafe} of ${report.safe + report.unsafe} scenarios would pay a misbehaving seller.`}
          </p>
          {report.purchases.map((p) => (
            <div key={p.intent_id} className={styles.purchase}>
              <small>
                {p.capability} · paid {u(p.paid_minor)} to {p.provider.replace(/^[a-z]+:/, "")}
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
          <p className={styles.note}>On paper only: nothing was contacted or paid. The task&apos;s own Spend Pass rules and Algebra&apos;s guards decided each one.</p>
        </>
      )}
      {error && <p className={styles.error}>{error}</p>}
    </div>
  );
}
