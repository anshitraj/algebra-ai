"use client";

// Signing with the account wallet: the Solana wallet Privy made for the
// person at sign-in (an email code or Google), whose key Privy keeps for them.
// Algebra builds the transaction unsigned; Privy shows it to the person, signs
// it in its own secure frame and sends it. Algebra never sees the key.
//
// Algebra's sign-in traded Privy's session for its own and signed out of
// Privy, so the first signature asks the person to confirm it is them with
// Privy again (the same email code). Only the wallet already on the account
// is accepted: a different Privy account can't sign for this one.
//
// Loaded with next/dynamic only where a pass is put on chain, so the rest of
// the console doesn't carry Privy's SDK.

import { useCallback, useEffect, useRef } from "react";
import { PrivyProvider, useLogin, usePrivy } from "@privy-io/react-auth";
import { useSignAndSendTransaction, useWallets } from "@privy-io/react-auth/solana";
import { createSolanaRpc, createSolanaRpcSubscriptions } from "@solana/kit";
import { base58 } from "@/lib/onchain-pass";

/** Signs and sends a transaction Algebra prepared, with the account wallet. Returns its signature. */
export type AccountSigner = {
  address: string;
  signAndSend: (txBase64: string, network: string) => Promise<string>;
};

type Props = {
  appId: string;
  /** The account's email, to prefill Privy's code step. */
  email?: string;
  /** The account wallet's address: the only one that may sign. */
  address: string;
  onSigner: (s: AccountSigner | null) => void;
};

const rpcs = {
  "solana:devnet": {
    rpc: createSolanaRpc("https://api.devnet.solana.com"),
    rpcSubscriptions: createSolanaRpcSubscriptions("wss://api.devnet.solana.com"),
    blockExplorerUrl: "https://explorer.solana.com/?cluster=devnet",
  },
  "solana:mainnet": {
    rpc: createSolanaRpc("https://api.mainnet-beta.solana.com"),
    rpcSubscriptions: createSolanaRpcSubscriptions("wss://api.mainnet-beta.solana.com"),
    blockExplorerUrl: "https://explorer.solana.com",
  },
};

export default function AccountWalletSigner({ appId, ...rest }: Props) {
  return (
    <PrivyProvider
      appId={appId}
      config={{
        appearance: { walletChainType: "solana-only", landingHeader: "Confirm it's you", accentColor: "#4f46e5" },
        // The wallet already exists; never make another one here.
        embeddedWallets: { ethereum: { createOnLogin: "off" }, solana: { createOnLogin: "off" } },
        solana: { rpcs },
      }}
    >
      <Bridge {...rest} />
    </PrivyProvider>
  );
}

const short = (a: string) => (a.length > 12 ? `${a.slice(0, 4)}…${a.slice(-4)}` : a);

/** Polls until test() holds, or gives up after ms. */
async function waitFor(test: () => boolean, ms: number) {
  const until = Date.now() + ms;
  while (!test()) {
    if (Date.now() > until) return false;
    await new Promise((r) => setTimeout(r, 150));
  }
  return true;
}

function Bridge({ email, address, onSigner }: Omit<Props, "appId">) {
  const { ready, authenticated, logout } = usePrivy();
  const { wallets, ready: walletsReady } = useWallets();
  const { signAndSendTransaction } = useSignAndSendTransaction();

  const login = useRef<{ resolve: () => void; reject: (e: Error) => void } | null>(null);
  const { login: openLogin } = useLogin({
    onComplete: () => login.current?.resolve(),
    onError: (code) =>
      login.current?.reject(new Error(code === "exited_auth_flow" ? "You closed Privy before confirming. Nothing was sent." : `Privy didn't confirm it's you (${code}).`)),
  });

  // The latest of everything, for the async signer below. Privy's functions
  // are new on every render, so the signer reads them from here and stays the
  // same function (handing the panel a new one each render would loop).
  const state = useRef({ ready, authenticated, wallets, walletsReady, logout, openLogin, signAndSendTransaction });
  useEffect(() => {
    state.current = { ready, authenticated, wallets, walletsReady, logout, openLogin, signAndSendTransaction };
  }, [ready, authenticated, wallets, walletsReady, logout, openLogin, signAndSendTransaction]);

  const signAndSend = useCallback(
    async (txBase64: string, network: string) => {
      if (!(await waitFor(() => state.current.ready, 15_000))) throw new Error("Privy didn't load. Check your connection and try again.");
      if (!state.current.authenticated) {
        const done = new Promise<void>((resolve, reject) => (login.current = { resolve, reject }));
        state.current.openLogin({ prefill: email ? { type: "email", value: email } : undefined, disableSignup: true });
        try {
          await done;
        } finally {
          login.current = null;
        }
      }
      const found = () => state.current.wallets.find((w) => w.address === address);
      if (!(await waitFor(() => state.current.walletsReady && !!found(), 15_000))) {
        const other = state.current.wallets[0]?.address;
        await state.current.logout().catch(() => {});
        throw new Error(
          other
            ? `Privy signed you in with wallet ${short(other)}, not ${short(address)}, the wallet on this account. Sign in to Privy with the same email you use for Algebra.`
            : `Privy didn't open the wallet ${short(address)} on this account. Try again, using the same email you use for Algebra.`,
        );
      }
      const transaction = Uint8Array.from(atob(txBase64), (c) => c.charCodeAt(0));
      const { signature } = await state.current.signAndSendTransaction({
        transaction,
        wallet: found()!,
        chain: network === "solana-devnet" ? "solana:devnet" : "solana:mainnet",
      });
      return base58(signature);
    },
    [address, email],
  );

  useEffect(() => {
    onSigner({ address, signAndSend });
    return () => onSigner(null);
  }, [address, onSigner, signAndSend]);

  return null;
}
