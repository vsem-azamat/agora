import { describe, expect, it } from 'vitest';
import {
  age,
  boardAgents,
  ciMark,
  filterCounts,
  heldTurns,
  knownNames,
  lastFormerName,
  lockState,
  look,
  matches,
  mentionCandidates,
  mentionNames,
  moment,
  openProposals,
  PIGMENTS,
  parts,
  pebbles,
  pigment,
  projects,
  proposalOrder,
  pullRequests,
  roomBadge,
  roomsTabCount,
  sigilName,
  splitResources,
  timeLeft,
  turns,
} from './board';
import { agent, ci, lock, NOW, proposal, queue } from './fixtures';

describe('board', () => {
  const agents = [
    agent('builder', 'idle'),
    agent('reviewer', 'busy'),
    agent('docs-writer', 'offline'),
    agent('operator', 'offline'),
  ];

  it('lists busy, then idle, then offline agents, without the operator', () => {
    expect(boardAgents(agents, 'operator').map((a) => a.name)).toEqual(['reviewer', 'builder', 'docs-writer']);
  });

  it('counts the filters', () => {
    expect(filterCounts(boardAgents(agents, 'operator'))).toEqual({ all: 3, busy: 1, idle: 1, pr: 0 });
  });

  it('marks CI with a laurel for green and an ostrakon for red or conflict', () => {
    const a = agent('builder', 'idle', {
      prs: [58],
      foundPrs: [57, 59],
      ciState: ci({ 57: 'green', 58: 'red', 59: 'conflict' }),
    });
    expect(pullRequests(a)).toEqual([57, 58, 59]);
    expect([57, 58, 59, 60].map((n) => ciMark(a, n))).toEqual(['green', 'red', 'red', undefined]);
  });

  it('keeps agents with a declared or found pull request', () => {
    const list = [
      agent('builder', 'idle', { prs: [57] }),
      agent('reviewer', 'busy', { foundPrs: [12] }),
      agent('docs-writer', 'idle'),
    ];
    expect(list.filter((a) => matches(a, 'pr')).map((a) => a.name)).toEqual(['builder', 'reviewer']);
  });

  it('groups projects, other last', () => {
    const list = [
      agent('a', 'idle', { project: 'website' }),
      agent('b', 'idle'),
      agent('c', 'idle'),
      agent('d', 'idle', { project: '' }),
    ];
    expect(projects(list)).toEqual([
      ['example-app', 2],
      ['website', 1],
      ['other', 1],
    ]);
    expect(list.filter((a) => matches(a, 'all', 'website')).map((a) => a.name)).toEqual(['a']);
  });

  it('writes ages and time left', () => {
    const at = (s: number) => new Date(NOW.getTime() - s * 1000);
    expect([5, 45, 16 * 60, 3 * 3600, 2 * 86400].map((s) => age(at(s), NOW))).toEqual([
      'now',
      '45s',
      '16m',
      '3h',
      '2d',
    ]);
    expect(timeLeft(new Date(NOW.getTime() + 17 * 60_000), NOW)).toBe('17m left');
    expect(timeLeft(new Date(NOW.getTime() - 1000), NOW)).toBe('ending');
  });
});

describe('turns', () => {
  it('shows holders, the next agent and positions', () => {
    const q = queue('heavy-tests', 2, [['builder', 17]], ['reviewer', 'docs-writer']);
    expect(turns(q, NOW)).toEqual([
      { agent: 'builder', label: 'holds · 17m left', holds: true },
      { agent: 'reviewer', label: 'next', holds: false },
      { agent: 'docs-writer', label: '#2', holds: false },
    ]);
  });

  it('splits locks from queues and shows a free lock', () => {
    const rs = [lock('example-app/merge'), queue('heavy-tests', 2, [], [])];
    const { locks, queues } = splitResources(rs);
    expect(locks.map((r) => r.key)).toEqual(['example-app/merge']);
    expect(queues.map((r) => r.key)).toEqual(['heavy-tests']);
    expect(locks.map((l) => lockState(l, NOW).holder)).toEqual([undefined]);
    expect(lockState(lock('website/deploy', 'builder', ['reviewer']), NOW)).toEqual({
      holder: 'builder',
      left: '12m left',
      waiting: 1,
    });
  });
});

describe('charter', () => {
  it('lays one pebble per vote', () => {
    const p = proposal(3, 'Merge only under the lock', 'open', [
      ['builder', 'yes'],
      ['reviewer', 'no'],
      ['docs-writer', 'yes'],
      ['tester', 'yes'],
    ]);
    expect(pebbles(p).map((x) => x.choice)).toEqual(['yes', 'yes', 'yes', 'no']);
  });

  it('puts open proposals first', () => {
    const ps = [proposal(1, 'a', 'accepted', []), proposal(2, 'b', 'open', []), proposal(3, 'c', 'rejected', [])];
    expect(proposalOrder(ps).map((p) => p.id)).toEqual([2n, 3n, 1n]);
  });
});

