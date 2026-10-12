"use client";

import Link from "next/link";
import { useId, useState } from "react";
import { IconCheck, Spinner } from "@/components/icons";

// Book a Demo: posts to POST /api/v1/leads, which stores the enquiry for the
// operator. The limits here mirror backend/internal/app/lead_service.go; the
// server checks them again and is the one that decides.

const USAGE = [
  { value: "", label: "Choose one (optional)" },
  { value: "under-1k", label: "Under 1,000 paid calls a month" },
  { value: "1k-10k", label: "1,000 to 10,000" },
  { value: "10k-100k", label: "10,000 to 100,000" },
  { value: "over-100k", label: "Over 100,000" },
  { value: "unsure", label: "Not sure yet" },
];

const field =
  "w-full rounded-lg border border-border-strong bg-surface px-3 py-2.5 text-sm text-foreground placeholder:text-muted focus-visible:border-primary";

type State = { kind: "idle" } | { kind: "sending" } | { kind: "sent" } | { kind: "error"; message: string };

export function DemoRequestForm() {
  const id = useId();
  const [state, setState] = useState<State>({ kind: "idle" });
  const [values, setValues] = useState({ business_name: "", work_email: "", website: "", use_case: "", estimated_usage: "", message: "", company: "" });
  const set = (key: keyof typeof values) => (event: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>) =>
    setValues((v) => ({ ...v, [key]: event.target.value }));

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (state.kind === "sending") return;
    setState({ kind: "sending" });
    // A plan chosen on the pricing page arrives as ?plan=; say so in the message.
    const plan = new URLSearchParams(window.location.search).get("plan");
    const known = ["builder", "startup", "business", "enterprise"];
    const message = plan && known.includes(plan) ? `[Interested in the ${plan} plan]\n${values.message}`.trim() : values.message;
    try {
      const res = await fetch("/api/v1/leads", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "same-origin",
        body: JSON.stringify({ ...values, message }),
      });
      if (res.ok) {
        setState({ kind: "sent" });
        return;
      }
      const data = (await res.json().catch(() => null)) as { error?: string } | null;
      const fallback = res.status === 429 ? "Too many requests from this address. Try again later." : "We couldn't save your request. Try again in a moment.";
      setState({ kind: "error", message: data?.error ? sentence(data.error) : fallback });
    } catch {
      setState({ kind: "error", message: "Can't reach Algebra. Check your connection and try again; nothing was sent." });
    }
  }

  if (state.kind === "sent") {
    return (
      <div role="status" className="self-start rounded-lg border border-border bg-surface p-8">
        <span className="inline-flex size-9 items-center justify-center rounded-full bg-primary-tint text-success"><IconCheck size={18} /></span>
        <h2 className="mt-4 font-display text-xl font-semibold text-foreground">Request received</h2>
        <p className="mt-2 text-[0.975rem] leading-relaxed text-foreground/90">
          It&apos;s saved for our team, and we&apos;ll reply to <strong className="font-medium">{values.work_email}</strong>. If that address is wrong, send the form again.
        </p>
        <p className="mt-6 text-sm text-muted">
          While you wait: <Link href="/signup" className="text-primary underline">create a free account</Link> and run the sandbox quickstart.
        </p>
      </div>
    );
  }

  const sending = state.kind === "sending";
  return (
    <form onSubmit={submit} className="self-start rounded-lg border border-border bg-surface p-6 md:p-8" aria-describedby={state.kind === "error" ? `${id}-error` : undefined}>
      <div className="grid gap-5 sm:grid-cols-2">
        <label className="block">
          <span className="text-sm font-medium text-foreground">Business name</span>
          <input className={`${field} mt-1.5`} name="business_name" required maxLength={120} autoComplete="organization" value={values.business_name} onChange={set("business_name")} />
        </label>
        <label className="block">
          <span className="text-sm font-medium text-foreground">Work email</span>
          <input className={`${field} mt-1.5`} name="work_email" type="email" required maxLength={254} autoComplete="email" value={values.work_email} onChange={set("work_email")} />
        </label>
        <label className="block">
          <span className="text-sm font-medium text-foreground">Website <span className="font-normal text-muted">(optional)</span></span>
          <input className={`${field} mt-1.5`} name="website" maxLength={200} inputMode="url" autoComplete="url" placeholder="example.com" value={values.website} onChange={set("website")} />
        </label>
        <label className="block">
          <span className="text-sm font-medium text-foreground">Estimated usage</span>
          <select className={`${field} mt-1.5`} name="estimated_usage" value={values.estimated_usage} onChange={set("estimated_usage")}>
            {USAGE.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
          </select>
        </label>
        <label className="block sm:col-span-2">
          <span className="text-sm font-medium text-foreground">What do you want your agents to do?</span>
          <textarea className={`${field} mt-1.5 min-h-28`} name="use_case" required maxLength={2000} placeholder="For example: our research agent buys token and wallet data from several providers, and we need a spending limit per customer." value={values.use_case} onChange={set("use_case")} />
        </label>
        <label className="block sm:col-span-2">
          <span className="text-sm font-medium text-foreground">Anything else <span className="font-normal text-muted">(optional)</span></span>
          <textarea className={`${field} mt-1.5 min-h-20`} name="message" maxLength={3900} value={values.message} onChange={set("message")} />
        </label>
        {/* Not for people: left empty by a person, filled by a form-filling bot. */}
        <div aria-hidden="true" className="absolute -left-[9999px] h-0 w-0 overflow-hidden">
          <label>Company<input name="company" tabIndex={-1} autoComplete="off" value={values.company} onChange={set("company")} /></label>
        </div>
      </div>

      {state.kind === "error" && (
        <p id={`${id}-error`} role="alert" className="mt-5 rounded-lg bg-danger-tint px-4 py-3 text-sm text-danger">{state.message}</p>
      )}

      <div className="mt-6 flex flex-wrap items-center gap-4">
        <button type="submit" disabled={sending} className="inline-flex min-h-11 items-center justify-center gap-2 rounded-md bg-primary px-5 py-2.5 text-sm font-medium text-primary-tint hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-50">
          {sending ? <><Spinner size={15} /> Sending</> : "Request a demo"}
        </button>
        <p className="text-xs leading-relaxed text-muted">
          We use this only to reply to you. See the <Link href="/privacy" className="underline">privacy policy</Link>.
        </p>
      </div>
    </form>
  );
}

/** The API's errors are lower-case fragments ("algebra: invalid request: …"); show the reason as a sentence. */
function sentence(error: string) {
  const reason = error.replace(/^(algebra:\s*)?invalid request:\s*/i, "");
  return reason.charAt(0).toUpperCase() + reason.slice(1) + (/[.!?]$/.test(reason) ? "" : ".");
}
