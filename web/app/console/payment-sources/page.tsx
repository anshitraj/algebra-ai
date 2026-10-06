"use client";

import { useEffect, useState } from "react";
import * as api from "@/lib/api-client";
import type { PaymentSource } from "@/lib/types";
import { Button, EmptyState, Field, Input, Panel } from "@/components/console/ui";

export default function PaymentSourcesPage() {
  const [sources, setSources] = useState<PaymentSource[] | null>(null);
  const [alias, setAlias] = useState("payment:personal");
  const [nickname, setNickname] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [revokeError, setRevokeError] = useState<string | null>(null);

  async function load() {
    setSources((await api.listPaymentSources()) ?? []);
  }

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- fetching from the REST API on mount
    load();
  }, []);

  async function handleAdd(e: React.FormEvent) {
    e.preventDefault();
    setBusy("add");
    setError(null);
    try {
      // Sandbox only: providers/vault.SandboxProvider derives a
      // deterministic fake token + last4 from this nonce's hash. It is
      // never a real PAN, and there's no hosted-fields SDK wired up in
      // this build to produce a real one — see docs/PAYMENT_SECURITY.md.
      await api.addPaymentSource(crypto.randomUUID(), alias, nickname || undefined);
      setNickname("");
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to add payment source");
    } finally {
      setBusy(null);
    }
  }

  async function handleRevoke(id: string) {
    setBusy(id);
    setRevokeError(null);
    try {
      await api.revokePaymentSource(id);
      await load();
    } catch (err) {
      setRevokeError(`${err instanceof Error ? err.message : "Couldn't reach Algebra"}. The payment source is still active.`);
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="mx-auto max-w-2xl">
      <h1 className="font-display text-2xl font-semibold tracking-tight text-foreground">
        Payment sources
      </h1>
      <p className="mt-1.5 text-sm text-muted">
        Sandbox tokens only — <code className="font-mono text-xs">providers/vault.SandboxProvider</code>{" "}
        never touches a real card. Algebra&rsquo;s backend has no field that
        could hold a PAN or CVV.
      </p>

      <div className="mt-8">
        {revokeError && <p className="mb-3 rounded-lg bg-danger-tint px-3 py-2 text-sm text-danger">{revokeError}</p>}
        {sources === null && <p className="text-sm text-muted">Loading…</p>}
        {sources?.length === 0 && (
          <EmptyState title="No payment sources" body="Add a sandbox card below to see it here." />
        )}
        {sources && sources.length > 0 && (
          <Panel className="divide-y divide-border p-0">
            {sources.map((s) => (
              <div
                key={s.id}
                className={`flex items-center justify-between gap-4 px-5 py-4 ${s.revoked ? "opacity-50" : ""}`}
              >
                <div>
                  <p className="text-sm font-medium text-foreground">
                    {s.nickname || s.alias}{" "}
                    {s.last4 && <span className="font-mono text-muted">···· {s.last4}</span>}
                  </p>
                  <p className="mt-0.5 text-xs text-muted">
                    {s.alias} · {s.type}
                    {s.revoked && " · revoked"}
                  </p>
                </div>
                {!s.revoked && (
                  <button
                    type="button"
                    disabled={busy !== null}
                    onClick={() => handleRevoke(s.id)}
                    className="text-xs font-medium text-danger hover:underline disabled:opacity-40"
                  >
                    {busy === s.id ? "Revoking…" : "Revoke"}
                  </button>
                )}
              </div>
            ))}
          </Panel>
        )}
      </div>

      <Panel className="mt-8">
        <h2 className="font-display text-sm font-semibold text-foreground">Add sandbox card</h2>
        <form onSubmit={handleAdd} className="mt-4 space-y-4">
          <Field label="Alias" hint='Policy only allows "payment:personal" by default.'>
            <Input value={alias} onChange={(e) => setAlias(e.target.value)} required />
          </Field>
          <Field label="Nickname (optional)">
            <Input value={nickname} onChange={(e) => setNickname(e.target.value)} placeholder="Personal Visa" />
          </Field>
          {error && (
            <p className="rounded-lg bg-danger-tint px-3 py-2 text-sm text-danger">{error}</p>
          )}
          <Button type="submit" disabled={busy !== null}>
            {busy === "add" ? "Adding…" : "Add sandbox card"}
          </Button>
        </form>
      </Panel>
    </div>
  );
}
