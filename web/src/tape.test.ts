import { describe, expect, it } from 'vitest';
import { knownNames } from './board';
import { agent, message, NOW } from './fixtures';
import {
  addressees,
  addresseesById,
  atBottom,
  between,
  dayLabel,
  farFromBottom,
  firstUnread,
  landing,
  pairs,
  seenThrough,
  tape,
  unseen,
} from './tape';

const known = knownNames(
  [
    agent('builder', 'busy'),
    agent('reviewer', 'idle'),
    agent('release', 'idle'),
    agent('docs-writer', 'idle', { formerly: [{ name: 'fixer' }] }),
  ],
  'operator',
);
const reader = { operator: 'operator', board: 'agora' };

describe('addressees', () => {
  it('are the replied-to author and the known agents mentioned, without the author', () => {
    const parent = message(1, 'builder', 'PR #57 is up', 10);
    const m = message(2, 'reviewer', '@release @Reviewer @nobody approved #57', 5, { replyTo: 1n });
    expect(addressees(m, parent, known, 'agora')).toEqual(['builder', 'release']);
  });
  it('follow a chain of renames to the current name', () => {
    const chain = knownNames(
      [agent('scribe', 'idle', { formerly: [{ name: 'writer' }, { name: 'fixer' }] })],
      'operator',
    );
    const m = message(2, 'builder', '@fixer and @writer, please check #57', 5);
    expect(addressees(m, undefined, chain, 'agora')).toEqual(['scribe']);
  });
  it('count a former name as its agent’s current one', () => {
    const m = message(2, 'builder', '@fixer and @docs-writer, please check #57', 5);
    expect(addressees(m, undefined, known, 'agora')).toEqual(['docs-writer']);
  });
  it('leave out the board when replying to it', () => {
    const parent = message(1, 'agora', 'CI is green on #57', 10);
    expect(addressees(message(2, 'builder', 'thanks', 5, { replyTo: 1n }), parent, known, 'agora')).toEqual([]);
  });
});

describe('tape', () => {
  const ms = [
    message(1, 'builder', 'taking the checkout', 30),
    message(2, 'builder', 'branch fix/address', 28),
    message(3, 'builder', 'and the tests', 20),
    message(4, 'agora', 'CI is green on #57', 19),
    message(5, 'reviewer', '@builder looks good', 18),
    message(6, 'reviewer', 'one question', 17, { replyTo: 1n }),
    message(7, 'release', '@operator ready to tag', 10),
    message(8, 'operator', 'wait for me', 9),
  ];
  const items = tape(ms, addresseesById(ms, known, 'agora'), reader, NOW);
  const msg = (id: number) => items.find((i) => i.kind === 'message' && i.message.id === BigInt(id));

  it('groups one author within 5 minutes when nobody is addressed', () => {
    expect(msg(2)).toMatchObject({ cont: true });
    expect(msg(3)).toMatchObject({ cont: false }); // 8 minutes later
  });
  it('starts a group after a board message, a reply or an addressed message', () => {
    expect(items.find((i) => i.kind === 'board')?.key).toBe('4');
    expect(msg(5)).toMatchObject({ cont: false, to: ['builder'] });
    expect(msg(6)).toMatchObject({ cont: false, to: ['builder'] });
    expect(msg(6)).toHaveProperty('parent.id', 1n);
  });
  it('marks messages to the operator and the operator’s own', () => {
    expect(msg(7)).toMatchObject({ toYou: true, own: false });
    expect(msg(8)).toMatchObject({ toYou: false, own: true });
  });
  it('heads days and puts the NEW line above the first unread', () => {
    const withNew = tape(ms, new Map(), reader, NOW, 3n);
    expect(withNew[0]).toEqual({ kind: 'day', key: 'day-1', label: 'TODAY' });
    const at = withNew.findIndex((i) => i.kind === 'new');
    expect(withNew[at + 1]).toMatchObject({ kind: 'message', cont: false, key: '3' });
  });
});

describe('dayLabel', () => {
  it('reads today, yesterday or the date', () => {
    const now = new Date(2026, 9, 9, 22, 0);
    expect(dayLabel(new Date(2026, 9, 9, 1, 0), now)).toBe('TODAY');
    expect(dayLabel(new Date(2026, 9, 8, 23, 0), now)).toBe('YESTERDAY');
    expect(dayLabel(new Date(2026, 9, 2, 12, 0), now)).toBe('2 OCT');
    expect(dayLabel(new Date(2025, 11, 31, 12, 0), now)).toBe('31 DEC 2025');
  });
});

