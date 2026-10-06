"use client";

import { createContext, useCallback, useContext, useEffect, useState } from "react";
import * as api from "@/lib/api-client";
import type { Overview } from "@/lib/types";

type ConsoleData = {
  overview: Overview | null;
  /** How many of the person's economic intents wait for their approval. */
  awaitingApproval: number;
  refreshOverview: () => Promise<void>;
};

const Ctx = createContext<ConsoleData>({ overview: null, awaitingApproval: 0, refreshOverview: async () => {} });

/**
 * Shared, lightly-polled console state (pending approvals badge, today's
 * spend). Pages that change it — approving, placing an order — call
 * refreshOverview() so the sidebar updates immediately.
 */
export function ConsoleDataProvider({ children }: { children: React.ReactNode }) {
  const [overview, setOverview] = useState<Overview | null>(null);
  const [awaitingApproval, setAwaiting] = useState(0);

  const refreshOverview = useCallback(async () => {
    try {
      const l = await api.listMyEconomicIntents(50);
      setAwaiting((l.intents ?? []).filter((i) => i.state === "AWAITING_APPROVAL").length);
    } catch {
      // the badge is best-effort; pages surface their own errors
    }
    try {
      setOverview(await api.getOverview());
    } catch {
      // best-effort too
    }
  }, []);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- initial fetch + polling from the REST API
    refreshOverview();
    const id = window.setInterval(() => {
      if (document.visibilityState === "visible") refreshOverview();
    }, 20000);
    return () => window.clearInterval(id);
  }, [refreshOverview]);

  return <Ctx.Provider value={{ overview, awaitingApproval, refreshOverview }}>{children}</Ctx.Provider>;
}

export function useConsoleData() {
  return useContext(Ctx);
}
