"use client";

import Link from "next/link";
import { useEffect, useRef, useState } from "react";
import { usePathname } from "next/navigation";
import { AnimatePresence, motion } from "motion/react";
import { Logo } from "@/components/logo";
import { displayName, initials, realEmail, useSession } from "@/lib/session";
import type { User } from "@/lib/types";
import {
  IconChat,
  IconChevronDown,
  IconGauge,
  IconGrid,
  IconArrowRight,
  IconCheck,
  IconEye,
  IconTag,
  IconLock,
  IconInbox,
  IconList,
  IconLogOut,
  IconMenu,
  IconShield,
  IconSettings,
  IconStore,
  IconPlug,
  IconX,
  Spinner,
} from "@/components/icons";
import { ConsoleDataProvider, useConsoleData } from "./console-data";
import { NetworkProvider } from "@/lib/network";
import { NotificationsProvider } from "@/lib/notifications";
import { NetworkSwitch } from "./network-switch";
import { NotificationBell } from "./notification-bell";
import { WalletMenu } from "./wallet-menu";

type NavItem = { href: string; label: string; icon: React.ReactNode; badge?: "approvals" };

const PRIMARY: NavItem[] = [
  { href: "/console/agent", label: "Agent chat", icon: <IconChat /> },
  { href: "/console", label: "Overview", icon: <IconGrid /> },
  { href: "/console/providers", label: "Providers", icon: <IconStore /> },
  { href: "/console/intelligence", label: "Provider intelligence", icon: <IconEye /> },
  { href: "/console/routing", label: "Routing", icon: <IconGauge /> },
  { href: "/console/executions", label: "Executions", icon: <IconList />, badge: "approvals" },
];

const CONTROLS: NavItem[] = [
  { href: "/console/passes", label: "Spend passes", icon: <IconShield /> },
  { href: "/console/firewall", label: "Spend firewall", icon: <IconLock /> },
];

const DEVELOPERS: NavItem[] = [
  { href: "/console/quickstart", label: "Quickstart", icon: <IconCheck /> },
  { href: "/console/projects", label: "Projects & API keys", icon: <IconTag /> },
  { href: "/console/connect", label: "Connect an agent", icon: <IconPlug /> },
];

function isActive(pathname: string, href: string) {
  if (href === "/console") return pathname === "/console";
  return pathname === href || pathname.startsWith(href + "/");
}

export function ConsoleShell({ children }: { children: React.ReactNode }) {
  const { user, status } = useSession();

  if (status !== "authenticated" || !user || !user.onboarded) {
    return (
      <div className="flex min-h-dvh items-center justify-center text-muted" aria-busy="true">
        <Spinner size={20} />
      </div>
    );
  }

  return (
    <NetworkProvider demo={user.mode === "demo"}>
      <ConsoleDataProvider>
        <NotificationsProvider userId={user.id}>
          <ShellFrame user={user}>{children}</ShellFrame>
        </NotificationsProvider>
      </ConsoleDataProvider>
    </NetworkProvider>
  );
}

