// The agent chat is kept in this browser's localStorage so a reload doesn't
// throw it away. A browser is not a person: it can be shared, and the next
// person can sign in as someone else. So a saved chat records whose it is and
// is only ever restored for that account, and signing out removes it.

// v2: the Solana chat. Chats saved by the earlier shopping agent aren't restored.
export const CHAT_KEY = "algebra:agent-chat:v2";

/** Forgets the saved chat (sign-out, or a chat that belongs to someone else). */
export function clearSavedChat() {
  try {
    window.localStorage.removeItem(CHAT_KEY);
  } catch {
    // storage blocked: nothing was saved
  }
}
