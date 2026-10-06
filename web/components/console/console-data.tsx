"use client";

import { createContext, useCallback, useContext, useEffect, useState } from "react";
import * as api from "@/lib/api-client";

type ConsoleData = {
  /** How many of the person's economic intents wait for their approval. */
  awaitingApproval: number;
  /** Reads it again, after something that changes it (an approval, a new paid call). */
  refresh: () => Promise<void>;
};

const Ctx = createContext<ConsoleData>({ awaitingApproval: 0, refresh: async () => {} });

/**
 * Shared, lightly-polled console state: the badge on Executions. Pages that
 * change it call refresh() so the sidebar updates at once.
 */
export function ConsoleDataProvider({ children }: { children: React.ReactNode }) {
  const [awaitingApproval, setAwaiting] = useState(0);

  const refresh = useCallback(async () => {
    try {
      const l = await api.listMyEconomicIntents(50);
      setAwaiting((l.intents ?? []).filter((i) => i.state === "AWAITING_APPROVAL").length);
    } catch {
      // the badge is best-effort; pages surface their own errors
    }
  }, []);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- initial fetch + polling from the REST API
    refresh();
    const id = window.setInterval(() => {
      if (document.visibilityState === "visible") refresh();
    }, 20000);
    return () => window.clearInterval(id);
  }, [refresh]);

  return <Ctx.Provider value={{ awaitingApproval, refresh }}>{children}</Ctx.Provider>;
}

export function useConsoleData() {
  return useContext(Ctx);
}
