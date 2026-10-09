"use client";

import { useEffect, useState } from "react";

/** A clock that ticks every `ms` — keeps render pure while showing live countdowns. */
export function useNow(ms = 30000) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), ms);
    return () => window.clearInterval(id);
  }, [ms]);
  return now;
}
