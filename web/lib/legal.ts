// Who stands behind Algebra, for the legal pages. Set these on the web app
// (server env) before launch — Google's OAuth consent screen checks that these
// pages name a real business and a way to reach it. Anything unset is left out
// rather than invented.

export const LEGAL = {
  /** Registered business name, e.g. "Algebra Technologies Private Limited". */
  entity: process.env.LEGAL_ENTITY_NAME || "Algebra",
  /** Registered office address, one line. */
  address: process.env.LEGAL_ADDRESS || "",
  /** Where users write for help and privacy requests. */
  supportEmail: process.env.SUPPORT_EMAIL || "",
  /** Grievance Officer under India's IT Rules and DPDP Act. */
  grievanceName: process.env.GRIEVANCE_OFFICER_NAME || "",
  grievanceEmail: process.env.GRIEVANCE_OFFICER_EMAIL || process.env.SUPPORT_EMAIL || "",
  /** City whose courts hear disputes, e.g. "Bengaluru". */
  jurisdiction: process.env.LEGAL_JURISDICTION || "",
  /** How the documents name the business: `Acme Pvt Ltd ("Algebra", "we")`, or just `Algebra ("we")` until an entity is set. */
  get who() {
    return this.entity === "Algebra" ? `Algebra ("we")` : `${this.entity} ("Algebra", "we")`;
  },
  /** Shown on every legal page. Update when the text changes. */
  updated: "7 October 2026",
};
