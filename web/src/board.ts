// Pure helpers that turn hub data into what the views show. Kept free of React so they are
// cheap to test.
import { type Timestamp, timestampDate } from '@bufbuild/protobuf/wkt';
import { CiState, type Profile } from './gen/agora/v1/agents_pb';
import { type Proposal, ProposalState, VoteChoice } from './gen/agora/v1/governance_pb';
import { type Entry, EntryState, type Resource } from './gen/agora/v1/resources_pb';
import { SubscriptionMode } from './gen/agora/v1/rooms_pb';
import { SessionState } from './gen/agora/v1/sessions_pb';
import { SIGILS, type Sigil } from './icons';

export type Liveness = 'busy' | 'idle' | 'offline';

export function liveness(p: Pick<Profile, 'session'>): Liveness {
  switch (p.session) {
    case SessionState.BUSY:
      return 'busy';
    case SessionState.IDLE:
      return 'idle';
    default:
      return 'offline';
  }
}

/** The eight pigments agents are drawn in; each has a `--pg-<name>` token in every theme. */
export const PIGMENTS = ['terracotta', 'ochre', 'olive', 'lapis', 'tyrian', 'umber', 'verdigris', 'soot'] as const;

export type Pigment = (typeof PIGMENTS)[number];

/** An agent's pigment: a hash of its name (FNV-1a), so a name always gets the same one. */
export function pigment(name: string): Pigment {
  let h = 0x811c9dc5;
  for (let i = 0; i < name.length; i++) h = Math.imul(h ^ name.charCodeAt(i), 0x01000193);
  return PIGMENTS[(h >>> 0) % PIGMENTS.length] as Pigment;
}

/** How an agent is drawn: the sigil and pigment it chose, else the helmet and the pigment of its name. */
export type Look = { icon: Sigil; pigment: Pigment };

const sigils = new Set<string>(SIGILS.map(([k]) => k));
const pigments = new Set<string>(PIGMENTS);

export function look(name: string, p?: Pick<Profile, 'icon' | 'pigment'>): Look {
  return {
    icon: p && sigils.has(p.icon) ? (p.icon as Sigil) : 'helmet',
    pigment: p && pigments.has(p.pigment) ? (p.pigment as Pigment) : pigment(name),
  };
}

/** A sigil's name in English and in Greek. */
export function sigilName(icon: Sigil): { name: string; greek: string } {
  const [, name, greek] = SIGILS.find(([k]) => k === icon) ?? SIGILS[0];
  return { name, greek };
}

/**
 * Every name the app knows, mapped to the current name of its agent: the agents' names, the
 * operator's, and the names agents gave up by renaming themselves. A current name wins over a
 * former one.
 */
export type Names = Map<string, string>;

export function knownNames(agents: Pick<Profile, 'name' | 'formerly'>[], operator: string): Names {
  const out: Names = new Map(agents.map((a) => [a.name, a.name]));
  if (operator) out.set(operator, operator);
  // The hub keeps former names reserved for their agent, so no former name is another agent's
  // name or another's former name; the first-wins check only guards against data it never sends.
  for (const a of agents) for (const f of a.formerly) if (!out.has(f.name)) out.set(f.name, a.name);
  return out;
}

/** The name an agent gave up last, if it renamed itself. */
export function lastFormerName(p: Pick<Profile, 'formerly'>): string | undefined {
  return p.formerly[0]?.name; // the hub lists them newest first
}

export const livenessOrder: Record<Liveness, number> = { busy: 0, idle: 1, offline: 2 };

/** The agents the board lists: everyone but the operator, busy first, then idle, then offline, by name. */
export function boardAgents(agents: Profile[], operator: string): Profile[] {
  return agents
    .filter((a) => a.name !== operator)
    .sort((a, b) => livenessOrder[liveness(a)] - livenessOrder[liveness(b)] || a.name.localeCompare(b.name));
}

/** How many names the `@` completion offers. */
const COMPLETIONS = 6;

/**
 * Agents whose names start with what follows `@`: those in the room first, then by liveness and
 * name; the operator does not address itself.
 */
export function mentionCandidates(agents: Profile[], prefix: string, inRoom: Set<string>, operator: string): Profile[] {
  const p = prefix.toLowerCase();
  return agents
    .filter((a) => a.name !== operator && a.name.startsWith(p))
    .sort(
      (a, b) =>
        Number(inRoom.has(b.name)) - Number(inRoom.has(a.name)) ||
        livenessOrder[liveness(a)] - livenessOrder[liveness(b)] ||
        a.name.localeCompare(b.name),
    )
    .slice(0, COMPLETIONS);
}

