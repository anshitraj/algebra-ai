"use client";

// The person's own wallet, in the top bar: its address and what it holds on
// the chosen cluster. Hover (or click, or tab to) it for a QR code and a copy
// button, so it can be funded from a faucet, an exchange or another wallet.
//
// This is the wallet tied to the account: the one signed in with, or the one
// Privy made for an email sign-in. It is not Algebra's payer wallet, which
// pays providers and is never shown as the person's.

import { QRCodeSVG } from "qrcode.react";
import { useCallback, useEffect, useId, useMemo, useRef, useState } from "react";
import { formatUSDC } from "@/lib/money";
import { networkLabel, useNetwork } from "@/lib/network";
import { useSession } from "@/lib/session";
import dynamic from "next/dynamic";
import { IconCheck, IconExternal, IconRefresh, IconWallet } from "@/components/icons";
import { getAuthProviders } from "@/lib/api-client";
import type { AccountSigner } from "./account-wallet-signer";
import { useAirdrop } from "@/lib/use-airdrop";
import { AirdropAction } from "./airdrop-action";
import { ConvertSol } from "./convert-sol";

// Privy's SDK loads only when the person starts a conversion.
const AccountWalletSigner = dynamic(() => import("./account-wallet-signer"), { ssr: false });

const short = (a: string) => (a.length > 12 ? `${a.slice(0, 4)}…${a.slice(-4)}` : a);
const sol = (lamports: number) => (lamports / 1e9).toLocaleString("en-US", { maximumFractionDigits: 4 });

async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    // Not a secure context, or permission denied: the older way.
    const box = document.createElement("textarea");
    box.value = text;
    box.setAttribute("readonly", "");
    box.style.cssText = "position:fixed;opacity:0;top:0;left:0";
    document.body.appendChild(box);
    box.select();
    try {
      return document.execCommand("copy");
    } catch {
      return false;
    } finally {
      document.body.removeChild(box);
    }
  }
}

