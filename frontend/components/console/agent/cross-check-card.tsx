"use client";

import type { CrossCheckInfo } from "@/lib/agent/cross-check";
import { IconCheck, IconInfo } from "@/components/icons";
import styles from "./cross-check-card.module.css";

/** A provider as a short name: "sandbox:alpha" reads "alpha". */
function shortName(id: string) {
  const name = id.replace(/^(paysh|circle|payai|cdp|monid|sandbox|demo|keyed):/, "");
  return name.length > 28 ? `${name.slice(0, 27)}…` : name;
}

const MAX_ROWS = 5;

/**
 * Whether the providers paid for the same question agreed. Agreement is the quiet
 * case (one line); a disagreement lists the fields and what each said; "unclear"
 * says nothing could be compared and is never dressed as agreement. The values
 * are the providers' own words, shown as text.
 */
export function CrossCheckCard({ check }: { check: CrossCheckInfo }) {
  const r = check.report;
  const names = r.providers.map(shortName);
  const headline =
    r.verdict === "agree"
      ? `${r.providers.length} providers agree`
      : r.verdict === "differ"
        ? "The providers differ"
        : "Couldn't compare the answers";
  const rows = (r.differences ?? []).slice(0, MAX_ROWS);
  const hidden = (r.differences?.length ?? 0) - rows.length + (r.more_differences ?? 0);
  return (
    <section className={styles.check} data-verdict={r.verdict} aria-label="Cross-check of the answers">
      <div className={styles.head}>
        <span className={styles.mark} aria-hidden="true">
          {r.verdict === "agree" ? <IconCheck size={13} strokeWidth={3} /> : <IconInfo size={13} />}
        </span>
        <p className={styles.title}>
          <strong>{headline}</strong>
          <span className={styles.who}>{names.join(" · ")}</span>
        </p>
      </div>
      <p className={styles.summary}>{r.summary}</p>
      {r.verdict === "agree" && r.compared > 0 && (
        <p className={styles.fine}>
          {r.agreed} field{r.agreed === 1 ? "" : "s"} compared · numbers within {r.tolerance_pct}%
        </p>
      )}
      {rows.length > 0 && (
        <div className={styles.scroll}>
          <table className={styles.table}>
            <thead>
              <tr>
                <th scope="col">Field</th>
                {r.providers.map((p) => (
                  <th key={p} scope="col" title={p}>
                    {shortName(p)}
                  </th>
                ))}
                <th scope="col">Apart</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((d) => (
                <tr key={d.path}>
                  <th scope="row">{d.path}</th>
                  {r.providers.map((p) => (
                    <td key={p} className={styles.value} data-odd={d.outlier === p || undefined}>
                      {d.values[p] ?? "—"}
                    </td>
                  ))}
                  <td className={styles.apart}>{typeof d.spread_pct === "number" ? `${d.spread_pct}%` : ""}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {hidden > 0 && <p className={styles.fine}>and {hidden} more</p>}
        </div>
      )}
    </section>
  );
}
