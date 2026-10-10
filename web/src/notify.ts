// Browser notifications while the page is hidden, from the rise of the unread counts per room.
import { useEffect, useRef } from 'react';
import type { RoomCount } from './board';
import type { Notify } from './prefs';

type Alert = { room: string; body: string };

/**
 * What to notify about between two readings of the unread counts: for Everything, every room
 * whose unread count rose; for Mentions, every room whose count of messages to the operator rose.
 */
export function alerts(before: Map<string, RoomCount>, after: Map<string, RoomCount>, pref: Notify): Alert[] {
  const out: Alert[] = [];
  for (const [room, c] of after) {
    const was = before.get(room) ?? { unread: 0, addressed: 0 };
    const fresh = c.unread - was.unread;
    const forYou = c.addressed - was.addressed;
    if (pref === 'all' && fresh > 0)
      out.push({ room, body: `${fresh} new${forYou > 0 ? ` · ${forYou} for you` : ''}` });
    if (pref === 'mentions' && forYou > 0) out.push({ room, body: `${forYou} for you` });
  }
  return out;
}

export function notificationsAllowed(): boolean {
  return typeof Notification !== 'undefined' && Notification.permission === 'granted';
}

/** Asks the browser for permission to notify, unless it was already given or refused. */
export function askToNotify() {
  if (typeof Notification !== 'undefined' && Notification.permission === 'default')
    void Notification.requestPermission();
}

/** Shows a notification per room whose counts rose while the page is hidden; clicking it opens the room. */
export function useNotifications(
  unread: Map<string, RoomCount> | undefined,
  pref: Notify,
  open: (room: string) => void,
) {
  const before = useRef<Map<string, RoomCount>>(undefined);
  useEffect(() => {
    if (!unread) return;
    const was = before.current;
    before.current = unread;
    if (!was || pref === 'none' || document.visibilityState !== 'hidden' || !notificationsAllowed()) return;
    for (const a of alerts(was, unread, pref)) {
      const n = new Notification(`#${a.room}`, { body: a.body, tag: `agora-${a.room}`, icon: '/icons/owl-192.png' });
      n.onclick = () => {
        window.focus();
        open(a.room);
        n.close();
      };
    }
  }, [unread, pref, open]);
}