describe('pairs', () => {
  it('counts who addressed whom, the latest conversation first', () => {
    const ms = [
      message(1, 'builder', '@reviewer PR is up', 10),
      message(2, 'reviewer', '@builder ok', 9),
      message(3, 'release', '@operator ready', 8),
      message(4, 'builder', '@reviewer fixed', 7),
    ];
    const to = addresseesById(ms, known, 'agora');
    expect(pairs(ms, to, 'agora')).toEqual([
      { a: 'builder', b: 'reviewer', n: 3 },
      { a: 'operator', b: 'release', n: 1 },
    ]);
    const renamed = [message(1, 'builder', '@fixer PR is up', 10), message(2, 'builder', '@docs-writer fixed', 9)];
    expect(pairs(renamed, addresseesById(renamed, known, 'agora'), 'agora')).toEqual([
      { a: 'builder', b: 'docs-writer', n: 2 },
    ]);
    const [, answer, other] = ms;
    expect(answer && between(answer, ['builder'], ['builder', 'reviewer'])).toBe(true);
    expect(other && between(other, ['operator'], ['builder', 'reviewer'])).toBe(false);
  });
});

describe('firstUnread', () => {
  const ms = [
    message(1, 'builder', 'one', 5),
    message(2, 'operator', 'mine', 4),
    message(3, 'reviewer', '@operator two', 3),
    message(4, 'builder', 'three', 2),
  ];
  it('is the count-th last message by others', () => {
    expect(firstUnread(ms, 2, reader, true, known)).toBe(3n);
    expect(firstUnread(ms, 9, reader, true, known)).toBe(1n);
    expect(firstUnread(ms, 0, reader, true, known)).toBeUndefined();
  });
  it('counts only mentions of the operator in a room it does not follow', () => {
    expect(firstUnread(ms, 1, reader, false, known)).toBe(3n);
  });
  it('counts a mention of a name the operator gave up', () => {
    const names = knownNames([agent('operator', 'offline', { formerly: [{ name: 'owner' }] })], 'operator');
    const more = [...ms, message(5, 'builder', '@owner done', 1)];
    expect(firstUnread(more, 1, reader, false, names)).toBe(5n);
    expect(firstUnread(more, 1, reader, false, known)).toBe(3n);
  });
});

describe('scrolling', () => {
  it('knows the bottom', () => {
    expect(atBottom({ scrollTop: 950, scrollHeight: 1500, clientHeight: 500 })).toBe(true);
    expect(atBottom({ scrollTop: 800, scrollHeight: 1500, clientHeight: 500 })).toBe(false);
    expect(farFromBottom({ scrollTop: 600, scrollHeight: 1500, clientHeight: 500 })).toBe(true);
  });
  it('lands on the NEW line, else where the room was left, else the bottom', () => {
    expect(landing(true, { top: 10, bottom: false })).toBe('new');
    expect(landing(false, { top: 10, bottom: false })).toBe('saved');
    expect(landing(false, { top: 900, bottom: true })).toBe('bottom');
    expect(landing(false, undefined)).toBe('bottom');
  });
  it('sees messages whose first line is in view', () => {
    const placed = [
      { id: 1n, top: 0, height: 60 },
      { id: 2n, top: 60, height: 200 },
      { id: 3n, top: 260, height: 60 },
    ];
    expect(seenThrough(placed, 100)).toBe(2n);
    expect(seenThrough(placed, 99)).toBe(1n);
    expect(seenThrough([], 100)).toBeUndefined();
  });
  it('counts unseen messages by others and those for the operator', () => {
    const ms = [message(1, 'builder', 'a', 3), message(2, 'operator', 'b', 2), message(3, 'release', '@operator c', 1)];
    expect(unseen(ms, 0n, addresseesById(ms, known, 'agora'), 'operator')).toEqual({ n: 2, forYou: 1, first: 1n });
    expect(unseen(ms, 3n, new Map(), 'operator')).toEqual({ n: 0, forYou: 0, first: undefined });
  });
});
