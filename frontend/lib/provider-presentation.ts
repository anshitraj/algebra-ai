import type { ProviderSummary } from "./types";

const UUID = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i;
const HOST_LIKE = /^(?:[a-z0-9-]+\.)+[a-z]{2,}$/i;

// Where anyone can deploy: the subdomain is the app's name, the rest is the platform's.
const HOSTING_SUFFIXES = [
  "vercel.app", "netlify.app", "pages.dev", "workers.dev", "github.io", "replit.app", "replit.dev",
  "onrender.com", "up.railway.app", "railway.app", "fly.dev", "herokuapp.com", "a.run.app", "run.app",
  "deno.dev", "web.app", "firebaseapp.com", "azurewebsites.net", "amplifyapp.com", "lovable.app",
];
// Tunnels hand out random words, not names.
const TUNNEL_SUFFIXES = ["ngrok-free.app", "ngrok-free.dev", "ngrok.app", "ngrok.io", "trycloudflare.com", "loca.lt"];
const GENERIC_LABELS = new Set(["www", "api", "apis", "app", "x402", "402", "pay", "payai", "mainnet", "devnet", "prod", "production", "staging", "preview", "dev", "v1", "v2", "sol", "solana"]);

const endsWith = (host: string, suffix: string) => host === suffix || host.endsWith(`.${suffix}`);

/**
 * A readable name from a host, for providers a catalog only knows by where
 * they answer: "api.bentcrypto.com" → "Bentcrypto", "blockrun-sol-vbsbhh7lea-uc.a.run.app"
 * → "Blockrun Sol". Deployment hashes, UUIDs and tunnels give "" (no name to show).
 */
export function nameFromHost(host: string): string {
  const h = host.toLowerCase().replace(/^https?:\/\//, "").replace(/[/:].*$/, "");
  if (!HOST_LIKE.test(h) || TUNNEL_SUFFIXES.some((s) => endsWith(h, s))) return "";
  let label: string;
  const hosting = HOSTING_SUFFIXES.find((s) => endsWith(h, s));
  if (hosting) {
    label = h.slice(0, -hosting.length - 1).split(".")[0] ?? "";
  } else {
    const parts = h.split(".");
    // "example.co.uk": the brand sits before the two-part suffix.
    const twoPart = parts.length > 2 && parts.at(-1)!.length === 2 && ["co", "com", "org", "net", "ac", "gov"].includes(parts.at(-2)!);
    label = parts.at(twoPart ? -3 : -2) ?? "";
  }
  // A UUID for a project name: the rest is a deployment hash and an account.
  if (UUID.test(label)) return "";
  const words: string[] = [];
  for (const [i, w] of label.split(/[-_]+/).filter(Boolean).entries()) {
    // A deployment hash ("1aw0hwejp", "vbsbhh7lea") or a preview branch ends the app's own name.
    if (i > 0 && (w === "git" || (w.length >= 7 && /[a-z]\d+[a-z]/.test(w)))) break;
    if (!GENERIC_LABELS.has(w)) words.push(w);
  }
  return words.map((w) => w[0].toUpperCase() + w.slice(1)).join(" ");
}

function openingLine(description: string): string {
  const opening = description
    .trim()
    .split(/\.\s|[\n]|(?=\b(?:GET|POST|HTTP)\b)/)[0]
    .trim()
    .replace(/[.·:]+$/, "");
  return opening.length <= 64 ? opening : "";
}

/**
 * Catalog deployment IDs and hosts are identities, but poor browse labels:
 * a host becomes a readable name, and a name with nothing readable in it
 * becomes the opening of its description. Keep the original in details.
 */
export function providerTitle(
  provider: Pick<ProviderSummary, "name" | "host" | "description">,
): string {
  const name = provider.name.trim();
  const hostName = HOST_LIKE.test(name);
  if (name && !hostName && !/^[0-9a-f]{8}-[0-9a-f-]{27}/i.test(name)) return name;
  return nameFromHost(hostName ? name : provider.host) || openingLine(provider.description) || (name ? "Independent API" : "Unnamed provider");
}

export function providerSummary(
  provider: Pick<ProviderSummary, "name" | "host" | "description" | "use_case">,
): string {
  const text = (provider.use_case || provider.description)
    .replace(/\s+/g, " ")
    .trim();
  const title = providerTitle(provider);
  // The title already is the description's opening: don't say it twice.
  if (title !== provider.name && title === openingLine(provider.description) && text.startsWith(title))
    return "Independent provider · catalog-listed API";
  return (
    text.split(
      /(?<=\.)\s+(?=[A-Z])|(?=\b(?:GET this|POST \{|HTTP 402|Pay, retry)\b)/,
    )[0] || "Explore this provider’s APIs and pricing."
  );
}

/** A host short enough for a line: "01a046b2-…-xai-org.vercel.app" → "…-1aw0hwejp-xai-org.vercel.app". */
export function shortHost(host: string): string {
  return compactHost(host.replace(UUID, "…"));
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
