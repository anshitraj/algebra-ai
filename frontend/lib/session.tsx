"use client";

import { createContext, useCallback, useContext, useEffect, useState } from "react";
import { usePathname, useRouter } from "next/navigation";
import * as api from "./api-client";
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
    try {
      const { user } = await api.getSession();
      setUserState(user);
      setStatus(user ? "authenticated" : "unauthenticated");
      return user;
    } catch {
      setUserState(null);
      setStatus("unauthenticated");
      return null;
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

/** First name for greetings; falls back to the email's local part. */
export function firstName(user: User | null): string {
  if (!user) return "";
  const n = user.name?.trim();
  if (n) return n.split(/\s+/)[0];
  return user.email.split("@")[0];
}

export function initials(user: User | null): string {
  if (!user) return "";
  const source = user.name?.trim() || user.email;
  const parts = source.split(/[\s@._-]+/).filter(Boolean);
  return ((parts[0]?.[0] ?? "") + (parts[1]?.[0] ?? "")).toUpperCase() || "A";
}
