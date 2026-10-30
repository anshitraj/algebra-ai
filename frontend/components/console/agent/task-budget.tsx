"use client";

import Link from "next/link";
import type { JobPass } from "@/lib/types";
import { usdcNumber } from "@/lib/money";
import { IconCheck, IconClock } from "@/components/icons";
import styles from "./task-budget.module.css";

/** What a turn knows about its Job Pass, from the chat's job events. */
export type TaskJob =
  | { phase: "opened" | "closed"; job: JobPass; pass: string }
  | { phase: "short"; leftMinor: number; wantedMinor: number; pass: string };

const usdc = (minor: number) => usdcNumber(minor);

/**
 * The task's budget: the most the chosen cards could cost, set aside from the
 * Spend Pass before anything was paid, and, once the task ends, what it paid
 * and what went back. An allocation in Algebra's ledger, never separate money.
 */
export function TaskBudget({ task }: { task: TaskJob }) {
  if (task.phase === "short") {
    return (
      <section className={styles.task} data-phase="short" aria-label="Task budget">
        <p className={styles.line}>
          <strong>Not started.</strong> {task.pass} has {usdc(task.leftMinor)} USDC left, and these cards could cost up to {usdc(task.wantedMinor)} USDC
          together. Nothing was paid.
        </p>
        <Link className={styles.fix} href="/console/firewall">
          Raise the budget
        </Link>
      </section>
    );
  }
  const j = task.job;
  const open = task.phase === "opened";
  const budget = Math.max(j.budget_minor, 1);
  const paid = j.paid_minor / budget;
  const held = j.held_minor / budget;
  return (
    <section className={styles.task} data-phase={task.phase} aria-label="Task budget">
      <div className={styles.head}>
        <span className={styles.mark} aria-hidden="true">
          {open ? <IconClock size={13} /> : <IconCheck size={13} />}
        </span>
        <p className={styles.line}>
          {open ? (
            <>
              <strong>{usdc(j.budget_minor)} USDC</strong> set aside from {task.pass} for this task
            </>
          ) : (
            <>
              Task paid <strong>{usdc(j.paid_minor)} USDC</strong>
              {j.held_minor > 0 ? <> · {usdc(j.held_minor)} still settling</> : null} · {usdc(j.returned_minor)} back to {task.pass}
            </>
          )}
        </p>
      </div>
      <div
        className={styles.bar}
        role="meter"
        aria-label="Task budget used"
        aria-valuemin={0}
        aria-valuemax={j.budget_minor}
        aria-valuenow={j.paid_minor + j.held_minor}
      >
        <span className={styles.paid} style={{ transform: `scaleX(${open ? 1 : paid})` }} data-open={open || undefined} />
        {!open && held > 0 && <span className={styles.held} style={{ left: `${paid * 100}%`, transform: `scaleX(${held})` }} />}
      </div>
    </section>
  );
}
