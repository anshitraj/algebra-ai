// Server-only model catalog for the agent's model picker. A provider shows
// up only when its API key is set; its model list comes from
// <PROVIDER>_MODELS (comma-separated) or the defaults below. The first model
// in a list is that provider's default.

import * as anthropicProvider from "./providers/anthropic";
import * as openaiProvider from "./providers/openai";
import * as geminiProvider from "./providers/gemini";
import type { ProviderTurnInput, ProviderTurnResult } from "./providers/types";

export type AgentProviderId = "anthropic" | "openai" | "gemini";

type ProviderConfig = {
  label: string;
  envKey: string;
  modelsEnvKey: string;
  legacyModelEnvKey: string;
  defaults: { id: string; label: string }[];
  run: (input: ProviderTurnInput) => Promise<ProviderTurnResult>;
};

const PROVIDERS: Record<AgentProviderId, ProviderConfig> = {
  anthropic: {
    label: "Anthropic",
    envKey: "ANTHROPIC_API_KEY",
    modelsEnvKey: "ANTHROPIC_MODELS",
    legacyModelEnvKey: "ANTHROPIC_MODEL",
    defaults: [
      { id: "claude-sonnet-5", label: "Claude Sonnet 5" },
      { id: "claude-opus-5-5", label: "Claude Opus 5.5" },
      { id: "claude-haiku-4-5-20251001", label: "Claude Haiku 4.5" },
    ],
    run: anthropicProvider.runTurn,
  },
  openai: {
    label: "OpenAI",
    envKey: "OPENAI_API_KEY",
    modelsEnvKey: "OPENAI_MODELS",
    legacyModelEnvKey: "OPENAI_MODEL",
    defaults: [
      { id: "gpt-4o", label: "GPT-4o" },
      { id: "gpt-4o-mini", label: "GPT-4o mini" },
    ],
    run: openaiProvider.runTurn,
  },
  gemini: {
    label: "Google",
    envKey: "GEMINI_API_KEY",
    modelsEnvKey: "GEMINI_MODELS",
    legacyModelEnvKey: "GEMINI_MODEL",
    defaults: [
      { id: "gemini-flash-latest", label: "Gemini Flash" },
      { id: "gemini-pro-latest", label: "Gemini Pro" },
    ],
    run: geminiProvider.runTurn,
  },
};

export const PROVIDER_ORDER: AgentProviderId[] = ["anthropic", "openai", "gemini"];

function prettify(id: string) {
  return id
    .replace(/-(\d{8})$/, "")
    .split(/[-_]/)
    .map((w) => (/^\d/.test(w) ? w : w.charAt(0).toUpperCase() + w.slice(1)))
    .join(" ");
}

export function modelsFor(id: AgentProviderId): { id: string; label: string }[] {
  const cfg = PROVIDERS[id];
  const fromEnv = (process.env[cfg.modelsEnvKey] ?? "")
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
  const list = fromEnv.length
    ? fromEnv.map((m) => cfg.defaults.find((d) => d.id === m) ?? { id: m, label: prettify(m) })
    : [...cfg.defaults];
  // Honor the older single-model override by putting it first.
  const legacy = process.env[cfg.legacyModelEnvKey]?.trim();
  if (legacy && !fromEnv.length) {
    const existing = list.findIndex((m) => m.id === legacy);
    const entry = existing >= 0 ? list.splice(existing, 1)[0] : { id: legacy, label: prettify(legacy) };
    list.unshift(entry);
  }
  return list;
}

export function providerCatalog() {
  return PROVIDER_ORDER.map((id) => ({
    id,
    label: PROVIDERS[id].label,
    available: Boolean(process.env[PROVIDERS[id].envKey]),
    models: modelsFor(id),
  }));
}

export function resolveProvider(provider: string | undefined, model: string | undefined) {
  const available = PROVIDER_ORDER.filter((id) => process.env[PROVIDERS[id].envKey]);
  const id = (provider && available.includes(provider as AgentProviderId) ? provider : available[0]) as AgentProviderId | undefined;
  if (!id) return null;
  const models = modelsFor(id);
  const chosen = models.find((m) => m.id === model) ?? models[0];
  return { id, model: chosen.id, apiKey: process.env[PROVIDERS[id].envKey] as string, run: PROVIDERS[id].run };
}