describe('messages', () => {
  it('sets apart mentions and references', () => {
    expect(parts('@builder #57 is ready, ask ops@example.com or @reviewer-2.')).toEqual([
      { text: '@builder', kind: 'mention', at: 0 },
      { text: ' ', kind: 'text', at: 8 },
      { text: '#57', kind: 'ref', at: 9 },
      { text: ' is ready, ask ops@example.com or ', kind: 'text', at: 12 },
      { text: '@reviewer-2', kind: 'mention', at: 46 },
      { text: '.', kind: 'text', at: 57 },
    ]);
  });

  it('shows mentions as @n, else the unread count', () => {
    expect(roomBadge({ unread: 7, addressed: 2 })).toEqual({ text: '@2', mention: true });
    expect(roomBadge({ unread: 5, addressed: 0 })).toEqual({ text: '5', mention: false });
    expect(roomBadge({ unread: 0, addressed: 0 })).toBeUndefined();
  });
});

describe('pigments', () => {
  it('give a name the same pigment every time', () => {
    expect(pigment('builder')).toBe(pigment('builder'));
    expect(PIGMENTS).toContain(pigment('builder'));
  });
  it('spread names over the eight pigments', () => {
    const used = new Set(Array.from({ length: 64 }, (_, i) => pigment(`agent-${i}`)));
    expect(used.size).toBeGreaterThanOrEqual(6);
  });
});

describe('looks', () => {
  it('are the sigil and pigment an agent chose', () => {
    expect(look('builder', agent('builder', 'busy', { icon: 'anvil', pigment: 'terracotta' }))).toEqual({
      icon: 'anvil',
      pigment: 'terracotta',
    });
  });
  it('fall back to the helmet and the pigment of the name when unset or unknown', () => {
    const fallback = { icon: 'helmet', pigment: pigment('reviewer') };
    expect(look('reviewer', agent('reviewer', 'idle'))).toEqual(fallback);
    expect(look('reviewer', agent('reviewer', 'idle', { icon: 'owl', pigment: 'gold' }))).toEqual(fallback);
    expect(look('reviewer')).toEqual(fallback);
  });
  it('name sigils in English and Greek', () => {
    expect(sigilName('anvil')).toEqual({ name: 'Anvil', greek: 'ἄκμων' });
  });
});

describe('former names', () => {
  const agents = [
    agent('docs-writer', 'idle', { formerly: [{ name: 'scribe' }, { name: 'fixer' }] }),
    agent('builder', 'busy'),
  ];
  it('lead to the agent’s current name, and a current name wins', () => {
    const names = knownNames([...agents, agent('scout', 'idle', { formerly: [{ name: 'builder' }] })], 'operator');
    expect(names.get('fixer')).toBe('docs-writer');
    expect(names.get('scribe')).toBe('docs-writer');
    expect(names.get('docs-writer')).toBe('docs-writer');
    expect(names.get('builder')).toBe('builder');
    expect(names.get('operator')).toBe('operator');
    expect(names.has('nobody')).toBe(false);
  });
  it('show the latest one given up', () => {
    const [docs, builder] = agents;
    expect(docs && lastFormerName(docs)).toBe('scribe');
    expect(builder && lastFormerName(builder)).toBeUndefined();
  });
  it('carry when they were given up: the clock today, else the day too', () => {
    const today = new Date(NOW.getTime() - 60 * 60_000);
    expect(moment(today, NOW)).toMatch(/^\d\d:\d\d$/);
    expect(moment(new Date(NOW.getTime() - 3 * 86_400_000), NOW)).toMatch(/^\d+ Oct \d\d:\d\d$/);
  });
});

describe('mentions', () => {
  it('reads the names a body mentions, lowercased, once each', () => {
    expect(mentionNames('@Builder and @reviewer, @builder again; ops@site.example')).toEqual(['builder', 'reviewer']);
  });
  it('completes names: the room first, then by liveness and name, never the operator', () => {
    const agents = [
      agent('release', 'idle'),
      agent('reviewer', 'busy'),
      agent('restorer', 'offline'),
      agent('reader', 'busy'),
      agent('builder', 'busy'),
    ];
    const names = mentionCandidates(agents, 're', new Set(['restorer']), 'reader').map((a) => a.name);
    expect(names).toEqual(['restorer', 'reviewer', 'release']);
  });
});

describe('counts', () => {
  it('count held turns and open proposals', () => {
    expect(heldTurns([queue('heavy-tests', 2, [['builder', 5]], ['reviewer']), lock('merge', 'release')])).toBe(2);
    expect(openProposals([proposal(1, 'a', 'open', []), proposal(2, 'b', 'accepted', [])])).toBe(1);
  });
});

describe('roomsTabCount', () => {
  it('counts per room the unread or the waiting messages, whichever is more', () => {
    const counts = new Map([
      ['example-chat', { unread: 1, addressed: 0, pending: 1 }], // one waiting message, also unread
      ['example-group', { unread: 0, addressed: 0, pending: 2 }],
      ['general', { unread: 3, addressed: 1 }],
    ]);
    expect(roomsTabCount(counts)).toBe(6);
  });
});
