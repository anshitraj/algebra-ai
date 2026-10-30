"use client";

import { useEffect, useState } from "react";
import * as api from "@/lib/api-client";
import type { AgentPnL, JobAttempt, JobPass } from "@/lib/types";
import { usdcNumber } from "@/lib/money";
import { explorerTx } from "@/lib/explorer";
import { IconChevronDown, IconExternal, Spinner } from "@/components/icons";
import styles from "./pass-tasks.module.css";
import { AgentPnLSummary, ReplayPanel, TaskPnLLine } from "./task-pnl";

const STATE: Record<JobPass["state"], string> = { ACTIVE: "Running", CLOSED: "Done", REVOKED: "Stopped", EXPIRED: "Timed out" };

function ago(iso: string) {
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  return new Date(iso).toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

/** What an attempt came to, in a word. */
function attemptState(a: JobAttempt) {
  if (a.state === "COMMITTED") return a.fulfillment === "NOT_FULFILLED" ? "Paid, not usable" : "Paid";
  if (a.state === "RELEASED" || a.state === "EXPIRED") return "Not paid";
  return "Settling";
}

const provider = (id: string) => id.replace(/^(paysh|circle|payai|x402|demo|sandbox):/, "");

/**
 * The agent's recent tasks: each a Job Pass, a budget set aside from this
 * Spend Pass for one job and shared by every sub-agent on it, with what it
 * paid and what went back. Shown inside the pass, not as a section of its own.
 */
export function PassTasks({ passId, heldMinor = 0, onChanged }: { passId: string; heldMinor?: number; onChanged?: () => void }) {
  const [jobs, setJobs] = useState<JobPass[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [open, setOpen] = useState<string | null>(null);
  const [stopping, setStopping] = useState<string | null>(null);
  const [pnl, setPnl] = useState<AgentPnL | null>(null);

  useEffect(() => {
    let gone = false;
    api
      .getPassPnL(passId, 30)
      .then((p) => {
        if (!gone) setPnl(p);
      })
      .catch(() => {});
    api
      .listJobs(passId, 6)
      .then((r) => {
        if (!gone) setJobs(r.jobs);
      })
      .catch((e) => {
        if (!gone) setError(e instanceof Error ? e.message : "Couldn't load the tasks");
      });
    return () => {
      gone = true;
    };
  }, [passId]);

  async function stop(j: JobPass) {
    setStopping(j.id);
    try {
      const v = await api.revokeJob(j.id);
      setJobs((all) => (all ?? []).map((x) => (x.id === v.id ? v : x)));
      onChanged?.();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't stop it");
    } finally {
      setStopping(null);
    }
  }


  return (
    <section className={styles.tasks} aria-label="Recent tasks">
      <div className={styles.head}>
        <h4>Recent tasks</h4>
        {heldMinor > 0 && <span>{usdcNumber(heldMinor)} USDC held by running tasks</span>}
      </div>
      {pnl && <AgentPnLSummary pnl={pnl} />}
      {error && <p className={styles.note}>{error}</p>}
      {jobs === null && !error && (
        <p className={styles.note}>
          <Spinner size={12} /> Loading
        </p>
      )}
      {jobs?.length === 0 && <p className={styles.note}>Each task an agent starts sets its budget aside here first, then shows what it paid and what came back.</p>}
      {jobs && jobs.length > 0 && (
        <ul className={styles.list}>
          {jobs.map((j) => (
            <Task
              key={j.id}
              job={j}
              open={open === j.id}
              onToggle={() => setOpen(open === j.id ? null : j.id)}
              onStop={() => stop(j)}
              stopping={stopping === j.id}
              onChanged={(v) => {
                setJobs((all) => (all ?? []).map((x) => (x.id === v.id ? v : x)));
                // A task's value changes the agent's P&L too.
                api.getPassPnL(passId, 30).then(setPnl, () => {});
              }}
            />
          ))}
        </ul>
      )}
    </section>
  );
}

function Task({ job: j, open, onToggle, onStop, stopping, onChanged }: { job: JobPass; open: boolean; onToggle: () => void; onStop: () => void; stopping: boolean; onChanged: (j: JobPass) => void }) {
  const budget = Math.max(j.budget_minor, 1);
  const used = (j.paid_minor + j.held_minor + j.set_aside_minor) / budget;
  const attempts = j.attempts.filter((a) => a.state !== "RELEASED" && a.state !== "EXPIRED");
  return (
    <li className={styles.task} data-live={j.live || undefined}>
      <button type="button" className={styles.row} onClick={onToggle} aria-expanded={open}>
        <span className={styles.label}>
          <strong>{j.label}</strong>
          <small>
            {STATE[j.state]} · {ago(j.created_at)}
          </small>
        </span>
        <span className={styles.amount}>
          {usdcNumber(j.paid_minor)} <small>of {usdcNumber(j.budget_minor)}</small>
        </span>
        <IconChevronDown size={14} className={open ? styles.flip : undefined} />
      </button>
      <div className={styles.meter} aria-hidden="true">
        <span style={{ transform: `scaleX(${Math.min(1, used)})` }} />
      </div>
      {open && (
        <div className={styles.detail}>
          <p className={styles.sum}>
            {j.live ? (
              <>
                {usdcNumber(j.left_minor)} USDC left of {usdcNumber(j.budget_minor)} · ends {new Date(j.expires_at).toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" })}
              </>
            ) : (
              <>
                Paid {usdcNumber(j.paid_minor)} USDC{j.held_minor > 0 ? `, ${usdcNumber(j.held_minor)} still settling` : ""} · {usdcNumber(j.returned_minor)} went back to the pass
              </>
            )}
          </p>
          <TaskPnLLine job={j} onChanged={onChanged} />
          {attempts.length > 0 ? (
            <ul className={styles.attempts}>
              {attempts.map((a) => (
                <li key={a.reservation_id}>
                  <span>
                    {provider(a.provider_id)} <small>{a.capability}</small>
                  </span>
                  <span className={styles.attemptAmount}>
                    {usdcNumber(a.hold_minor)} <small>{attemptState(a)}</small>
                  </span>
                  {a.transaction && (
                    <a href={explorerTx(a.transaction, a.network)} target="_blank" rel="noopener noreferrer" aria-label="Transaction">
                      <IconExternal size={11} />
                    </a>
                  )}
                </li>
              ))}
            </ul>
          ) : j.children.length === 0 ? (
            <p className={styles.note}>Nothing paid under this task.</p>
          ) : null}
          {j.children.length > 0 && (
            <ul className={styles.children} aria-label="Sub-tasks">
              {j.children.map((c) => (
                <li key={c.id}>
                  <span>
                    {c.label} <small>{STATE[c.state]}</small>
                  </span>
                  <span className={styles.attemptAmount}>
                    {usdcNumber(c.paid_minor)} <small>of {usdcNumber(c.budget_minor)}</small>
                  </span>
                </li>
              ))}
            </ul>
          )}
          {!j.live && <ReplayPanel jobId={j.id} paid={j.pnl.paid_minor > 0} />}
          {j.live && (
            <button type="button" className={styles.stop} onClick={onStop} disabled={stopping}>
              {stopping && <Spinner size={11} />} Stop this task
            </button>
          )}
        </div>
      )}
    </li>
  );
}
