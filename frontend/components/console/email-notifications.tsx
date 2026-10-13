"use client";

// Email notifications, in Account: where sign-in alerts and receipts go, and
// which of the two are sent. They go to the address a person signs in with.
// Someone who signed in with a wallet has none, so they add one here; an
// added address gets only its confirmation link until that link is opened
// (it lands back on this page as ?confirm_email=…).

import { useEffect, useRef, useState } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import * as api from "@/lib/api-client";
import { useSession } from "@/lib/session";
import type { NotificationSettings } from "@/lib/types";
import { IconCheck, IconMail, Spinner } from "@/components/icons";
import { ErrorNote, Skeleton } from "@/components/console/ui";

const KINDS = [
  { id: "sign_ins", label: "Sign-in alerts", hint: "An email after each sign-in: when, which device, and how to sign it out if it wasn't you." },
  { id: "receipts", label: "Payment receipts", hint: "An email for each payment that lands on Solana, with the transaction and the PDF receipt. At most 20 an hour; sandbox calls send none." },
] as const;

export function EmailNotifications() {
  const { refresh } = useSession();
  const router = useRouter();
  const pathname = usePathname();
  const params = useSearchParams();
  const token = params.get("confirm_email");

  const [settings, setSettings] = useState<NotificationSettings | null>(null);
  const [email, setEmail] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [editing, setEditing] = useState(false);
  // The link's token works once. The request is kept here so a second run of the effect
  // (React runs effects twice in development) reads the same answer instead of spending it again.
  const confirming = useRef<{ token: string; answer: Promise<NotificationSettings> } | null>(null);

  useEffect(() => {
    let live = true;
    (async () => {
      if (token) {
        if (confirming.current?.token !== token) confirming.current = { token, answer: api.confirmNotificationEmail(token) };
        try {
          const s = await confirming.current.answer;
          if (!live) return;
          setSettings(s);
          setNotice(`Confirmed. Notifications now go to ${s.address}.`);
          void refresh();
        } catch (e) {
          if (!live) return;
          setError(e instanceof Error ? e.message : "That confirmation link didn't work.");
        }
        // Used: drop it from the address bar.
        router.replace(`${pathname}#notifications`);
        return;
      }
      try {
        const s = await api.getNotifications();
        if (live) setSettings(s);
      } catch (e) {
        if (live) setError((cur) => cur ?? (e instanceof Error ? e.message : "Couldn't load your notification settings."));
      }
    })();
    return () => {
      live = false;
    };
  }, [token, pathname, router, refresh]);

  async function run(name: string, fn: () => Promise<NotificationSettings>, done?: (s: NotificationSettings) => string | null) {
    setBusy(name);
    setError(null);
    setNotice(null);
    try {
      const s = await fn();
      setSettings(s);
      setNotice(done?.(s) ?? null);
      // The banner at the top reads the session's copy.
      void refresh();
    } catch (e) {
      setError(e instanceof Error ? e.message : "That didn't work. Try again.");
    } finally {
      setBusy(null);
    }
  }

  function submit(e: React.FormEvent) {
    e.preventDefault();
    const address = email.trim();
    if (!address) return;
    void run(
      "email",
      () => api.setNotificationEmail(address),
      (s) => {
        setEmail("");
        setEditing(false);
        if (!s.pending_email) return `Notifications go to ${s.address}.`;
        return s.sending
          ? `We sent a confirmation link to ${s.pending_email}. Open it in this browser to finish.`
          : `This server has no mail provider, so the confirmation link for ${s.pending_email} was written to its log instead of sent.`;
      },
    );
  }

  const s = settings;
  const showForm = !!s && s.can_add && (editing || (!s.address && !s.pending_email));

  return (
    <section id="notifications" className="mt-8 scroll-mt-6">
      <h2 className="text-[0.95rem] font-semibold text-foreground">Email notifications</h2>
      <p className="mt-1 text-sm text-muted">A sign-in alert after each sign-in, and a receipt for each payment that lands on Solana.</p>

      <div className="mt-3 overflow-hidden rounded-2xl border border-border bg-surface">
        {s === null && !error && (
          <div className="p-5">
            <Skeleton className="h-16" />
          </div>
        )}

        {s && (
          <div className="flex flex-wrap items-center gap-3.5 border-b border-border px-5 py-4">
            <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-background text-foreground">
              <IconMail size={18} />
            </span>
            <div className="min-w-0 flex-1">
              {s.address ? (
                <>
                  <p className="truncate text-sm text-foreground">{s.address}</p>
                  <p className="text-xs text-muted">{s.source === "account" ? "The address you sign in with" : "Added and confirmed by you"}</p>
                </>
              ) : !s.can_add ? (
                <>
                  <p className="text-sm text-foreground">A demo account has no email</p>
                  <p className="text-xs text-muted">Create an account to get sign-in alerts and receipts.</p>
                </>
              ) : (
                <>
                  <p className="text-sm text-foreground">No email yet</p>
                  <p className="text-xs text-muted">You signed in with a wallet, so there is nowhere to send them. Add an address below.</p>
                </>
              )}
            </div>
            {s.can_add && s.address && !editing && (
              <button type="button" onClick={() => setEditing(true)} className="text-xs font-medium text-muted hover:text-foreground">
                Change
              </button>
            )}
            {s.source === "added" && (
              <button
                type="button"
                disabled={busy !== null}
                onClick={() => run("remove", api.removeNotificationEmail, (n) => (n.address ? `Notifications go back to ${n.address}.` : "Removed. Nothing is emailed until you add an address."))}
                className="inline-flex items-center gap-1.5 text-xs font-medium text-muted hover:text-danger disabled:opacity-50"
              >
                {busy === "remove" && <Spinner size={12} />} Remove
              </button>
            )}
          </div>
        )}

        {s?.pending_email && (
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 border-b border-border bg-accent-tint/40 px-5 py-3 text-sm">
            <span className="min-w-0 flex-1 text-foreground">
              Waiting for <span className="font-medium break-all">{s.pending_email}</span> to be confirmed. Open the link we emailed, in this browser. It lasts 24 hours.
            </span>
            <button
              type="button"
              disabled={busy !== null}
              onClick={() =>
                run(
                  "resend",
                  () => api.setNotificationEmail(s.pending_email!),
                  () => "Sent again.",
                )
              }
              className="inline-flex items-center gap-1.5 text-xs font-medium text-primary hover:underline disabled:opacity-50"
            >
              {busy === "resend" && <Spinner size={12} />} Send it again
            </button>
            <button type="button" disabled={busy !== null} onClick={() => run("remove", api.removeNotificationEmail)} className="text-xs font-medium text-muted hover:text-foreground disabled:opacity-50">
              Cancel
            </button>
          </div>
        )}

        {showForm && (
          <form onSubmit={submit} className="flex flex-col gap-3 border-b border-border px-5 py-4 sm:flex-row sm:items-end">
            <label className="block flex-1">
              <span className="text-sm font-medium text-foreground">{s?.address ? "Send them to another address" : "Your email"}</span>
              <input
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                autoComplete="email"
                maxLength={254}
                required
                placeholder="you@example.com"
                className="mt-1.5 h-11 w-full rounded-xl border border-border-strong bg-background px-3.5 text-[0.95rem] text-foreground placeholder:text-muted focus-visible:border-primary focus-visible:outline-none"
              />
            </label>
            <button
              type="submit"
              disabled={busy !== null || !email.trim()}
              className="inline-flex h-11 items-center justify-center gap-2 rounded-xl bg-primary px-4 text-sm font-medium text-primary-tint disabled:opacity-40"
            >
              {busy === "email" && <Spinner size={14} />} Send confirmation link
            </button>
            {editing && (
              <button type="button" onClick={() => setEditing(false)} className="inline-flex h-11 items-center px-2 text-sm font-medium text-muted hover:text-foreground">
                Cancel
              </button>
            )}
          </form>
        )}

        {s && s.can_add && (
          <ul className="divide-y divide-border">
            {KINDS.map((k) => {
              const on = s[k.id];
              return (
                <li key={k.id} className="flex items-center gap-4 px-5 py-3.5">
                  <div className="min-w-0 flex-1">
                    <p className="text-sm text-foreground">{k.label}</p>
                    <p className="mt-0.5 text-xs leading-relaxed text-muted">{k.hint}</p>
                  </div>
                  <button
                    type="button"
                    role="switch"
                    aria-checked={on}
                    aria-label={k.label}
                    disabled={busy !== null}
                    onClick={() => run(k.id, () => api.setNotificationKinds({ [k.id]: !on }))}
                    className={`relative h-6 w-11 shrink-0 rounded-full transition-colors disabled:opacity-60 ${on ? "bg-primary" : "bg-border-strong"}`}
                  >
                    <span className={`absolute top-0.5 left-0.5 h-5 w-5 rounded-full bg-surface shadow-sm transition-transform ${on ? "translate-x-5" : ""}`} />
                  </button>
                </li>
              );
            })}
          </ul>
        )}
      </div>

      {notice && (
        <p role="status" className="mt-3 flex items-start gap-2 rounded-xl bg-primary-tint px-4 py-3 text-sm text-foreground">
          <IconCheck size={15} className="mt-0.5 shrink-0 text-primary" /> {notice}
        </p>
      )}
      {error && (
        <div className="mt-3">
          <ErrorNote>{error}</ErrorNote>
        </div>
      )}
      {s && s.can_add && !s.sending && (
        <p className="mt-3 text-xs text-muted">This server has no mail provider configured (RESEND_API_KEY): emails are written to the API&apos;s log instead of sent.</p>
      )}
    </section>
  );
}
