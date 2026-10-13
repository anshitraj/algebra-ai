import type { ProviderSummary } from "./types";

/** Catalog deployment IDs are identities, but poor browse labels. Keep the original in details. */
export function providerTitle(
  provider: Pick<ProviderSummary, "name" | "host" | "description">,
): string {
  const name = provider.name.trim();
  const deploymentName =
    (name === provider.host && name.length > 48) ||
    /^[0-9a-f]{8}-[0-9a-f-]{27}/i.test(name);
  if (!deploymentName) return name || "Unnamed provider";
  const opening = provider.description
    .trim()
    .split(/\.\s|[\n]|(?=\b(?:GET|POST|HTTP)\b)/)[0]
    .trim()
    .replace(/[.·:]+$/, "");
  return opening && opening.length <= 64 ? opening : "Independent API";
}

export function providerSummary(
  provider: Pick<ProviderSummary, "name" | "host" | "description" | "use_case">,
): string {
  const text = (provider.use_case || provider.description)
    .replace(/\s+/g, " ")
    .trim();
  const title = providerTitle(provider);
  if (title !== provider.name && text.startsWith(title))
    return "Independent provider · catalog-listed API";
  return (
    text.split(
      /(?<=\.)\s+(?=[A-Z])|(?=\b(?:GET this|POST \{|HTTP 402|Pay, retry)\b)/,
    )[0] || "Explore this provider’s APIs and pricing."
  );
}

export function compactHost(host: string): string {
  return host.length > 42 ? `…${host.slice(-34)}` : host;
}

export function safeExternalUrl(url: string | undefined): string | undefined {
  try {
    const parsed = new URL(url || "");
    return /^(https?:)$/.test(parsed.protocol) ? parsed.href : undefined;
  } catch {
    return undefined;
  }
}