function ShellFrame({ user, children }: { user: User; children: React.ReactNode }) {
  const pathname = usePathname();
  const [drawer, setDrawer] = useState(false);
  const fullBleed = pathname === "/console/agent";

  useEffect(() => {
    // Close the mobile drawer whenever the route changes.
    // eslint-disable-next-line react-hooks/set-state-in-effect -- responding to navigation
    setDrawer(false);
  }, [pathname]);

  return (
    <div className="console-frame flex h-dvh overflow-hidden print:block print:h-auto print:overflow-visible">
      <aside className="console-sidebar console-inverse hidden shrink-0 flex-col border-r border-border md:flex print:hidden">
        <SidebarContents user={user} pathname={pathname} />
      </aside>

      <AnimatePresence>
        {drawer && (
          <>
            <motion.div
              className="fixed inset-0 z-40 bg-foreground/25 md:hidden"
              initial={{ opacity: 0 }}
              animate={{ opacity: 1 }}
              exit={{ opacity: 0 }}
              onClick={() => setDrawer(false)}
            />
            <motion.aside
              className="console-inverse fixed inset-y-0 left-0 z-50 flex w-[280px] flex-col border-r border-border bg-background shadow-2xl md:hidden"
              initial={{ x: "-100%" }}
              animate={{ x: 0 }}
              exit={{ x: "-100%" }}
              transition={{ type: "spring", stiffness: 380, damping: 38 }}
              role="dialog"
              aria-label="Navigation"
            >
              <button
                type="button"
                onClick={() => setDrawer(false)}
                aria-label="Close menu"
                className="absolute top-4 right-3 flex h-9 w-9 items-center justify-center rounded-lg text-muted hover:bg-primary-tint"
              >
                <IconX />
              </button>
              <SidebarContents user={user} pathname={pathname} />
            </motion.aside>
          </>
        )}
      </AnimatePresence>

      <div className="flex min-w-0 flex-1 flex-col">
        {user.mode === "demo" && <DemoBanner />}
        {user.mode !== "demo" && user.needs_notification_email && !pathname.startsWith("/console/settings") && <EmailBanner userId={user.id} />}
        <header className="flex h-14 shrink-0 items-center gap-2 border-b border-border px-3 md:hidden print:hidden">
          <button
            type="button"
            onClick={() => setDrawer(true)}
            aria-label="Open menu"
            className="flex h-9 w-9 items-center justify-center rounded-lg text-foreground hover:bg-primary-tint"
          >
            <IconMenu />
          </button>
          <Link href="/console" className="flex items-center gap-2 text-foreground">
            <Logo size={20} />
            <span className="hidden font-display text-sm font-semibold min-[480px]:inline">Algebra</span>
          </Link>
          <div className="ml-auto flex min-w-0 items-center gap-1.5">
            <MobileApprovalsPill />
            <NetworkSwitch compact />
            <NotificationBell />
            <WalletMenu compact />
          </div>
        </header>
        <div className="console-toolbar hidden shrink-0 items-center gap-3 border-b border-border md:flex print:hidden">
          <div className="console-breadcrumb"><span>Workspace</span><span>/</span><strong>{[...PRIMARY, ...CONTROLS, ...DEVELOPERS].find((item) => isActive(pathname, item.href))?.label ?? "Settings"}</strong></div>
          <div className="console-toolbar-actions"><Link href="/console/connect" className="console-status">Connect an agent <IconArrowRight size={13} /></Link><NetworkSwitch /><NotificationBell /><WalletMenu /></div>
        </div>
        <main className={`min-h-0 flex-1 ${fullBleed ? "overflow-hidden" : "overflow-y-auto"} print:overflow-visible`}>
          {fullBleed ? children : <div className="px-5 py-8 md:px-10 md:py-10">{children}</div>}
        </main>
      </div>
    </div>
  );
}

function MobileApprovalsPill() {
  const { awaitingApproval: n } = useConsoleData();
  if (!n) return null;
  return (
    <Link
      href="/console/executions"
      className="inline-flex h-8 shrink-0 items-center gap-1 rounded-full bg-accent-tint px-2.5 text-xs font-medium text-accent"
      aria-label={`${n} waiting for your approval`}
    >
      <IconInbox size={14} /> {n}
      <span className="hidden min-[480px]:inline">waiting</span>
    </Link>
  );
}

