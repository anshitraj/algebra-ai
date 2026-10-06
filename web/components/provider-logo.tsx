"use client";

import { useState } from "react";
import { ProviderMark } from "./provider-mark";

// Some catalogs list a provider at a gateway or a proxy rather than its own
// site. Their icons are the gateway's, so the brand's own domain is named here.
const GATEWAY_BRANDS: Record<string, string> = {
  google: "google.com",
  alibaba: "alibabacloud.com",
};
const PROXIED_BRANDS: Record<string, string> = {
  perplexity: "perplexity.ai",
  coingecko: "coingecko.com",
  wolframalpha: "wolframalpha.com",
  tripadvisor: "tripadvisor.com",
  fal: "fal.ai",
  screenshotone: "screenshotone.com",
  "2captcha": "2captcha.com",
  rentcast: "rentcast.io",
  reducto: "reducto.ai",
  textbelt: "textbelt.com",
  nyne: "nyne.ai",
};

/** The domain whose icon stands for a provider. */
export function brandDomain(host: string | undefined, fqn?: string): string | undefined {
  if (!host) return undefined;
  const h = host.toLowerCase().replace(/^https?:\/\//, "").replace(/\/.*$/, "");
  const gateway = h.match(/^[a-z0-9-]+\.([a-z0-9-]+)\.gateway-402\.com$/);
  if (gateway && GATEWAY_BRANDS[gateway[1]]) return GATEWAY_BRANDS[gateway[1]];
  if (h.endsWith("paysponge.com") && fqn) {
    const brand = PROXIED_BRANDS[fqn.split("/").pop() ?? ""];
    if (brand) return brand;
  }
  const parts = h.split(".");
  return parts.length > 2 ? parts.slice(-2).join(".") : h;
}

/**
 * A provider's logo: the one its catalog publishes, else its site's icon,
 * else its monogram. Images are only shown, never fetched by Algebra's server,
 * and an image that fails falls through to the next.
 */
export function ProviderLogo({
  name,
  logo,
  website,
  host,
  fqn,
  size = 28,
  className = "",
}: {
  name: string;
  logo?: string;
  website?: string;
  host?: string;
  fqn?: string;
  size?: number;
  className?: string;
}) {
  const domain = brandDomain(website || host, fqn) ?? brandDomain(host, fqn);
  const sources = [logo, domain ? `https://www.google.com/s2/favicons?sz=64&domain=${domain}` : undefined].filter((s): s is string => !!s);
  const [failed, setFailed] = useState(0);

  if (failed >= sources.length) return <ProviderMark name={name} size={size} className={className} />;
  return (
    // Remote logos from many hosts: a plain img, with the next source on error.
    // eslint-disable-next-line @next/next/no-img-element
    <img
      key={sources[failed]}
      src={sources[failed]}
      alt=""
      width={size}
      height={size}
      loading="lazy"
      referrerPolicy="no-referrer"
      onError={() => setFailed((n) => n + 1)}
      className={`shrink-0 border border-border bg-white object-contain ${className}`}
      style={{ width: size, height: size, borderRadius: Math.round(size * 0.28), padding: Math.round(size * 0.12) }}
    />
  );
}
