// The operator's drafts, kept for the browser tab so a reload does not lose them. Storage may be
// off or full; drafts then last while the page is open.

/** What the operator was writing in a room; in a bridged room, whether it is a note for the agents. */
export type Draft = { text: string; note: boolean };

export const DRAFTS_KEY = 'agora.drafts';

/** The drafts kept for this tab; anything unreadable counts as none. */
export function loadDrafts(): Record<string, Draft> {
  let stored: unknown;
  try {
    stored = JSON.parse(sessionStorage.getItem(DRAFTS_KEY) ?? '{}');
  } catch {
    return {};
  }
  const drafts: Record<string, Draft> = {};
  if (typeof stored !== 'object' || stored === null) return drafts;
  for (const [room, d] of Object.entries(stored as Record<string, unknown>)) {
    if (typeof d !== 'object' || d === null) continue;
    const { text, note } = d as { text?: unknown; note?: unknown };
    if (typeof text === 'string' && text !== '') drafts[room] = { text, note: note === true };
  }
  return drafts;
}

/** Forgets every draft, as when the operator signs out. */
export function forgetDrafts() {
  try {
    sessionStorage.removeItem(DRAFTS_KEY);
  } catch {
    // nothing was kept then
  }
}

/** Keeps the drafts that hold text; an empty box keeps nothing. */
export function saveDrafts(drafts: Record<string, Draft>) {
  const kept = Object.fromEntries(Object.entries(drafts).filter(([, d]) => d.text !== ''));
  try {
    if (Object.keys(kept).length === 0) sessionStorage.removeItem(DRAFTS_KEY);
    else sessionStorage.setItem(DRAFTS_KEY, JSON.stringify(kept));
  } catch {
    // storage may be off or full; the drafts then last for this page only
  }
}
