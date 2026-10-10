// Browser notifications while the page is hidden, from the rise of the unread counts per room.
import { useEffect, useRef } from 'react';
import type { RoomCount } from './board';
import type { Notify } from './prefs';

type Alert = { room: string; body: string };

/**
 * What to notify about between two readings of the counts: for Everything, every room whose
 * unread count rose; for Mentions, every room whose count of messages to the operator rose;
 * unless Nothing, also every room in `wake` (followed with Every message notifies) whose unread
 * count rose, and every room where more messages wait for the operator to send them.
 */
export function alerts(
  before: Map<string, RoomCount>,
  after: Map<string, RoomCount>,
  pref: Notify,
  wake: ReadonlySet<string> = new Set(),
): Alert[] {
  const out: Alert[] = [];
  if (pref === 'none') return out;
  for (const [room, c] of after) {
    const was = before.get(room) ?? { unread: 0, addressed: 0 };
    const fresh = c.unread - was.unread;
    const forYou = c.addressed - was.addressed;
    const waiting = Math.max(0, (c.pending ?? 0) - (was.pending ?? 0));
    // a waiting message may also be new and mention the operator: it is told once, as waiting,
    // and the other counts only when they are more than the waiting ones
    const parts: string[] = [];
    if ((pref === 'all' || wake.has(room)) && fresh > waiting) parts.push(`${fresh} new`);
    if (forYou > waiting) parts.push(`${forYou} for you`);
    if (waiting > 0) parts.push(`${waiting} waiting for you to send`);
    if (parts.length > 0) out.push({ room, body: parts.join(' · ') });
  }
  return out;
}

export function notificationsAllowed(): boolean {
  return typeof Notification !== 'undefined' && Notification.permission === 'granted';
}

/** Asks the browser for permission to notify, unless it was already given or refused. */
export async function askToNotify(): Promise<void> {
  if (typeof Notification !== 'undefined' && Notification.permission === 'default')
    await Notification.requestPermission();
}

/** Shows a notification per room whose counts rose while the page is hidden; clicking it opens the room. */
export function useNotifications(
  unread: Map<string, RoomCount> | undefined,
  pref: Notify,
  open: (room: string) => void,
  wake?: ReadonlySet<string>,
) {
  const before = useRef<Map<string, RoomCount>>(undefined);
  useEffect(() => {
    if (!unread) return;
    const was = before.current;
    before.current = unread;
    if (!was || pref === 'none' || document.visibilityState !== 'hidden' || !notificationsAllowed()) return;
    for (const a of alerts(was, unread, pref, wake)) {
      const n = new Notification(`#${a.room}`, { body: a.body, tag: `agora-${a.room}`, icon: '/icons/owl-192.png' });
      n.onclick = () => {
        window.focus();
        open(a.room);
        n.close();
      };
    }
  }, [unread, pref, open, wake]);
}
