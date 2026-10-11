// Where the console keeps a chat between reloads: this browser's localStorage,
// one slot per signed-in person, so a second account on the same machine never
// opens the first one's conversation.

const PREFIX = "algebra:agent-chat:";

/** The slot holding one person's chat. */
export function chatKey(userId: string): string {
  return `${PREFIX}${userId}`;
}

/**
 * Removes every saved chat in this browser, including the shared slot earlier
 * versions used. Run on sign-out, so the next person at this machine starts clean.
 */
export function clearSavedChats(): void {
  try {
    for (const key of Object.keys(window.localStorage)) {
      if (key.startsWith(PREFIX)) window.localStorage.removeItem(key);
    }
  } catch {
    // storage blocked: nothing was saved
  }
}