function SidebarContents({ user, pathname }: { user: User; pathname: string }) {
  const { awaitingApproval: pending } = useConsoleData();

  const renderItem = (item: NavItem) => {
    const active = isActive(pathname, item.href);
    return (
      <Link
        key={item.href}
        href={item.href}
        aria-current={active ? "page" : undefined}
        className={`group relative flex h-9 items-center gap-3 rounded-lg px-3 text-sm transition-colors ${
          active ? "font-medium text-foreground" : "text-muted hover:bg-primary-tint/60 hover:text-foreground"
        }`}
      >
        {active && (
          <motion.span
            layoutId="nav-active"
            className="absolute inset-0 rounded-lg bg-primary-tint"
            transition={{ type: "spring", stiffness: 500, damping: 40 }}
          />
        )}
        <span className={`relative [&>svg]:h-[18px] [&>svg]:w-[18px] ${active ? "text-primary" : ""}`}>{item.icon}</span>
        <span className="relative flex-1">{item.label}</span>
        {item.badge === "approvals" && pending > 0 && (
          <span className="relative min-w-5 rounded-full bg-accent px-1.5 text-center font-mono text-[0.7rem] leading-5 font-medium text-accent-tint tabular-nums">
            {pending}
          </span>
        )}
      </Link>
    );
  };

  return (
    <>
      <Link href="/" className="flex h-16 shrink-0 items-center gap-2.5 px-5 text-foreground">
        <Logo size={22} />
        <span className="font-display text-[1.4rem] font-semibold tracking-[-0.06em]">algebra</span>
      </Link>
      <nav className="flex flex-1 flex-col gap-6 overflow-y-auto px-3 pb-4" aria-label="Console">
        <div className="flex flex-col gap-0.5">{PRIMARY.map(renderItem)}</div>
        <div>
          <p className="px-3 pb-1.5 text-xs font-medium text-muted/80">Your agents</p>
          <div className="flex flex-col gap-0.5">{CONTROLS.map(renderItem)}</div>
        </div>
        <div>
          <p className="px-3 pb-1.5 text-xs font-medium text-muted/80">Developers</p>
          <div className="flex flex-col gap-0.5">{DEVELOPERS.map(renderItem)}</div>
        </div>
        <div className="mx-1 mt-auto rounded-lg border border-border px-3.5 py-4"><IconShield size={20} className="text-foreground" /><p className="mt-3 text-xs font-medium text-foreground">Your limits. Every request.</p><p className="mt-1.5 text-[0.6875rem] leading-relaxed text-muted">USDC on Solana. Keys stay with Algebra. Authority stays with you.</p><Link href="/console/firewall" className="mt-3 inline-flex items-center gap-2 text-[0.6875rem] font-medium text-foreground">Review your firewall <IconArrowRight size={12} /></Link></div>
      </nav>
      <UserMenu user={user} />
    </>
  );
}

function Avatar({ user, size = 32 }: { user: User; size?: number }) {
  if (user.avatar_url) {
    // eslint-disable-next-line @next/next/no-img-element -- provider avatar URLs are arbitrary remote hosts
    return <img src={user.avatar_url} alt="" width={size} height={size} className="shrink-0 rounded-full object-cover" referrerPolicy="no-referrer" />;
  }
  return (
    <span
      className="flex shrink-0 items-center justify-center rounded-full bg-primary font-medium text-primary-tint"
      style={{ width: size, height: size, fontSize: size * 0.38 }}
    >
      {initials(user)}
    </span>
  );
}

