// The pass a signed-in person practises with. Practice mode is Algebra's
// sandbox: simulated providers and USDC, no wallet and no chain. A real
// account's sandbox pass lives in its own project's sandbox environment, so
// its budget is never mixed with a pass that pays real or test money.

import * as api from "./api-client";
import type { SpendPass } from "./types";

// One lookup at a time: React's strict mode and a second open tab would
// otherwise each make a pass.
let pending: Promise<SpendPass | null> | null = null;

/**
 * The person's active practice pass, made when they have none. Null when they
 * have no project to put it in (the workspace is made on first read, so that
 * means the API couldn't be asked).
 */
export function ensurePracticePass(): Promise<SpendPass | null> {
  if (!pending) {
    pending = find().finally(() => {
      pending = null;
    });
  }
  return pending;
}

async function find(): Promise<SpendPass | null> {
  const { organizations } = await api.listOrgs();
  const org = organizations[0];
  if (!org) return null;
  const { projects } = await api.listProjects(org.id);
  const project = projects.find((p) => !p.archived_at) ?? projects[0];
  if (!project) return null;

  const { passes } = await api.listProjectPasses(project.id, "sandbox");
  const existing = passes.find((p) => p.active && p.currency === "USDC");
  if (existing) return existing;

  // The same limits as the developer quickstart's sandbox pass: 1 USDC a month,
  // at most 0.05 a call, so the chat's examples meet every outcome.
  const made = await api.createPass({
    label: "Practice pass",
    agent_kind: "custom",
    currency: "USDC",
    budget_minor_units: 1_000_000,
    budget_period: "month",
    max_per_purchase_minor_units: 50_000,
    allowed_categories: ["digital_services"],
    expires_in_days: 30,
    project_id: project.id,
    environment: "sandbox",
  });
  return made;
}
