// Pure helpers for a room's messages: who a message is to, how the tape groups them, who talks
// with whom, and where the tape scrolls. Kept free of React and the DOM so they are cheap to test.
import { mentionNames, type Names, toDate } from './board';
import type { Message } from './gen/agora/v1/rooms_pb';

/** Who the tape is read as: the operator, and the name the board posts its own messages under. */
export type Reader = { operator: string; board: string };

/**
 * The agents a message is to: the author of the message it replies to, then the known agents its
 * body mentions (a former name counting as its agent's current one), without its author and
 * without the board.
 */
export function addressees(m: Message, parent: Message | undefined, known: Names, board: string): string[] {
  const to = parent ? [parent.author] : [];
  for (const name of mentionNames(m.body)) {
    const current = known.get(name);
    if (current) to.push(current);
  }
  return [...new Set(to)].filter((n) => n !== m.author && n !== board);
}

/** Addressees for every message of a list, by id. */
export function addresseesById(messages: Message[], known: Names, board: string): Map<bigint, string[]> {
  const byId = new Map(messages.map((m) => [m.id, m]));
  return new Map(messages.map((m) => [m.id, addressees(m, byId.get(m.replyTo), known, board)]));
}

/** A day heading: `TODAY`, `YESTERDAY` or the date, with the year when it is not this year's. */
export function dayLabel(at: Date, now: Date): string {
  const day = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const days = Math.round((day(now) - day(at)) / 86_400_000);
  if (days === 0) return 'TODAY';
  if (days === 1) return 'YESTERDAY';
  const opts: Intl.DateTimeFormatOptions = { day: 'numeric', month: 'short' };
  if (at.getFullYear() !== now.getFullYear()) opts.year = 'numeric';
  return at.toLocaleDateString('en-GB', opts).toUpperCase();
}

/** Messages of one author closer than this share a header. */
const GROUP_MS = 5 * 60_000;

export type TapeItem =
  | { kind: 'day'; key: string; label: string }
  | { kind: 'new'; key: string }
  | { kind: 'board'; key: string; message: Message }
  | {
      kind: 'message';
      key: string;
      message: Message;
      parent?: Message;
      to: string[];
      /** Shares the header of the message above. */
      cont: boolean;
      toYou: boolean;
      own: boolean;
    };

/**
 * The tape: messages oldest first with day headings and a `NEW` line above `firstNew`. A message
 * continues the one above when the same author wrote it within 5 minutes and it neither replies
 * nor addresses anyone; a heading, the `NEW` line or a board message starts a new group.
 */
export function tape(
  messages: Message[],
  to: Map<bigint, string[]>,
  reader: Reader,
  now: Date,
  firstNew?: bigint,
): TapeItem[] {
  const byId = new Map(messages.map((m) => [m.id, m]));
  const out: TapeItem[] = [];
  let day = '';
  let prev: Message | undefined;
  for (const m of messages) {
    const at = toDate(m.at) ?? now;
    const label = dayLabel(at, now);
    if (label !== day) {
      day = label;
      out.push({ kind: 'day', key: `day-${m.id}`, label });
      prev = undefined;
    }
    if (m.id === firstNew) {
      out.push({ kind: 'new', key: 'new' });
      prev = undefined;
    }
    if (m.author === reader.board) {
      out.push({ kind: 'board', key: String(m.id), message: m });
      prev = undefined;
      continue;
    }
    const mto = to.get(m.id) ?? [];
    const cont =
      prev !== undefined &&
      prev.author === m.author &&
      m.replyTo === 0n &&
      mto.length === 0 &&
      at.getTime() - (toDate(prev.at) ?? now).getTime() < GROUP_MS;
    out.push({
      kind: 'message',
      key: String(m.id),
      message: m,
      parent: m.replyTo ? byId.get(m.replyTo) : undefined,
      to: mto,
      cont,
      toYou: m.author !== reader.operator && mto.includes(reader.operator),
      own: m.author === reader.operator,
    });
    prev = m;
  }
  return out;
}

export type Pair = { a: string; b: string; n: number };

/** Pairs of agents who addressed each other, with how many messages, the latest conversation first. */
export function pairs(messages: Message[], to: Map<bigint, string[]>): Pair[] {
  const found = new Map<string, Pair & { last: number }>();
  messages.forEach((m, i) => {
    for (const other of to.get(m.id) ?? []) {
      const [a, b] = [m.author, other].sort() as [string, string];
      const key = `${a}\n${b}`;
      const p = found.get(key) ?? { a, b, n: 0, last: 0 };
      p.n++;
      p.last = i;
      found.set(key, p);
    }
  });
  return [...found.values()].sort((x, y) => y.last - x.last).map(({ a, b, n }) => ({ a, b, n }));
}

/** Whether a message is between the two agents of a pair: one of them wrote it to the other. */
export function between(m: Message, to: string[], pair: readonly [string, string]): boolean {
  const [a, b] = pair;
  return (m.author === a && to.includes(b)) || (m.author === b && to.includes(a));
}

/**
 * The first unread message when the operator has `count` unread in the room: the count-th last
 * message by others, counting only those that mention the operator when the room is not followed.
 */
export function firstUnread(messages: Message[], count: number, reader: Reader, followed: boolean): bigint | undefined {
  if (count <= 0) return undefined;
  const counted = messages.filter(
    (m) => m.author !== reader.operator && (followed || mentionNames(m.body).includes(reader.operator)),
  );
  return counted[Math.max(0, counted.length - count)]?.id;
}

// --- scrolling ---------------------------------------------------------------------------

type Box = { scrollTop: number; scrollHeight: number; clientHeight: number };

/** Closer to the bottom than this counts as at the bottom. */
const NEAR_BOTTOM = 60;

export function atBottom(b: Box): boolean {
  return b.scrollHeight - b.scrollTop - b.clientHeight < NEAR_BOTTOM;
}

/** Farther than this share of the view from the bottom offers a way back to the latest. */
export function farFromBottom(b: Box): boolean {
  return b.scrollHeight - b.scrollTop - b.clientHeight > b.clientHeight * 0.6;
}

/** Where a room was left: its scroll position and whether that was the bottom. */
export type Saved = { top: number; bottom: boolean };

/** Where entering a room lands: the `NEW` line, else where it was left unless that was the bottom, else the bottom. */
export function landing(hasNew: boolean, saved: Saved | undefined): 'new' | 'saved' | 'bottom' {
  if (hasNew) return 'new';
  if (saved && !saved.bottom) return 'saved';
  return 'bottom';
}

/** A message on the tape: its id, its top and its height, in the tape's coordinates. */
export type Placed = { id: bigint; top: number; height: number };

/** A message counts as seen once its first line (up to 40 pixels) is above the bottom of the view. */
export function seenThrough(placed: Placed[], viewBottom: number): bigint | undefined {
  let last: bigint | undefined;
  for (const p of placed)
    if (p.top + Math.min(p.height, 40) <= viewBottom && (last === undefined || p.id > last)) last = p.id;
  return last;
}

/** Messages by others newer than `seen`: how many, how many are to the operator, and the first. */
export function unseen(
  messages: Message[],
  seen: bigint,
  to: Map<bigint, string[]>,
  operator: string,
): { n: number; forYou: number; first?: bigint } {
  const fresh = messages.filter((m) => m.id > seen && m.author !== operator);
  return {
    n: fresh.length,
    forYou: fresh.filter((m) => to.get(m.id)?.includes(operator)).length,
    first: fresh[0]?.id,
  };
}