function UserMenu({ user }: { user: User }) {
  const { signOut } = useSession();
  const [open, setOpen] = useState(false);
  const [leaving, setLeaving] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  return (
    <div ref={ref} className="relative border-t border-border p-3">
      <AnimatePresence>
        {open && (
          <motion.div
            initial={{ opacity: 0, y: 6, scale: 0.98 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: 4, scale: 0.98 }}
            transition={{ duration: 0.16 }}
            className="absolute right-3 bottom-full left-3 mb-2 overflow-hidden rounded-xl border border-border bg-surface p-1 shadow-[0_16px_40px_-16px_rgba(11,16,32,0.35)]"
            role="menu"
          >
            <Link
              href="/console/settings"
              role="menuitem"
              onClick={() => setOpen(false)}
              className="flex h-9 items-center gap-2.5 rounded-lg px-2.5 text-sm text-foreground hover:bg-primary-tint"
            >
              <IconSettings size={16} /> Account settings
            </Link>
            <button
              type="button"
              role="menuitem"
              disabled={leaving}
              onClick={async () => {
                setLeaving(true);
                await signOut();
              }}
              className="flex h-9 w-full items-center gap-2.5 rounded-lg px-2.5 text-sm text-danger hover:bg-danger-tint disabled:opacity-60"
            >
              {leaving ? <Spinner size={16} /> : <IconLogOut size={16} />} Sign out
            </button>
          </motion.div>
        )}
      </AnimatePresence>
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-haspopup="menu"
        aria-expanded={open}
        className="flex w-full items-center gap-3 rounded-xl px-2 py-2 text-left transition-colors hover:bg-primary-tint/60"
      >
        <Avatar user={user} />
        <span className="min-w-0 flex-1">
          <span className="block truncate text-sm font-medium text-foreground">{displayName(user)}</span>
          <span className="block truncate text-xs text-muted">{user.mode === "demo" ? "Trying Algebra without an account" : realEmail(user) || "Signed in with a wallet"}</span>
        </span>
        <IconChevronDown size={16} className={`text-muted transition-transform ${open ? "rotate-180" : ""}`} />
      </button>
    </div>
  );
}

const EMAIL_BANNER_KEY = "algebra:email-banner-dismissed";

// Someone who signed in with a wallet has no address to be written to: no
// sign-in alerts, no receipts. This says so on every screen and leads to
// where one is added (Account > Email notifications). Closing it lasts for
// this browser tab; it is gone for good once an address is confirmed.
function EmailBanner({ userId }: { userId: string }) {
  const [hidden, setHidden] = useState(true);
  useEffect(() => {
    let dismissed = false;
    try {
      dismissed = window.sessionStorage.getItem(EMAIL_BANNER_KEY) === userId;
    } catch {
      // storage blocked: show it
    }
    // eslint-disable-next-line react-hooks/set-state-in-effect -- sessionStorage is unavailable during SSR, so read it after mount
    setHidden(dismissed);
  }, [userId]);
  if (hidden) return null;
  return (
    <div className="flex shrink-0 flex-wrap items-center justify-center gap-x-3 gap-y-1 border-b border-border bg-primary-tint px-4 py-2 text-center text-xs text-foreground print:hidden">
      <span>
        <strong className="font-semibold">Add your email to receive email notifications</strong> · sign-in alerts and a receipt for each payment. You signed in with a wallet, so we have nowhere to send them.
      </span>
      <Link href="/console/settings#notifications" className="font-semibold text-primary underline decoration-primary/40 underline-offset-2 hover:decoration-primary">
        Add email
      </Link>
      <button
        type="button"
        aria-label="Dismiss"
        onClick={() => {
          setHidden(true);
          try {
            window.sessionStorage.setItem(EMAIL_BANNER_KEY, userId);
          } catch {
            // storage blocked: hidden for this page only
          }
        }}
        className="flex h-5 w-5 items-center justify-center rounded text-muted hover:text-foreground"
      >
        <IconX size={12} />
      </button>
    </div>
  );
}

// Every screen of a demo account says so: it works in demo mode, where the
// providers and the money are simulated. The API enforces it; this says so.
function DemoBanner() {
  return (
    <div className="flex shrink-0 flex-wrap items-center justify-center gap-x-3 gap-y-1 border-b border-border bg-accent-tint px-4 py-2 text-center text-xs text-accent print:hidden">
      <span>
        <strong className="font-semibold">Demo mode</strong> · play money and simulated providers. Nothing real moves, and this demo account ends after 3 days.
      </span>
      <Link href="/signup" className="font-semibold underline decoration-accent/40 underline-offset-2 hover:decoration-accent">
        Create your account
      </Link>
    </div>
  );
}

export { Avatar };
