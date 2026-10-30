// The pass a signed-in person practises with. Demo mode is Algebra's
// sandbox: simulated providers and USDC, no wallet and no chain. A real
// account's sandbox pass lives in its own project's sandbox environment, so
// its budget is never mixed with a pass that pays real or test money.

import * as api from "./api-client";
import type { SpendPass } from "./types";

// One lookup at a time per environment: React's strict mode and a second open
// tab would otherwise each make a pass.
const pending = new Map<string, Promise<SpendPass | null>>();

/** The starter pass of each environment: what it is called and what it may spend. */
const STARTERS = {
  // The same limits as the developer quickstart's sandbox pass: 1 USDC a month, at most 0.05 a call, so the
  // chat's examples meet every outcome.
  sandbox: { label: "Demo pass", budget: 1_000_000 },
  // Testnet: as much as the account's test credits (10 USDC), so the pass is never the first thing to run out.
  devnet: { label: "Testnet pass", budget: 10_000_000 },
} as const;

/**
 * The person's active starter pass for an environment, made when they have
 * none: "sandbox" (demo mode) or "devnet" (Testnet). Null when they have no
 * project to put it in (the workspace is made on first read, so that means
 * the API couldn't be asked).
 */
export function ensureStarterPass(environment: keyof typeof STARTERS = "sandbox"): Promise<SpendPass | null> {
  let p = pending.get(environment);
  if (!p) {
    p = find(environment).finally(() => pending.delete(environment));
    pending.set(environment, p);
  }
  return p;
}

/** The demo-mode starter pass (see ensureStarterPass). */
export const ensurePracticePass = () => ensureStarterPass("sandbox");

async function find(environment: keyof typeof STARTERS): Promise<SpendPass | null> {
  const { organizations } = await api.listOrgs();
  const org = organizations[0];
  if (!org) return null;
  const { projects } = await api.listProjects(org.id);
  const project = projects.find((p) => !p.archived_at) ?? projects[0];
  if (!project) return null;

  const { passes } = await api.listProjectPasses(project.id, environment);
  const existing = passes.find((p) => p.active && p.currency === "USDC");
  if (existing) return existing;

  const starter = STARTERS[environment];
  const made = await api.createPass({
    label: starter.label,
    agent_kind: "custom",
    currency: "USDC",
    budget_minor_units: starter.budget,
    budget_period: "month",
    max_per_purchase_minor_units: 50_000,
    allowed_categories: ["digital_services"],
    expires_in_days: 30,
    project_id: project.id,
    environment,
  });
  return made;
}