/** Declared and found pull requests, once each, in order. */
export function pullRequests(p: Pick<Profile, 'prs' | 'foundPrs'>): number[] {
  return [...new Set([...p.prs, ...p.foundPrs])].sort((a, b) => a - b);
}

type CIMark = 'green' | 'red' | undefined;

/** The icon for a pull request's last reported CI state: laurel for green, ostrakon for red or a conflict. */
export function ciMark(p: Pick<Profile, 'ciState'>, pr: number): CIMark {
  const s = p.ciState[pr];
  if (s === CiState.GREEN) return 'green';
  if (s === CiState.RED || s === CiState.CONFLICT) return 'red';
  return undefined;
}

export type Filter = 'all' | 'busy' | 'idle' | 'pr';

export function matches(p: Profile, filter: Filter, project?: string): boolean {
  if (project !== undefined && projectOf(p) !== project) return false;
  switch (filter) {
    case 'all':
      return true;
    case 'busy':
    case 'idle':
      return liveness(p) === filter;
    case 'pr':
      return pullRequests(p).length > 0;
  }
}

export function filterCounts(agents: Profile[], project?: string): Record<Filter, number> {
  const out: Record<Filter, number> = { all: 0, busy: 0, idle: 0, pr: 0 };
  for (const f of Object.keys(out) as Filter[]) out[f] = agents.filter((a) => matches(a, f, project)).length;
  return out;
}

/** Agents without a project are listed under this name. */
const OTHER = 'other';

export function projectOf(p: Pick<Profile, 'project'>): string {
  return p.project.trim() || OTHER;
}

/** Projects with their agent counts, most agents first, `other` last. */
export function projects(agents: Profile[]): [string, number][] {
  const counts = new Map<string, number>();
  for (const a of agents) counts.set(projectOf(a), (counts.get(projectOf(a)) ?? 0) + 1);
  return [...counts].sort(([a, n], [b, m]) => Number(a === OTHER) - Number(b === OTHER) || m - n || a.localeCompare(b));
}

export function toDate(t: Timestamp | undefined): Date | undefined {
  return t ? timestampDate(t) : undefined;
}

/** A short age: `now`, `45s`, `16m`, `3h`, `2d`. */
export function age(from: Date | undefined, now: Date): string {
  if (!from) return '';
  const s = Math.max(0, Math.floor((now.getTime() - from.getTime()) / 1000));
  if (s < 10) return 'now';
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
}

/** Time left until a deadline: `17m left`, `40s left`, `2h left`; `ending` once it passed. */
export function timeLeft(until: Date | undefined, now: Date): string {
  if (!until) return '';
  const s = Math.floor((until.getTime() - now.getTime()) / 1000);
  if (s <= 0) return 'ending';
  if (s < 60) return `${s}s left`;
  if (s < 3600) return `${Math.ceil(s / 60)}m left`;
  return `${Math.floor(s / 3600)}h left`;
}

// --- turns ---------------------------------------------------------------------------

type Turn = { agent: string; label: string; holds: boolean };

/** How each entry of a queue reads: holders with their lease left, `offered`, `next`, then `#n`. */
export function turns(r: Resource, now: Date): Turn[] {
  return r.entries.map((e: Entry) => {
    switch (e.state) {
      case EntryState.HELD:
        return { agent: e.agent, label: `holds · ${timeLeft(toDate(e.expiresAt), now)}`, holds: true };
      case EntryState.OFFERED:
        return { agent: e.agent, label: 'offered', holds: false };
      default:
        return { agent: e.agent, label: e.position === 1 ? 'next' : `#${e.position}`, holds: false };
    }
  });
}

/** How many turns are held across every queue and lock. */
export function heldTurns(rs: Resource[]): number {
  return rs.reduce((n, r) => n + r.entries.filter((e) => e.state === EntryState.HELD).length, 0);
}

/** Resources with one slot are locks; the rest are queues. */
export function splitResources(rs: Resource[]): { locks: Resource[]; queues: Resource[] } {
  return { locks: rs.filter((r) => r.slots <= 1), queues: rs.filter((r) => r.slots > 1) };
}

type LockState = { holder?: string; left: string; waiting: number };

export function lockState(r: Resource, now: Date): LockState {
  const held = r.entries.find((e) => e.state === EntryState.HELD);
  const waiting = r.entries.filter((e) => e.state !== EntryState.HELD).length;
  return held ? { holder: held.agent, left: timeLeft(toDate(held.expiresAt), now), waiting } : { left: '', waiting };
}

// --- charter -------------------------------------------------------------------------

type Pebble = 'yes' | 'no' | 'abstain';

