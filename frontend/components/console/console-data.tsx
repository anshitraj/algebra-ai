"use client";

import { createContext, useCallback, useContext, useEffect, useState } from "react";
import * as api from "@/lib/api-client";
import type { EconIntent } from "@/lib/types";

type ConsoleData = {
  /** How many of the person's economic intents wait for their approval. */
  awaitingApproval: number;
  /** The person's latest intents, at most 50; null until the first read. The notifications read the payments in it. */
  intents: EconIntent[] | null;
  /** Reads it again, after something that changes it (an approval, a new paid call). */
  refresh: () => Promise<void>;
};

const Ctx = createContext<ConsoleData>({ awaitingApproval: 0, intents: null, refresh: async () => {} });

/**
 * Shared, lightly-polled console state: the badge on Executions and the
 * payments in the notifications. Pages that change it call refresh() so the
 * sidebar and the bell update at once.
 */
export function ConsoleDataProvider({ children }: { children: React.ReactNode }) {
  const [awaitingApproval, setAwaiting] = useState(0);
  const [intents, setIntents] = useState<EconIntent[] | null>(null);

  const refresh = useCallback(async () => {
    try {
      const l = await api.listMyEconomicIntents(50);
      const list = l.intents ?? [];
      setIntents(list);
      setAwaiting(list.filter((i) => i.state === "AWAITING_APPROVAL").length);
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

  return <Ctx.Provider value={{ awaitingApproval, intents, refresh }}>{children}</Ctx.Provider>;
}

export function useConsoleData() {
  return useContext(Ctx);
}
