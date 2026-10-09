import type { Metadata } from "next";
import { LegalDoc, Section } from "@/components/legal-doc";
import { LEGAL } from "@/lib/legal";

export const metadata: Metadata = {
  title: "Contact — Algebra",
  description: "Reach Algebra for help with your account, privacy requests or a grievance.",
};

const GITHUB_ISSUES = "https://github.com/anshitraj/algebra-ai/issues";

export default function ContactPage() {
  return (
    <LegalDoc
      title="Contact us"
      intro="For help with your account, a privacy request or a complaint, here's how to reach us. For the work an API provider did, or didn't, do, the provider's own terms apply: ask the provider."
    >
      <Section title="Support">
        {LEGAL.supportEmail ? (
          <p>
            Email <a href={`mailto:${LEGAL.supportEmail}`}>{LEGAL.supportEmail}</a>. We reply within two business days.
          </p>
        ) : (
          <p>
            Open an issue on <a href={GITHUB_ISSUES}>GitHub</a>. Don&apos;t include personal details, tokens or wallet keys there.
          </p>
        )}
      </Section>

      {(LEGAL.grievanceName || LEGAL.grievanceEmail) && (
        <Section title="Grievance Officer">
          <p>
            For privacy requests and complaints under the Digital Personal Data Protection Act, 2023 and the IT Rules, 2021, write to our Grievance
            Officer{LEGAL.grievanceName ? `, ${LEGAL.grievanceName}` : ""}
            {LEGAL.grievanceEmail && (
              <>
                , at <a href={`mailto:${LEGAL.grievanceEmail}`}>{LEGAL.grievanceEmail}</a>
              </>
            )}
            . We acknowledge grievances within 24 hours and resolve them within 15 days.
          </p>
        </Section>
      )}

      <Section title="Company">
        <p>
          {LEGAL.entity}
          {LEGAL.address && (
            <>
              <br />
              {LEGAL.address}
            </>
          )}
        </p>
      </Section>

      <Section title="Developers">
        <p>
          Questions about the API, the MCP server or the router: <a href={GITHUB_ISSUES}>GitHub issues</a>. A security problem: please don&apos;t
          open a public issue; use GitHub&apos;s private vulnerability reporting on the repository, or write to the support address when one is listed.
        </p>
      </Section>
    </LegalDoc>
  );
}
