import { isIP } from "node:net";

/**
 * The visitor's address as the hosting platform's edge reports it, or null.
 * Vercel sets these headers itself and overwrites whatever a client sent, so a
 * visitor can't choose their address. When a list comes back the first entry is
 * the client. Anything that is not an IP address is not used.
 */
export function visitorIp(headers: Pick<Headers, "get">): string | null {
  for (const name of ["x-vercel-forwarded-for", "x-real-ip", "x-forwarded-for"]) {
    const first = headers.get(name)?.split(",")[0]?.trim();
    if (first && isIP(first) !== 0) return first;
  }
  return null;
}