export function WalletMenu({ compact = false }: { compact?: boolean }) {
  const { user } = useSession();
  const { network, demo, wallets, refreshWallets } = useNetwork();
  const [open, setOpen] = useState(false);
  const [picked, setPicked] = useState(0);
  const [copied, setCopied] = useState<"yes" | "no" | null>(null);
  // While a swap is being signed in Privy's window (outside this menu), the menu stays open.
  const pinned = useRef(false);
  const [signerWanted, setSignerWanted] = useState(false);
  const [privyAppId, setPrivyAppId] = useState("");
  const [signer, setSigner] = useState<AccountSigner | null>(null);
  const wantSigner = useCallback(() => {
    setSignerWanted(true);
    getAuthProviders()
      .then((p) => setPrivyAppId(p.privy && p.privy_app_id ? p.privy_app_id : ""))
      .catch(() => {});
  }, []);
  const holder = useRef<HTMLDivElement>(null);
  const leaveTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const copyTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const panelId = useId();

  // The account's wallets, the one Privy made for the person first.
  const mine = useMemo(() => {
    const list = [...(user?.wallets ?? [])].filter((w) => w.chain === "solana");
    return list.sort((a, b) => Number(b.kind === "embedded") - Number(a.kind === "embedded"));
  }, [user?.wallets]);

  // The airdrop, asked about only while the menu is open on devnet. It goes to the wallet shown here.
  const airdrop = useAirdrop(open && network === "solana-devnet" && !demo && mine.length > 0, mine[Math.min(picked, Math.max(mine.length - 1, 0))]?.address);

  useEffect(() => {
    if (!open) return;
    const away = (e: PointerEvent) => {
      if (!pinned.current && !holder.current?.contains(e.target as Node)) setOpen(false);
    };
    const key = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("pointerdown", away);
    document.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("pointerdown", away);
      document.removeEventListener("keydown", key);
    };
  }, [open]);

  // What it holds, read again when the menu opens and while it stays open (a faucet's payment arrives).
  useEffect(() => {
    if (!open) return;
    refreshWallets(true);
    const id = setInterval(() => refreshWallets(false), 15_000);
    return () => clearInterval(id);
  }, [open, refreshWallets]);

  useEffect(
    () => () => {
      if (leaveTimer.current) clearTimeout(leaveTimer.current);
      if (copyTimer.current) clearTimeout(copyTimer.current);
    },
    [],
  );

  const enter = useCallback(() => {
    if (leaveTimer.current) clearTimeout(leaveTimer.current);
    setOpen(true);
  }, []);
  const leave = useCallback(() => {
    if (leaveTimer.current) clearTimeout(leaveTimer.current);
    leaveTimer.current = setTimeout(() => {
      // Not while a control inside still has the keyboard.
      if (!pinned.current && !holder.current?.contains(document.activeElement)) setOpen(false);
    }, 180);
  }, []);

  if (demo || network === "sandbox" || mine.length === 0) return null;

  const wallet = mine[Math.min(picked, mine.length - 1)];
  const held = wallets?.wallets.find((w) => w.address === wallet.address);
  const known = held?.ok === true;
  const onDevnet = network === "solana-devnet";
  const balanceText = known ? formatUSDC(held.usdc_minor) : held ? "balance unavailable" : "…";

  async function copy() {
    const ok = await copyText(wallet.address);
    setCopied(ok ? "yes" : "no");
    if (copyTimer.current) clearTimeout(copyTimer.current);
    copyTimer.current = setTimeout(() => setCopied(null), 2200);
  }

  return (
    <div ref={holder} className="relative" onMouseEnter={enter} onMouseLeave={leave} onBlur={leave}>
      {signerWanted && privyAppId && wallet.kind === "embedded" && (
        <AccountWalletSigner appId={privyAppId} email={user?.email} address={wallet.address} onSigner={setSigner} />
      )}
      <button
        type="button"
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-controls={open ? panelId : undefined}
        onClick={() => setOpen((o) => !o)}
        onFocus={enter}
        title={`Your wallet ${short(wallet.address)}`}
        className="inline-flex h-9 items-center gap-2 rounded-xl border border-border bg-surface px-3 text-xs font-medium text-foreground transition-colors hover:bg-primary-tint/60"
      >
        <IconWallet size={15} />
        {compact ? (
          <span className="sr-only">Your wallet {short(wallet.address)}</span>
        ) : (
          <>
            <span className="font-mono">{short(wallet.address)}</span>
            <span className="text-muted">{balanceText}</span>
          </>
        )}
      </button>

      {open && (
        <div
          id={panelId}
          role="dialog"
          aria-label="Your wallet"
          className="absolute right-0 top-full z-50 mt-2 max-h-[calc(100vh-5rem)] w-[19.5rem] overflow-y-auto max-w-[calc(100vw-1.5rem)] rounded-2xl border border-border bg-surface p-4 text-left shadow-[0_18px_44px_-18px_rgb(0_0_0/0.35)]"
        >
          <div className="flex items-center justify-between gap-2">
            <p className="text-sm font-semibold text-foreground">Your wallet</p>
            <span
              className={`rounded-full px-2 py-0.5 text-[0.6875rem] font-medium ${onDevnet ? "bg-accent-tint text-accent" : "bg-primary-tint text-primary"}`}
            >
              {networkLabel(network)} · {onDevnet ? "test funds" : "real funds"}
            </span>
          </div>

          {mine.length > 1 && (
            <div role="tablist" aria-label="Your wallets" className="mt-3 flex flex-wrap gap-1.5">
              {mine.map((w, i) => (
                <button
                  key={w.address}
                  type="button"
                  role="tab"
                  aria-selected={i === picked}
                  onClick={() => setPicked(i)}
                  className={`rounded-lg px-2 py-1 text-[0.6875rem] font-medium ${i === picked ? "bg-primary text-primary-tint" : "bg-primary-tint/60 text-muted hover:text-foreground"}`}
                >
                  {w.kind === "embedded" ? "Account wallet" : "Your wallet"} · {short(w.address)}
                </button>
              ))}
            </div>
          )}

          <div className="mt-3 flex justify-center">
            <div className="rounded-xl bg-white p-3">
              <QRCodeSVG value={wallet.address} size={168} level="M" bgColor="#ffffff" fgColor="#111111" aria-label={`QR code of ${wallet.address}`} />
            </div>
          </div>

          <p className="mt-3 select-all break-all rounded-lg bg-primary-tint/50 px-2.5 py-2 font-mono text-[0.6875rem] leading-relaxed text-foreground">
            {wallet.address}
          </p>
          <div className="mt-2 flex items-center gap-2">
            <button
              type="button"
              onClick={copy}
              className="inline-flex h-9 flex-1 items-center justify-center gap-2 rounded-lg bg-primary text-xs font-medium text-primary-tint hover:opacity-90"
            >
              {copied === "yes" ? <IconCheck size={14} /> : null}
              {copied === "yes" ? "Copied" : copied === "no" ? "Select the address above to copy" : "Copy address"}
            </button>
            <button
              type="button"
              onClick={() => refreshWallets(true)}
              aria-label="Read the balance again"
              title="Read the balance again"
              className="inline-flex h-9 w-9 items-center justify-center rounded-lg border border-border text-muted hover:text-foreground"
            >
              <IconRefresh size={14} />
            </button>
          </div>

          {/* Devnet has the airdrop below (and the public faucets): nothing trades its USDC, so there is no swap to offer. */}
          {network !== "solana" ? null : wallet.kind === "embedded" ? (
            <ConvertSol
              address={wallet.address}
              network={network}
              solLamports={known ? held.sol_lamports : undefined}
              signer={signer?.address === wallet.address ? signer : null}
              wantSigner={wantSigner}
              onBusy={(b) => (pinned.current = b)}
              onDone={() => refreshWallets(true)}
              copyAddress={() => void copyText(wallet.address)}
            />
          ) : (
            <p className="mt-3 text-[0.6875rem] leading-relaxed text-muted">To turn SOL into USDC, use the swap in your wallet app; it lands at this address.</p>
          )}

          <dl className="mt-3 grid grid-cols-2 gap-2 text-xs">
            <div className="rounded-lg border border-border px-2.5 py-2">
              <dt className="text-muted">USDC</dt>
              <dd className="mt-0.5 font-mono text-foreground">{known ? formatUSDC(held.usdc_minor).replace(" USDC", "") : "—"}</dd>
            </div>
            <div className="rounded-lg border border-border px-2.5 py-2">
              <dt className="text-muted">SOL (fees and rent)</dt>
              <dd className="mt-0.5 font-mono text-foreground">{known ? sol(held.sol_lamports) : "—"}</dd>
            </div>
          </dl>
          {held && !known && <p className="mt-2 text-[0.6875rem] text-danger">The Solana node couldn&apos;t be read just now. Try again in a moment.</p>}

          <p className="mt-3 text-[0.6875rem] leading-relaxed text-muted">
            {wallet.kind === "embedded"
              ? "Made for you when you signed in. Privy keeps its key; Algebra only ever sees this address. It signs the transactions that fund and control your Spend Passes."
              : "The wallet you signed in with. Algebra only ever sees this address."}
          </p>

          {onDevnet ? (
            <div className="mt-3 space-y-2 border-t border-border pt-3">
              <p className="text-[0.6875rem] font-medium text-foreground">Devnet: free test money</p>
              <AirdropAction airdrop={airdrop} compact />
              <div className="space-y-1 text-[0.6875rem] leading-relaxed text-muted">
                <p>
                  Devnet only: these are test tokens. For more, open a public faucet, choose <strong className="font-medium text-foreground">Solana Devnet</strong> and paste the address.
                </p>
                <p className="flex flex-wrap gap-x-4 gap-y-1">
                  <a className="inline-flex items-center gap-1 text-primary underline" href="https://faucet.circle.com" target="_blank" rel="noopener noreferrer">
                    Devnet USDC <IconExternal size={10} />
                  </a>
                  <a className="inline-flex items-center gap-1 text-primary underline" href="https://faucet.solana.com" target="_blank" rel="noopener noreferrer">
                    Devnet SOL <IconExternal size={10} />
                  </a>
                </p>
              </div>
            </div>
          ) : (
            <p className="mt-2 text-[0.6875rem] leading-relaxed text-muted">
              Send only USDC and a little SOL on <strong className="font-medium text-foreground">Solana mainnet</strong>. Anything sent on another network can&apos;t be recovered.
            </p>
          )}

        </div>
      )}
    </div>
  );
}
