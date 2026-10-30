import { LEGAL } from "@/lib/legal";
import { securityTxt } from "@/lib/security-txt";

const SITE = process.env.PUBLIC_WEB_URL || "http://localhost:3000";

// RFC 9116. The contact is the address the legal pages name (SUPPORT_EMAIL):
// with none configured there is no file rather than a made-up contact.
export function GET() {
  const body = securityTxt({ contact: LEGAL.supportEmail, site: SITE });
  if (!body) return new Response("The security contact is not configured.\n", { status: 404, headers: { "content-type": "text/plain; charset=utf-8" } });
  return new Response(body, { headers: { "content-type": "text/plain; charset=utf-8", "cache-control": "public, max-age=3600" } });
}
