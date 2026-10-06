// Where to send someone after they sign in. The destination arrives in the URL
// (?next=), so it is attacker-controlled input: only a path on this site is
// ever followed. Mirrors safeNext in internal/api/v1/auth.go.

/** A same-site path (with optional query and hash), or null for anything else. */
export function safeNext(next: string | null | undefined): string | null {
  if (!next || !next.startsWith("/") || next.startsWith("//")) return null;
  for (let i = 0; i < next.length; i++) {
    const c = next.charCodeAt(i);
    // A backslash is a slash to a browser ("/\evil.example" leaves the site),
    // and a browser strips tab, CR and LF from a URL, so "/<TAB>/evil.example"
    // becomes "//evil.example". Control characters have no place in a path.
    if (c === 0x5c || c < 0x20 || c === 0x7f) return null;
  }
  try {
    // Last word goes to the URL parser a browser uses: if it resolves to any
    // other origin, it is not a path on this site.
    const base = "http://site.invalid";
    if (new URL(next, base).origin !== base) return null;
  } catch {
    return null;
  }
  return next;
}
