// What Algebra knows about a seller, as chips. Each chip is one fact and none
// implies another: a quote is not a payment, a payment that worked is not an
// identity, and only "Verified" means a person reviewed who runs it. Every
// payment is still checked against the Spend Pass and the router's guards,
// whatever these say.

import type { EndpointTrust, ProviderTrust } from "@/lib/types";
import type { Seller } from "@/lib/routing-types";
import { IconBan, IconCheck, IconShield } from "@/components/icons";

type Tone = "verified" | "fact" | "warn" | "bad";

const TONES: Record<Tone, string> = {
  verified: "bg-primary text-primary-tint",
  fact: "bg-primary-tint text-foreground",
  warn: "border border-dashed border-border-strong text-muted",
  bad: "bg-danger-tint text-danger",
};

function Chip({ tone, title, children }: { tone: Tone; title: string; children: React.ReactNode }) {
  return (
    <span title={title} className={`inline-flex h-5 shrink-0 items-center gap-1 rounded-full px-2 text-[0.6875rem] font-medium whitespace-nowrap ${TONES[tone]}`}>
      {children}
    </span>
  );
}

const VERIFIED_TITLE = "Identity verified: every check of Algebra's standard passed and a person reviewed who runs it. Not a guarantee of future behaviour.";
const UNVERIFIED_TITLE = "No person has verified who runs this endpoint. Listing, quoting or paying it never verifies it.";
const TESTED_TITLE = "Algebra paid it and it delivered. One transaction worked; it says nothing about who the seller is.";
const PINNED_TITLE = "Its payee is pinned: a request to be paid at any other address is refused, and the endpoint blocked.";
const BLOCKED_TITLE = "Blocked: nothing is paid to it until a person unblocks it.";

/** One seller in a comparison or a decision. */
export function SellerChips({ seller }: { seller?: Seller }) {
  if (!seller || !seller.known) {
    return (
      <span className="flex flex-wrap gap-1">
        <Chip tone="warn" title="Algebra's registry has no record of this endpoint: who runs it is unknown.">Not in registry</Chip>
      </span>
    );
  }
  return (
    <span className="flex flex-wrap gap-1">
      {seller.trust === "verified" ? (
        <Chip tone="verified" title={VERIFIED_TITLE}>
          <IconCheck size={11} strokeWidth={3} /> Verified
        </Chip>
      ) : seller.trust === "blocked" ? (
        <Chip tone="bad" title={BLOCKED_TITLE}>
          <IconBan size={11} /> Blocked
        </Chip>
      ) : (
        <Chip tone="warn" title={UNVERIFIED_TITLE}>Unverified</Chip>
      )}
      {seller.payment_tested && <Chip tone="fact" title={TESTED_TITLE}>Payment tested</Chip>}
      {seller.payee_pinned && (
        <Chip tone="fact" title={PINNED_TITLE}>
          <IconShield size={11} /> Payee pinned
        </Chip>
      )}
    </span>
  );
}

/** A provider in a listing: its endpoints' facts, counted. */
export function ProviderTrustChips({ trust }: { trust?: ProviderTrust }) {
  if (!trust || trust.endpoints === 0) {
    return <Chip tone="warn" title="Algebra's registry has no record of this provider yet: who runs it is unknown.">Not in registry</Chip>;
  }
  const all = trust.endpoints;
  return (
    <span className="flex flex-wrap gap-1">
      {trust.verified > 0 ? (
        <Chip tone="verified" title={VERIFIED_TITLE}>
          <IconCheck size={11} strokeWidth={3} /> Verified{all > 1 ? ` ${trust.verified}/${all}` : ""}
        </Chip>
      ) : (
        <Chip tone="warn" title={UNVERIFIED_TITLE}>Unverified</Chip>
      )}
      {trust.blocked > 0 && (
        <Chip tone="bad" title={BLOCKED_TITLE}>
          <IconBan size={11} /> {trust.blocked === all ? "Blocked" : `${trust.blocked} blocked`}
        </Chip>
      )}
      {trust.payment_tested > 0 && <Chip tone="fact" title={TESTED_TITLE}>Payment tested</Chip>}
      {trust.payee_pinned > 0 && (
        <Chip tone="fact" title={PINNED_TITLE}>
          <IconShield size={11} /> Payee pinned
        </Chip>
      )}
    </span>
  );
}

/** One endpoint: its facts on each network it is paid on. */
export function EndpointTrustChips({ rows }: { rows?: EndpointTrust[] }) {
  if (!rows || rows.length === 0) return null;
  const known = rows.filter((r) => r.known);
  if (known.length === 0) return <Chip tone="warn" title="Algebra's registry has no record of this endpoint.">Not in registry</Chip>;
  const pick = known.find((r) => r.trust === "blocked") ?? known.find((r) => r.verified) ?? known[0];
  return (
    <SellerChips
      seller={{ known: true, trust: pick.trust ?? "unverified", payment_tested: known.some((r) => r.payment_tested), payee_pinned: known.some((r) => r.payee_pinned) }}
    />
  );
}
