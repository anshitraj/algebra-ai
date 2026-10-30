/**
 * The body of /.well-known/security.txt (RFC 9116): where to report a
 * vulnerability. It is built from what is configured and nothing is invented:
 * with no usable contact there is no file, because a security.txt that names
 * nobody sends reporters nowhere.
 */
export function securityTxt({ contact, site, now = new Date() }: { contact: string; site: string; now?: Date }): string | null {
  const email = contact.trim();
  if (!/^[^\s@<>]+@[^\s@<>]+\.[^\s@<>]+$/.test(email)) return null;
  // RFC 9116: Expires is required and should be less than a year ahead.
  const expires = new Date(now.getTime() + 180 * 24 * 3600 * 1000).toISOString();
  const lines = [`Contact: mailto:${email}`, `Expires: ${expires}`, "Preferred-Languages: en"];
  const base = site.trim().replace(/\/+$/, "");
  if (base.startsWith("https://")) lines.push(`Canonical: ${base}/.well-known/security.txt`);
  return lines.join("\n") + "\n";
}
