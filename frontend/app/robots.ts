import type { MetadataRoute } from "next";

const SITE = process.env.PUBLIC_WEB_URL || "http://localhost:3000";

// Public pages are indexable; the console, onboarding and API are not.
export default function robots(): MetadataRoute.Robots {
  return {
    rules: [{ userAgent: "*", allow: "/", disallow: ["/console", "/onboarding", "/api/", "/reset-password"] }],
    sitemap: `${SITE}/sitemap.xml`,
  };
}
