// Which model "Auto" means when the person picked none. Pure (no SDK imports),
// so it can be tested without Next.

/**
 * A chat turn is several model calls and a tool or two, and the server stops a
 * turn after about two minutes. Google's list can lead with a preview Pro
 * model, which thinks for a minute before it answers (a turn on it was cut at
 * two minutes in production): so Auto takes a Flash model there, the alias
 * first because it always exists, and the Pro stays one pick away in the
 * model menu. Every other provider's first model is its default, as before.
 */
export function autoModel<T extends { id: string }>(provider: string, models: T[]): T | undefined {
  if (provider === "gemini") {
    const quick = models.find((m) => m.id === "gemini-flash-latest") ?? models.find((m) => /flash/i.test(m.id));
    if (quick) return quick;
  }
  return models[0];
}
