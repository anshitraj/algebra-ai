"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { AnimatePresence, motion } from "motion/react";
import { Logo } from "./logo";
import { IconArrowRight, IconMenu, IconX } from "./icons";

const links = [
  { href: "/#get-started", label: "Get started" },
  { href: "/#how-it-works", label: "How it works" },
  { href: "/#security", label: "Security" },
  { href: "/#providers", label: "Providers" },
  { href: "/#integrate", label: "Developers" },
];

export function Nav() {
  const [open, setOpen] = useState(false);
  const [scrolled, setScrolled] = useState(false);
  const [hovered, setHovered] = useState<string | null>(null);
  const [signedIn, setSignedIn] = useState<boolean | null>(null);

  useEffect(() => {
    const onScroll = () => setScrolled(window.scrollY > 12);
    onScroll();
    window.addEventListener("scroll", onScroll, { passive: true });
    return () => window.removeEventListener("scroll", onScroll);
  }, []);

  useEffect(() => {
    fetch("/api/v1/auth/session", { credentials: "same-origin", cache: "no-store" })
      .then((r) => (r.ok ? r.json() : { user: null }))
      .then((d) => setSignedIn(!!d?.user))
      .catch(() => setSignedIn(false));
  }, []);

  useEffect(() => {
    document.body.style.overflow = open ? "hidden" : "";
    return () => {
      document.body.style.overflow = "";
    };
  }, [open]);

  return (
    <header className="sticky top-0 z-50 px-3 pt-3 md:px-6">
      <div
        className={`mx-auto flex h-14 max-w-6xl items-center justify-between rounded-2xl border px-3 transition-[background-color,border-color,box-shadow] duration-300 md:px-4 ${
          scrolled
            ? "border-border bg-background/80 shadow-[0_10px_30px_-18px_rgba(11,16,32,0.35)] backdrop-blur-xl"
            : "border-transparent bg-transparent"
        }`}
      >
        <Link href="/" className="flex items-center gap-2.5 px-1 text-foreground" onClick={() => setOpen(false)}>
          <Logo size={24} />
          <span className="font-display text-[1.05rem] font-semibold tracking-tight">Algebra</span>
        </Link>

        <nav className="hidden items-center lg:flex" onMouseLeave={() => setHovered(null)} aria-label="Main">
          {links.map((l) => (
            <Link
              key={l.href}
              href={l.href}
              onMouseEnter={() => setHovered(l.href)}
              onFocus={() => setHovered(l.href)}
              className="relative px-3.5 py-2 text-sm text-muted transition-colors hover:text-foreground"
            >
              {hovered === l.href && (
                <motion.span
                  layoutId="nav-hover"
                  className="absolute inset-0 rounded-lg bg-primary-tint"
                  transition={{ type: "spring", stiffness: 500, damping: 38 }}
                />
              )}
              <span className="relative">{l.label}</span>
            </Link>
          ))}
        </nav>

        <div className="hidden items-center gap-2 lg:flex">
          {signedIn ? (
            <Link
              href="/console"
              className="group inline-flex h-9 items-center gap-1.5 rounded-xl bg-primary px-4 text-sm font-medium text-primary-tint transition-transform active:scale-[0.98]"
            >
              Open console <IconArrowRight size={15} className="transition-transform group-hover:translate-x-0.5" />
            </Link>
          ) : (
            <>
              <Link href="/login" className="inline-flex h-9 items-center rounded-xl px-3.5 text-sm font-medium text-foreground hover:bg-primary-tint">
                Sign in
              </Link>
              <Link
                href="/signup"
                className="group inline-flex h-9 items-center gap-1.5 rounded-xl bg-primary px-4 text-sm font-medium text-primary-tint shadow-[0_6px_16px_-8px_color-mix(in_srgb,var(--color-primary)_80%,transparent)] transition-transform active:scale-[0.98]"
              >
                Get started <IconArrowRight size={15} className="transition-transform group-hover:translate-x-0.5" />
              </Link>
            </>
          )}
        </div>

        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          aria-expanded={open}
          aria-label={open ? "Close menu" : "Open menu"}
          className="flex h-10 w-10 items-center justify-center rounded-xl text-foreground hover:bg-primary-tint lg:hidden"
        >
          {open ? <IconX size={20} /> : <IconMenu size={20} />}
        </button>
      </div>

      <AnimatePresence>
        {open && (
          <motion.div
            initial={{ opacity: 0, y: -8 }}
            animate={{ opacity: 1, y: 0 }}
            exit={{ opacity: 0, y: -8 }}
            transition={{ duration: 0.2, ease: [0.16, 1, 0.3, 1] }}
            className="mx-auto mt-2 max-w-6xl overflow-hidden rounded-2xl border border-border bg-background/95 p-3 shadow-[0_20px_40px_-20px_rgba(11,16,32,0.4)] backdrop-blur-xl lg:hidden"
          >
            <nav className="flex flex-col" aria-label="Mobile">
              {links.map((l, i) => (
                <motion.div key={l.href} initial={{ opacity: 0, x: -6 }} animate={{ opacity: 1, x: 0 }} transition={{ delay: 0.03 * i }}>
                  <Link href={l.href} onClick={() => setOpen(false)} className="block rounded-xl px-3 py-3 text-[0.95rem] text-foreground hover:bg-primary-tint">
                    {l.label}
                  </Link>
                </motion.div>
              ))}
            </nav>
            <div className="mt-2 grid grid-cols-2 gap-2 border-t border-border pt-3">
              {signedIn ? (
                <Link href="/console" className="col-span-2 flex h-11 items-center justify-center rounded-xl bg-primary text-sm font-medium text-primary-tint">
                  Open console
                </Link>
              ) : (
                <>
                  <Link href="/login" className="flex h-11 items-center justify-center rounded-xl border border-border-strong text-sm font-medium text-foreground">
                    Sign in
                  </Link>
                  <Link href="/signup" className="flex h-11 items-center justify-center rounded-xl bg-primary text-sm font-medium text-primary-tint">
                    Get started
                  </Link>
                </>
              )}
            </div>
          </motion.div>
        )}
      </AnimatePresence>
    </header>
  );
}
