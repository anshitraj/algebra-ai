"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { Logo } from "./logo";
import { IconArrowRight, IconMenu, IconX } from "./icons";
import { NetworkToggle } from "./network-toggle";

const links = [
  { href: "/#product", label: "Product" },
  { href: "/#how-it-works", label: "How it works" },
  { href: "/#security", label: "Why Algebra" },
  { href: "/#integrate", label: "Developers" },
];

export function Nav() {
  const [open, setOpen] = useState(false);
  const [signedIn, setSignedIn] = useState(false);

  useEffect(() => {
    let live = true;
    fetch("/api/v1/auth/session", { credentials: "same-origin", cache: "no-store" })
      .then((response) => response.ok ? response.json() : { user: null })
      .then((data) => { if (live) setSignedIn(Boolean(data?.user)); })
      .catch(() => {});
    return () => { live = false; };
  }, []);

  useEffect(() => {
    if (!open) return;
    const onKey = (event: KeyboardEvent) => { if (event.key === "Escape") setOpen(false); };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open]);

  return (
    <header className="landing-nav">
      <div className="landing-container nav-inner">
        <Link href="/" className="wordmark" onClick={() => setOpen(false)}><Logo size={32} /><span>algebra</span></Link>
        <nav className={open ? "landing-links is-open" : "landing-links"} aria-label="Main navigation" id="landing-menu">
          {links.map((link) => <Link key={link.href} href={link.href} onClick={() => setOpen(false)}>{link.label}</Link>)}
          <Link href={signedIn ? "/console" : "/login"} className="mobile-signin">{signedIn ? "Open workspace" : "Sign in"}</Link>
          <NetworkToggle className="mobile-network" />
        </nav>
        <div className="nav-actions">
          <NetworkToggle className="nav-network" />
          {!signedIn && <Link href="/login" className="nav-signin">Sign in</Link>}
          <Link href={signedIn ? "/console" : "/signup"} className="button-ink nav-cta">{signedIn ? "Open workspace" : "Get started"}<IconArrowRight size={14} /></Link>
          <button className="mobile-menu-toggle" onClick={() => setOpen(!open)} aria-label={open ? "Close navigation" : "Open navigation"} aria-controls="landing-menu" aria-expanded={open}>{open ? <IconX size={21} /> : <IconMenu size={21} />}</button>
        </div>
      </div>
    </header>
  );
}