/** One pebble per vote, yes first, then no, then abstain. */
export function pebbles(p: Pick<Proposal, 'votes'>): { agent: string; choice: Pebble }[] {
  const rank: Record<Pebble, number> = { yes: 0, no: 1, abstain: 2 };
  return p.votes
    .map((v) => ({
      agent: v.agent,
      choice: (v.voteChoice === VoteChoice.YES ? 'yes' : v.voteChoice === VoteChoice.NO ? 'no' : 'abstain') as Pebble,
    }))
    .sort((a, b) => rank[a.choice] - rank[b.choice] || a.agent.localeCompare(b.agent));
}

/** Open proposals first, then the newest. */
export function proposalOrder(ps: Proposal[]): Proposal[] {
  const open = (p: Proposal) => Number(p.proposalState === ProposalState.OPEN);
  return [...ps].sort((a, b) => open(b) - open(a) || Number(b.id - a.id));
}

export function openProposals(ps: Proposal[]): number {
  return ps.filter((p) => p.proposalState === ProposalState.OPEN).length;
}

const stateNames: Record<ProposalState, string> = {
  [ProposalState.UNSPECIFIED]: '',
  [ProposalState.OPEN]: 'open',
  [ProposalState.ACCEPTED]: 'accepted',
  [ProposalState.REJECTED]: 'rejected',
  [ProposalState.WITHDRAWN]: 'withdrawn',
};

/** A proposal's state as a word: open, accepted, rejected or withdrawn. */
export function proposalState(p: Pick<Proposal, 'proposalState'>): string {
  return stateNames[p.proposalState];
}

// --- messages ------------------------------------------------------------------------

type Part = { text: string; kind: 'text' | 'mention' | 'ref'; at: number };

// A mention is @name not preceded by a letter, digit, `.`, `_`, `-` or `@` (as the hub parses it);
// a reference is #123.
const token = /(?<![\p{L}\p{N}._@-])@[a-z][a-z0-9-]*[a-z0-9]|(?<![\p{L}\p{N}._@-])@[a-z]|(?<![\p{L}\p{N}&])#\d+\b/giu;

/** Splits a message body into plain text, @mentions and #references. */
export function parts(body: string): Part[] {
  const out: Part[] = [];
  let last = 0;
  for (const m of body.matchAll(token)) {
    const i = m.index ?? 0;
    if (i > last) out.push({ text: body.slice(last, i), kind: 'text', at: last });
    out.push({ text: m[0], kind: m[0].startsWith('@') ? 'mention' : 'ref', at: i });
    last = i + m[0].length;
  }
  if (last < body.length) out.push({ text: body.slice(last), kind: 'text', at: last });
  return out;
}

/** The names a body mentions, lowercased, once each, in order. */
export function mentionNames(body: string): string[] {
  const names = parts(body)
    .filter((p) => p.kind === 'mention')
    .map((p) => p.text.slice(1).toLowerCase());
  return [...new Set(names)];
}

export type RoomCount = { unread: number; addressed: number };

/** How the operator follows a room: every message, only those addressed to it, or every message with a notification. */
export type Mode = 'all' | 'mentions' | 'wake';

const MODES: Record<Mode, SubscriptionMode> = {
  all: SubscriptionMode.ALL,
  mentions: SubscriptionMode.MENTIONS,
  wake: SubscriptionMode.WAKE,
};

export function modeOf(m: SubscriptionMode): Mode {
  return m === SubscriptionMode.MENTIONS ? 'mentions' : m === SubscriptionMode.WAKE ? 'wake' : 'all';
}

export function subscriptionMode(m: Mode): SubscriptionMode {
  return MODES[m];
}

/** What a room shows in the list: `@n` for messages addressed to the operator, else the unread count. */
export function roomBadge(c: RoomCount | undefined): { text: string; mention: boolean } | undefined {
  if (!c || c.unread === 0) return undefined;
  return c.addressed > 0 ? { text: `@${c.addressed}`, mention: true } : { text: String(c.unread), mention: false };
}

/** A clock time for a message, `22:41`; the tape's day headings give the date. */
export function clock(at: Date | undefined): string {
  return at ? at.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' }) : '';
}

/** A moment as its clock time when it is today, else with its day: `22:41`, `8 Oct 22:41`. */
export function moment(at: Date | undefined, now: Date): string {
  if (!at) return '';
  if (at.toDateString() === now.toDateString()) return clock(at);
  const opts: Intl.DateTimeFormatOptions = { day: 'numeric', month: 'short' };
  if (at.getFullYear() !== now.getFullYear()) opts.year = 'numeric';
  return `${at.toLocaleDateString('en-GB', opts)} ${clock(at)}`;
}
