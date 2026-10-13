import type { SpendPass } from "@/lib/types";
import { formatMoney } from "@/lib/money";
import { IconShield } from "@/components/icons";
import styles from "./spend-pass-credential.module.css";

type Credential = Pick<SpendPass, "label" | "agent_kind" | "currency" | "budget_minor_units" | "budget_period" | "max_per_purchase_minor_units" | "approve_above_minor_units" | "allowed_merchants" | "remaining_minor_units" | "spent_minor_units"> & Partial<Pick<SpendPass, "id" | "expires_at" | "revoked_at" | "active" | "frozen_at">>;

export function passStatus(p: Credential) {
  return p.revoked_at ? "Revoked" : p.frozen_at ? "Frozen" : p.active === false ? "Expired" : "Active";
}

export function SpendPassCredential({ pass: p, demo = false, preview = false, validForDays }: { pass: Credential; demo?: boolean; preview?: boolean; validForDays?: number }) {
  const agent = p.agent_kind === "claude" ? "Claude" : p.agent_kind === "chatgpt" ? "ChatGPT" : "Custom agent";
  const status = preview ? "Preview" : passStatus(p);
  const money = (n: number) => Number.isFinite(n) ? formatMoney(n, p.currency) : "—";
  const used = p.budget_minor_units > 0 ? Math.max(0, Math.min(100, p.spent_minor_units / p.budget_minor_units * 100)) : 0;
  const approval = p.approve_above_minor_units == null ? "Not required" : p.approve_above_minor_units === 0 ? "Every call" : `Above ${money(p.approve_above_minor_units)}`;
  return <div className={`${styles.card} console-inverse`} aria-label={`${p.label}, ${demo ? "demo " : ""}spend pass, ${status.toLowerCase()}`}>
    <div className={styles.watermark} aria-hidden="true">a</div>
    <div className={styles.topline}>
      <span className={styles.brand}><span className={styles.brandMark} aria-hidden="true">a</span> algebra <span className={styles.edition}>/ SPEND PASS</span></span>
      <span className={styles.status} data-active={status === "Active"}><span aria-hidden="true" />{status}</span>
    </div>
    <div className={styles.identity}>
      <div className={styles.avatar} aria-hidden="true"><IconShield size={28} /><span>{p.agent_kind === "custom" ? "AG" : agent.slice(0, 2).toUpperCase()}</span></div>
      <div className={styles.identityText}>
        <p className={styles.eyebrow}>{demo ? "DEMO · SIMULATED USDC" : "AGENT SPENDING CREDENTIAL"}</p>
        <h3 className={styles.title}>{p.label || "Untitled pass"}</h3>
        <p className={styles.agent}>{agent} <span>· {preview ? "Not issued yet" : "Issued by you"}</span></p>
      </div>
    </div>
    <div className={styles.budget}>
      <div>
        <p className={styles.eyebrow}>{preview ? "AUTHORIZED BUDGET" : "REMAINING BUDGET"}</p>
        <p className={styles.amount}>{money(preview ? p.budget_minor_units : p.remaining_minor_units)}</p>
      </div>
      <div className={styles.period}><span>{money(p.budget_minor_units)}</span><span>{p.budget_period === "week" ? "per week" : p.budget_period === "month" ? "per month" : "total allowance"}</span></div>
    </div>
    <div className={styles.progress} role="progressbar" aria-label="Budget spent" aria-valuenow={Math.round(used)} aria-valuemin={0} aria-valuemax={100}><span style={{ width: `${used}%` }} /></div>
    <p className={styles.spent}>{money(p.spent_minor_units)} spent {p.budget_period === "week" ? "this week" : p.budget_period === "month" ? "this month" : "in total"}</p>
    <dl className={styles.limits}>
      <div><dt>PER CALL</dt><dd>{p.max_per_purchase_minor_units ? money(p.max_per_purchase_minor_units) : "Within budget"}</dd></div>
      <div><dt>YOUR APPROVAL</dt><dd>{approval}</dd></div>
      <div><dt>PROVIDERS</dt><dd>{p.allowed_merchants?.length ? `${p.allowed_merchants.length} approved` : "Any provider"}</dd></div>
    </dl>
    <div className={styles.footer}>
      <span title={p.id}>{preview ? "SERIAL / NOT ISSUED" : `ID / ${p.id}`}</span>
      <span>{validForDays ? `Valid for ${validForDays} days` : p.expires_at ? `Expires ${new Date(p.expires_at).toLocaleDateString("en-GB", { day: "2-digit", month: "short", year: "numeric" })}` : ""}</span>
    </div>
  </div>;
}
