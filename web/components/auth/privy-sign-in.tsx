"use client";

// Sign-in with Privy: an email code, Google, or a Solana wallet (Phantom,
// Solflare, Backpack…). Anyone who arrives without a Solana wallet is given
// a Privy embedded one, whose key Privy secures for them; Algebra only ever
// sees its address.
//
// Privy's session is used once: its identity token, signed by Privy for this
// app, is traded for Algebra's own session cookie, and Privy is then signed
// out, so signing out of Algebra signs the person out.
//
// Loaded only on the sign-in pages (next/dynamic), so the console doesn't
// carry Privy's SDK.

import { useCallback, useRef, useState } from "react";
import { PrivyProvider, getIdentityToken, useLogin, useLogout, usePrivy, useUser, type User as PrivyUser } from "@privy-io/react-auth";
import { toSolanaWalletConnectors, useCreateWallet } from "@privy-io/react-auth/solana";
import * as api from "@/lib/api-client";
import type { User } from "@/lib/types";
import { IconWallet, Spinner } from "@/components/icons";

type Props = {
  appId: string;
  label: string;
  onSignedIn: (user: User) => void;
  onError: (message: string) => void;
};

const solanaConnectors = toSolanaWalletConnectors({ shouldAutoConnect: false });

export default function PrivySignIn({ appId, ...rest }: Props) {
  return (
    <PrivyProvider
      appId={appId}
      config={{
        // Which methods are offered (email, Google, wallets) is set in the
        // Privy dashboard; only Solana wallets are listed.
        appearance: {
          walletChainType: "solana-only",
          landingHeader: "Sign in to Algebra",
          accentColor: "#4f46e5",
          showWalletLoginFirst: false,
        },
        // Created below, after sign-in, so the wallet exists before the
        // identity token Algebra receives is issued.
        embeddedWallets: { ethereum: { createOnLogin: "off" }, solana: { createOnLogin: "off" } },
        externalWallets: { solana: { connectors: solanaConnectors } },
      }}
    >
      <PrivyButton {...rest} />
    </PrivyProvider>
  );
}

function hasSolanaWallet(user: PrivyUser) {
  return user.linkedAccounts.some((a) => a.type === "wallet" && a.chainType === "solana");
}

function PrivyButton({ label, onSignedIn, onError }: Omit<Props, "appId">) {
  const { ready, authenticated } = usePrivy();
  const { logout } = useLogout();
  const { refreshUser } = useUser();
  const { createWallet } = useCreateWallet();
  const [busy, setBusy] = useState(false);
  // Only a sign-in the person started here is traded: onComplete also fires
  // for a Privy session left over from an earlier visit.
  const started = useRef(false);

  const finish = useCallback(
    async (privyUser: PrivyUser) => {
      if (!started.current) return;
      started.current = false;
      setBusy(true);
      try {
        if (!hasSolanaWallet(privyUser)) {
          await createWallet();
        }
        // A fresh identity token, naming the wallet just made.
        await refreshUser();
        const token = await getIdentityToken();
        if (!token) {
          throw new Error(
            "Privy didn't send an identity token. Turn on “Return user data in an identity token” in the Privy dashboard.",
          );
        }
        const { user } = await api.signInWithPrivy(token);
        await logout().catch(() => {});
        onSignedIn(user);
      } catch (err) {
        await logout().catch(() => {});
        onError(err instanceof Error ? err.message : "Sign-in with Privy didn't complete. Try again.");
        setBusy(false);
      }
    },
    [createWallet, refreshUser, logout, onSignedIn, onError],
  );

  const { login } = useLogin({
    onComplete: ({ user }) => void finish(user),
    onError: (code) => {
      started.current = false;
      if (code !== "exited_auth_flow") onError("Sign-in with Privy didn't complete. Try again.");
    },
  });

  async function start() {
    onError("");
    started.current = true;
    // A Privy session left from an abandoned attempt would skip the modal.
    if (authenticated) await logout().catch(() => {});
    login();
  }

  return (
    <button
      type="button"
      onClick={start}
      disabled={!ready || busy}
      className="flex h-11 w-full items-center justify-center gap-2.5 rounded-xl bg-primary text-[0.95rem] font-medium text-primary-tint shadow-[0_6px_16px_-8px_color-mix(in_srgb,var(--color-primary)_80%,transparent)] transition-[transform,opacity] hover:opacity-95 active:scale-[0.99] disabled:cursor-wait disabled:opacity-70"
    >
      {busy || !ready ? <Spinner size={16} /> : <IconWallet size={18} />}
      {busy ? "Signing you in…" : label}
    </button>
  );
}
