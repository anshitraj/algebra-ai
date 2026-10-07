// The classes of work Algebra routes across providers (internal/domain/routing/classes.go),
// as the chat agent needs them: the ID to ask for and the input it takes.
// Kept in step with the Go list; GET /api/v1/classes is the live source.

export type WorkClass = { id: string; title: string; input: string };

export const WORK_CLASSES: WorkClass[] = [
  { id: "token.price", title: "current USD price of a Solana token", input: '{"mint": "<SPL mint address>"}' },
  { id: "solana.token-risk", title: "rug-pull / honeypot / authority risk check of a Solana token", input: '{"mint": "<SPL mint address>"}' },
  { id: "wallet.balances", title: "token balances of a Solana wallet", input: '{"wallet": "<wallet address>"}' },
  { id: "wallet.risk", title: "sanctions / AML / fraud screen of a wallet", input: '{"wallet": "<wallet address>"}' },
  { id: "web.search", title: "ranked web results for a query", input: '{"query": "<text>"}' },
  { id: "web.scrape", title: "readable content of one web page", input: '{"url": "<https URL>"}' },
  { id: "news.search", title: "recent news articles for a query", input: '{"query": "<text>"}' },
  { id: "llm.chat", title: "a language-model answer to a prompt", input: '{"prompt": "<text>"}' },
  { id: "image.generate", title: "an image from a text prompt", input: '{"prompt": "<text>"}' },
  { id: "weather.forecast", title: "weather for a place", input: '{"location": "<city>"}' },
  { id: "text.translate", title: "text translated into another language", input: '{"text": "<text>", "target": "<language code>"}' },
  { id: "ip.geolocate", title: "where an IP address is", input: '{"ip": "<address>"}' },
  { id: "domain.lookup", title: "WHOIS / DNS / availability of a domain", input: '{"domain": "<name>"}' },
  { id: "email.verify", title: "whether an email address accepts mail", input: '{"email": "<address>"}' },
];

export const WORK_CLASS_IDS = WORK_CLASSES.map((c) => c.id);

export function workClassList(): string {
  return WORK_CLASSES.map((c) => `${c.id} (${c.title}; input ${c.input})`).join("; ");
}
