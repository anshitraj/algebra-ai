import type { ProviderSummary } from "./types";

// What the landing page shows before the live catalogs arrive, or when the API
// can't be reached: a snapshot of providers that Pay.sh and Circle's Agent
// Marketplace listed in early October 2026 as payable in USDC on Solana, with
// the prices they listed, in micro-USDC. The console and the landing table read
// the live catalogs (GET /api/v1/providers); this is not a promise of what is
// available today, and a listed price is not a quote.

type Featured = Pick<
  ProviderSummary,
  "id" | "name" | "description" | "category" | "host" | "endpoint_count" | "min_price_minor" | "max_price_minor" | "source"
>;

export const FEATURED_PROVIDERS: Featured[] = [
  {
    id: "paysh:solana-foundation.google.vision",
    name: "Cloud Vision API",
    description: "Google Cloud image analysis: labels, text, faces, logos and safe-search.",
    category: "ai_ml",
    host: "vision.google.gateway-402.com",
    endpoint_count: 2,
    min_price_minor: 1_500,
    max_price_minor: 1_500,
    source: "pay.sh",
  },
  {
    id: "paysh:solana-foundation.google.generativelanguage",
    name: "Generative Language API (Gemini)",
    description: "Google Gemini models for text, multimodal understanding, chat and embeddings.",
    category: "ai_ml",
    host: "generativelanguage.google.gateway-402.com",
    endpoint_count: 11,
    min_price_minor: 0,
    max_price_minor: 138_000_000,
    source: "pay.sh",
  },
  {
    id: "circle:birdeye",
    name: "Birdeye",
    description: "DeFi market data, token analytics and wallet intelligence across Solana and 15+ chains.",
    category: "finance",
    host: "public-api.birdeye.so",
    endpoint_count: 46,
    min_price_minor: 3_000,
    max_price_minor: 3_000,
    source: "circle",
  },
  {
    id: "circle:allium",
    name: "Allium",
    description: "Blockchain data: token prices, history and statistics across chains.",
    category: "finance",
    host: "agents.allium.so",
    endpoint_count: 12,
    min_price_minor: 10_000,
    max_price_minor: 30_000,
    source: "circle",
  },
  {
    id: "circle:messari",
    name: "Messari",
    description: "Crypto research, asset metrics and market intelligence.",
    category: "finance",
    host: "api.messari.io",
    endpoint_count: 8,
    min_price_minor: 100_000,
    max_price_minor: 550_000,
    source: "circle",
  },
  {
    id: "paysh:nansen-ai.nansen-api",
    name: "Nansen API",
    description: "On-chain analytics: smart-money flows, token metrics and wallet profiling.",
    category: "finance",
    host: "api.nansen.ai",
    endpoint_count: 60,
    min_price_minor: 10_000,
    max_price_minor: 7_500_000,
    source: "pay.sh",
  },
  {
    id: "circle:vybe-network",
    name: "Vybe Network",
    description: "Solana token prices, top holders, wallet PnL, DEX trades and oracles.",
    category: "finance",
    host: "x402-api.vybenetwork.xyz",
    endpoint_count: 43,
    min_price_minor: 12_000,
    max_price_minor: 30_000,
    source: "circle",
  },
  {
    id: "paysh:quicknode.rpc",
    name: "Quicknode",
    description: "Pay-per-request JSON-RPC for 140+ blockchain networks.",
    category: "compute",
    host: "x402.quicknode.com",
    endpoint_count: 137,
    min_price_minor: 1_000,
    max_price_minor: 1_000_000,
    source: "pay.sh",
  },
  {
    id: "circle:exa",
    name: "Exa",
    description: "AI web search and content extraction for agent retrieval.",
    category: "search",
    host: "api.exa.ai",
    endpoint_count: 2,
    min_price_minor: 1_000,
    max_price_minor: 7_000,
    source: "circle",
  },
  {
    id: "paysh:agentmail.email",
    name: "AgentMail",
    description: "Dedicated email inboxes for AI agents: create, send, receive.",
    category: "messaging",
    host: "x402.api.agentmail.to",
    endpoint_count: 51,
    min_price_minor: 0,
    max_price_minor: 10_000_000,
    source: "pay.sh",
  },
];

/** The catalogs Algebra reads, and where a person can browse each. */
export const CATALOGS: { id: string; name: string; url: string; blurb: string }[] = [
  {
    id: "pay.sh",
    name: "Pay.sh",
    url: "https://pay.sh",
    blurb: "The Solana Foundation's and Google Cloud's gateway of pay-per-call APIs.",
  },
  {
    id: "circle",
    name: "Circle Agent Marketplace",
    url: "https://agents.circle.com/services",
    blurb: "Circle's catalog of x402 services, with the payment terms each one publishes.",
  },
  {
    id: "payai",
    name: "PayAI",
    url: "https://payai.network",
    blurb: "The open bazaar of x402 services that settle through PayAI's facilitator on Solana.",
  },
];

export function catalogName(source: string): string {
  return CATALOGS.find((c) => c.id === source)?.name ?? source;
}

/** "A", "A and B", "A, B and C". */
export function joinNames(names: string[]): string {
  return names.length < 2 ? (names[0] ?? "") : `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;
}

const CATEGORY_LABELS: Record<string, string> = {
  ai_ml: "AI & ML",
  finance: "Finance & markets",
  data: "Data",
  search: "Search",
  maps: "Maps & places",
  translation: "Translation",
  messaging: "Messaging",
  media: "Media & creative",
  compute: "Compute & RPC",
  infrastructure: "Infrastructure",
  social: "Social",
  prediction_markets: "Prediction markets",
  security: "Security",
  identity: "Identity",
  productivity: "Productivity",
  shopping: "Shopping",
  devtools: "Developer tools",
  other: "Other",
};

/** "ai_ml" is for machines; people read "AI & ML". Unknown categories are shown as written. */
export function categoryLabel(name: string): string {
  return CATEGORY_LABELS[name] ?? name.replace(/[_-]+/g, " ").replace(/^./, (c) => c.toUpperCase());
}
