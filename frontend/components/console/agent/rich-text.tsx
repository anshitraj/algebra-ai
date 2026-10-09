// Minimal, safe renderer for agent replies: paragraphs, bullet/numbered
// lists (even mid-paragraph), **bold**, `code`, [markdown](links) and bare
// https links. No HTML is ever injected, and only http(s) URLs become links.

const TOKEN = /(\[[^\]\n]+\]\([^)\s]+\)|\*\*[^*\n]+\*\*|`[^`\n]+`|https?:\/\/[^\s)<>]+)/g;
const LIST_LINE = /^\s*([-*•]|\d+[.)])\s+/;

/** Returns a safe absolute http(s) URL, or null. Bare domains get https://. */
function safeHref(raw: string): string | null {
  let url = raw.trim();
  if (!/^[a-z][a-z0-9+.-]*:/i.test(url)) {
    if (!/^[\w-]+(\.[\w-]+)+(\/|$|\?)/.test(url)) return null;
    url = `https://${url}`;
  }
  try {
    const u = new URL(url);
    return u.protocol === "https:" || u.protocol === "http:" ? u.toString() : null;
  } catch {
    return null;
  }
}

function linkLabel(href: string) {
  const s = href.replace(/^https?:\/\/(www\.)?/, "");
  return s.length > 48 ? `${s.slice(0, 46)}…` : s;
}

function Anchor({ href, children }: { href: string; children: React.ReactNode }) {
  return (
    <a
      href={href}
      target="_blank"
      rel="noopener noreferrer nofollow"
      className="text-primary underline decoration-primary/30 underline-offset-2 hover:decoration-primary"
    >
      {children}
    </a>
  );
}

function renderInline(text: string, keyPrefix: string) {
  return text.split(TOKEN).map((part, i) => {
    const key = `${keyPrefix}-${i}`;
    const md = /^\[([^\]\n]+)\]\(([^)\s]+)\)$/.exec(part);
    if (md) {
      const href = safeHref(md[2]);
      return href ? (
        <Anchor key={key} href={href}>
          {md[1]}
        </Anchor>
      ) : (
        md[1]
      );
    }
    if (part.startsWith("**") && part.endsWith("**") && part.length > 4) {
      return (
        <strong key={key} className="font-semibold text-foreground">
          {part.slice(2, -2)}
        </strong>
      );
    }
    if (part.startsWith("`") && part.endsWith("`") && part.length > 2) {
      return (
        <code key={key} className="rounded bg-primary-tint px-1 py-0.5 font-mono text-[0.85em]">
          {part.slice(1, -1)}
        </code>
      );
    }
    if (/^https?:\/\//.test(part)) {
      const href = safeHref(part);
      return href ? (
        <Anchor key={key} href={href}>
          {linkLabel(href)}
        </Anchor>
      ) : (
        part
      );
    }
    return part;
  });
}

type Block = { kind: "text"; lines: string[] } | { kind: "list"; ordered: boolean; items: string[] };

function blocks(paragraph: string): Block[] {
  const out: Block[] = [];
  for (const line of paragraph.split("\n")) {
    if (LIST_LINE.test(line)) {
      const ordered = /^\s*\d/.test(line);
      const item = line.replace(LIST_LINE, "");
      const last = out[out.length - 1];
      if (last?.kind === "list" && last.ordered === ordered) last.items.push(item);
      else out.push({ kind: "list", ordered, items: [item] });
    } else if (line.trim()) {
      const last = out[out.length - 1];
      if (last?.kind === "text") last.lines.push(line);
      else out.push({ kind: "text", lines: [line] });
    }
  }
  return out;
}

export function RichText({ text }: { text: string }) {
  const paragraphs = text.trim().split(/\n{2,}/);
  return (
    <div className="space-y-3">
      {paragraphs.flatMap((p, i) =>
        blocks(p).map((b, j) => {
          const key = `${i}-${j}`;
          if (b.kind === "list") {
            const List = b.ordered ? "ol" : "ul";
            return (
              <List key={key} className="space-y-1.5">
                {b.items.map((item, k) => (
                  <li key={k} className="flex gap-2.5">
                    {b.ordered ? (
                      <span className="shrink-0 font-mono text-sm text-muted tabular-nums">{k + 1}.</span>
                    ) : (
                      <span className="mt-[0.62em] h-1 w-1 shrink-0 rounded-full bg-muted" aria-hidden="true" />
                    )}
                    <span>{renderInline(item, `${key}-${k}`)}</span>
                  </li>
                ))}
              </List>
            );
          }
          return (
            <p key={key}>
              {b.lines.map((l, k) => (
                <span key={k}>
                  {k > 0 && <br />}
                  {renderInline(l, `${key}-${k}`)}
                </span>
              ))}
            </p>
          );
        })
      )}
    </div>
  );
}
