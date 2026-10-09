import Link from "next/link";
import { Logo } from "./logo";
import { Container } from "./container";

const columns = [
  {
    heading: "Product",
    links: [
      { label: "Sign in", href: "/login" },
      { label: "Create account", href: "/signup" },
      { label: "Console", href: "/console" },
      { label: "How it works", href: "/#how-it-works" },
      { label: "Providers", href: "/#providers" },
      { label: "Policy", href: "/#policy" },
      { label: "Security", href: "/#security" },
      { label: "Integrate", href: "/#integrate" },
    ],
  },
  {
    heading: "Docs",
    links: [
      { label: "Execution and catalogs", href: "https://github.com/anshitraj/algebra-ai/blob/main/docs/EXECUTION.md" },
      { label: "Economic coordination", href: "https://github.com/anshitraj/algebra-ai/blob/main/docs/ECONOMIC_COORDINATION.md" },
      { label: "Threat model", href: "https://github.com/anshitraj/algebra-ai/blob/main/docs/THREAT_MODEL.md" },
      { label: "MCP tools", href: "https://github.com/anshitraj/algebra-ai/blob/main/docs/MCP.md" },
      { label: "Local development", href: "https://github.com/anshitraj/algebra-ai/blob/main/docs/LOCAL_DEVELOPMENT.md" },
    ],
  },
  {
    heading: "Project",
    links: [
      { label: "GitHub", href: "https://github.com/anshitraj/algebra-ai" },
      { label: "Pay.sh", href: "https://pay.sh" },
      { label: "Circle Agent Marketplace", href: "https://agents.circle.com" },
    ],
  },
  {
    heading: "Legal",
    links: [
      { label: "Terms", href: "/terms" },
      { label: "Privacy", href: "/privacy" },
      { label: "Contact", href: "/contact" },
    ],
  },
];

export function Footer() {
  return (
    <footer className="border-t border-border py-16">
      <Container>
        <div className="grid gap-12 md:grid-cols-[1fr_2fr]">
          <div>
            <Link href="/" className="flex items-center gap-2.5 text-foreground">
              <Logo size={22} />
              <span className="font-display text-base font-semibold tracking-tight">
                Algebra
              </span>
            </Link>
            <p className="mt-4 max-w-[26ch] text-sm leading-relaxed text-muted">
              The policy-aware execution layer for autonomous agents on Solana.
            </p>
          </div>

          <div className="grid grid-cols-2 gap-8 sm:grid-cols-4">
            {columns.map((col) => (
              <div key={col.heading}>
                <h3 className="text-xs font-medium text-muted">{col.heading}</h3>
                <ul className="mt-3.5 space-y-2.5">
                  {col.links.map((l) => (
                    <li key={l.label}>
                      <a
                        href={l.href}
                        className="text-sm text-foreground transition-colors hover:text-primary"
                      >
                        {l.label}
                      </a>
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </div>
        </div>

        <div className="mt-16 flex flex-col gap-3 border-t border-border pt-8 text-xs text-muted sm:flex-row sm:items-center sm:justify-between">
          <p>
            Agents never hold a key: Algebra pays from a wallet it controls. Provider names belong to their owners; Algebra isn&apos;t affiliated
            with them, with Pay.sh or with Circle.
          </p>
          <p>Apache-2.0 licensed.</p>
        </div>
      </Container>
    </footer>
  );
}
