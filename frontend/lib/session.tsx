"use client";

import { createContext, useCallback, useContext, useEffect, useState } from "react";
import { usePathname, useRouter } from "next/navigation";
import * as api from "./api-client";
import { clearSavedChats } from "./agent/chat-store";
import type { User } from "./types";

type SessionStatus = "loading" | "authenticated" | "unauthenticated";

type SessionContextValue = {
  user: User | null;
  status: SessionStatus;
  refresh: () => Promise<User | null>;
  setUser: (user: User) => void;
  signOut: () => Promise<void>;
};

const SessionContext = createContext<SessionContextValue | null>(null);

/**
 * SessionProvider mirrors the server session (HttpOnly cookie, never
 * readable here) into React state via GET /api/v1/auth/session. `required`
 * sends a signed-out visitor to /login and a not-yet-onboarded user to
 * /onboarding.
 */
export function SessionProvider({
  children,
  required = false,
  requireOnboarded = false,
}: {
  children: React.ReactNode;
  required?: boolean;
  requireOnboarded?: boolean;
}) {
  const router = useRouter();
  const pathname = usePathname();
  const [user, setUserState] = useState<User | null>(null);
  const [status, setStatus] = useState<SessionStatus>("loading");

  const refresh = useCallback(async () => {
    // A failed check is not a signed-out visitor: a rate-limited minute (429), the API
    // restarting or a dropped connection must not bounce a signed-in person to /login.
    // Those are asked again a few times before giving up.
    const retryAfterMs = [1500, 4000, 9000];
    for (let attempt = 0; ; attempt++) {
      try {
        const { user } = await api.getSession();
        setUserState(user);
        setStatus(user ? "authenticated" : "unauthenticated");
        return user;
      } catch (err) {
        const transient = !(err instanceof api.ApiError) || err.status === 0 || err.status === 429 || err.status >= 500;
        if (!transient) {
          setUserState(null);
          setStatus("unauthenticated");
          return null;
        }
        if (attempt >= retryAfterMs.length) {
          // Still not answering: someone already signed in stays signed in here.
          setStatus((cur) => (cur === "authenticated" ? cur : "unauthenticated"));
          return null;
        }
        await new Promise((resolve) => setTimeout(resolve, retryAfterMs[attempt]));
      }
    }
  }, []);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- syncing from the server session on mount
    refresh();
  }, [refresh]);

  useEffect(() => {
    if (!required) return;
    if (status === "unauthenticated") {
      router.replace(`/login?next=${encodeURIComponent(pathname)}`);
    } else if (status === "authenticated" && requireOnboarded && user && !user.onboarded) {
      router.replace("/onboarding");
    }
  }, [required, requireOnboarded, status, user, router, pathname]);

  useEffect(() => {
    // Any API 401 (session expired or revoked elsewhere) → back to sign-in.
    const onUnauthorized = () => {
      setUserState(null);
      setStatus("unauthenticated");
    };
    window.addEventListener(api.UNAUTHORIZED_EVENT, onUnauthorized);
    return () => window.removeEventListener(api.UNAUTHORIZED_EVENT, onUnauthorized);
  }, []);

  const setUser = useCallback((next: User) => {
    setUserState(next);
    setStatus("authenticated");
  }, []);

  const signOut = useCallback(async () => {
    try {
      await api.signOut();
    } finally {
      // A conversation stays with the account that had it, not the browser.
      clearSavedChats();
      setUserState(null);
      setStatus("unauthenticated");
      router.replace("/login");
    }
  }, [router]);

  return (
    <SessionContext.Provider value={{ user, status, refresh, setUser, signOut }}>
      {children}
    </SessionContext.Provider>
  );
}

export function useSession() {
  const ctx = useContext(SessionContext);
  if (!ctx) throw new Error("useSession must be used within SessionProvider");
  return ctx;
}

/**
 * The address a person can be reached at, or "". A wallet-only or demo account
 * has a placeholder one (under .invalid): it exists so the account has an
 * email, and is never something to show or to greet anyone by.
 */
export function realEmail(user: User | null): string {
  const e = user?.email?.trim() ?? "";
  return e && !/\.invalid$/i.test(e) ? e : "";
}

/**
 * First name for greetings, or "" when the person gave none. Only a plain word
 * before the @ (ada@…) reads as a name; "ada.l92", a number or a wallet's
 * placeholder does not.
 */
export function firstName(user: User | null): string {
  if (!user || user.mode === "demo") return "";
  const n = user.name?.trim();
  if (n) return n.split(/\s+/)[0];
  const local = realEmail(user).split("@")[0];
  return /^[A-Za-z][A-Za-z'’-]{1,19}$/.test(local) ? local[0].toUpperCase() + local.slice(1) : "";
}

/** What the account menu calls the person: their name, the start of their email, or "Guest" in a demo. */
export function displayName(user: User | null): string {
  if (!user) return "";
  if (user.mode === "demo") return "Guest";
  const n = user.name?.trim();
  if (n) return n;
  return realEmail(user).split("@")[0] || "Your account";
}

export function initials(user: User | null): string {
  if (!user) return "";
  if (user.mode === "demo") return "G";
  const source = user.name?.trim() || realEmail(user);
  const parts = source.split(/[\s@._-]+/).filter(Boolean);
  return ((parts[0]?.[0] ?? "") + (parts[1]?.[0] ?? "")).toUpperCase() || "A";
}
