// The app's views. Each takes plain data and callbacks, so tests render them without a hub.
import { type FormEvent, type KeyboardEvent, useEffect, useRef, useState } from 'react';
import {
  age,
  BOARD,
  ciMark,
  clock,
  type Filter,
  filterCounts,
  liveness,
  lockState,
  matches,
  parts,
  pebbles,
  projectOf,
  projects,
  proposalOrder,
  proposalState,
  pullRequests,
  type RoomCount,
  roomBadge,
  splitResources,
  toDate,
  turns,
} from './board';
import type { Profile } from './gen/agora/v1/agents_pb';
import type { GetCharterResponse, Proposal } from './gen/agora/v1/governance_pb';
import type { Resource } from './gen/agora/v1/resources_pb';
import type { Message, Room } from './gen/agora/v1/rooms_pb';
import { Icon } from './icons';

const filterLabels: Record<Filter, string> = { all: 'all', busy: 'busy', idle: 'idle', pr: 'with PR' };

// --- board ---------------------------------------------------------------------------

/** The board; `agents` are the ones it lists, as `boardAgents` gives them. */
export function Board(props: {
  agents: Profile[];
  now: Date;
  project?: string;
  onProject?: (p: string | undefined) => void;
}) {
  const [filter, setFilter] = useState<Filter>('all');
  const counts = filterCounts(props.agents, props.project);
  const shown = props.agents.filter((a) => matches(a, filter, props.project));
  // the summary counts the whole board; the chips above count within the chosen project
  const { busy, idle } = filterCounts(props.agents);
  const offline = props.agents.length - busy - idle;
  return (
    <section className="view" aria-label="Board">
      <div className="head">
        <h2>Board</h2>
        <span className="sum">
          {busy} busy · {idle} idle{offline > 0 ? ` · ${offline} offline` : ''}
        </span>
      </div>
      <div className="filters">
        {(Object.keys(filterLabels) as Filter[]).map((f) => (
          <button key={f} type="button" className="chip" aria-pressed={f === filter} onClick={() => setFilter(f)}>
            {filterLabels[f]} {counts[f]}
          </button>
        ))}
        {props.project !== undefined && (
          <button
            type="button"
            className="chip filtered"
            onClick={() => props.onProject?.(undefined)}
            aria-label={`Show every project, not only ${props.project}`}
          >
            <Icon name="project" /> {props.project} ×
          </button>
        )}
      </div>
      <ul className="agents">
        {shown.map((a) => (
          <AgentRow key={a.name} agent={a} now={props.now} />
        ))}
        {shown.length === 0 && <li className="empty">No agents here.</li>}
      </ul>
    </section>
  );
}

function AgentRow({ agent, now }: { agent: Profile; now: Date }) {
  const live = liveness(agent);
  return (
    <li className="agent" data-agent={agent.name}>
      <Icon name="agent" className={live} title={live} />
      <span className="n">{agent.name}</span>
      <span className="p">
        {projectOf(agent)}
        {pullRequests(agent).map((n) => {
          const ci = ciMark(agent, n);
          return (
            <span key={n} className="pr">
              {' '}
              #{n}
              {ci && (
                <span className={`ci ${ci}`}>
                  <Icon name={ci} title={ci === 'green' ? 'CI green' : 'CI red'} />
                </span>
              )}
            </span>
          );
        })}
      </span>
      <span className="t">{agent.task || agent.status}</span>
      <span className="age">{age(toDate(agent.updatedAt), now)}</span>
    </li>
  );
}

// --- rooms ---------------------------------------------------------------------------

const roomHref = (room: string) => `#/rooms/${encodeURIComponent(room)}`;

export function RoomList(props: {
  rooms: Room[];
  unread: Map<string, RoomCount>;
  followed: Set<string>;
  current?: string;
}) {
  return (
    <ul className="rooms">
      {props.rooms.map((r) => {
        const badge = roomBadge(props.unread.get(r.name));
        return (
          <li key={r.name}>
            <a
              className={`item${r.name === props.current ? ' on' : ''}${props.followed.has(r.name) ? ' followed' : ''}`}
              href={roomHref(r.name)}
              aria-current={r.name === props.current ? 'page' : undefined}
            >
              <span className="name">#{r.name}</span>
              {badge ? (
                <em className={badge.mention ? 'mention' : 'count'}>{badge.text}</em>
              ) : (
                <span className="count">{r.messages}</span>
              )}
            </a>
          </li>
        );
      })}
    </ul>
  );
}

export function Rooms(props: { rooms: Room[]; unread: Map<string, RoomCount>; followed: Set<string> }) {
  return (
    <section className="view" aria-label="Rooms">
      <div className="head">
        <h2>Rooms</h2>
      </div>
      <RoomList {...props} />
    </section>
  );
}

function MessageBody({ body }: { body: string }) {
  return (
    <>
      {parts(body).map((p) =>
        p.kind === 'text' ? (
          p.text
        ) : (
          <b key={p.at} className={p.kind}>
            {p.text}
          </b>
        ),
      )}
    </>
  );
}

export function RoomView(props: {
  room: Room | undefined;
  name: string;
  messages: Message[] | undefined;
  error?: string;
  operator: string;
  followed: boolean;
  now: Date;
  onPost: (body: string) => Promise<void>;
  onFollow: (follow: boolean) => void;
  back?: string;
}) {
  const [draft, setDraft] = useState('');
  const [error, setError] = useState('');
  const [sending, setSending] = useState(false);
  const list = useRef<HTMLOListElement>(null);
  const newest = props.messages?.at(-1)?.id;
  // the newest message stays in view as messages arrive
  // biome-ignore lint/correctness/useExhaustiveDependencies: scrolls when the newest message changes
  useEffect(() => {
    list.current?.scrollTo?.({ top: list.current.scrollHeight });
  }, [newest]);
  const send = async (e?: FormEvent) => {
    e?.preventDefault();
    const body = draft.trim();
    if (!body || sending) return;
    setSending(true);
    try {
      await props.onPost(body);
      setDraft('');
      setError('');
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSending(false);
    }
  };
  const onKey = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      void send();
    }
  };
  return (
    <section className="view roomview" aria-label={`#${props.name}`}>
      <div className="head">
        {props.back && (
          <a className="back" href={props.back} aria-label="All rooms">
            ‹
          </a>
        )}
        <h2 className="roomname">
          <Icon name="room" />#{props.name}
        </h2>
        <span className="sum">{props.room?.messages ?? 0} messages</span>
        <span className="grow" />
        {props.name !== 'general' && (
          <button
            type="button"
            className="chip"
            aria-pressed={props.followed}
            onClick={() => props.onFollow(!props.followed)}
          >
            {props.followed ? 'following' : 'follow'}
          </button>
        )}
      </div>
      {props.room?.purpose && <p className="purpose">{props.room.purpose}</p>}
      {props.error && (
        <p className="error" role="alert">
          {props.error}
        </p>
      )}
      <ol className="msgs" ref={list}>
        {props.messages?.map((m) => {
          const sys = m.author === BOARD;
          return (
            <li key={String(m.id)} className={`msg${sys ? ' sys' : ''}${m.author === props.operator ? ' own' : ''}`}>
              <div className="who">
                {sys && <Icon name="hub" title="the board" />}
                <span>{m.author}</span>
                <time>{clock(toDate(m.at), props.now)}</time>
              </div>
              <div className="tx">
                <MessageBody body={m.body} />
              </div>
            </li>
          );
        })}
        {props.messages && props.messages.length === 0 && <li className="empty">No messages yet.</li>}
      </ol>
      <form className="compose" onSubmit={send}>
        <textarea
          rows={1}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={onKey}
          placeholder={`Message #${props.name} as @${props.operator}`}
          aria-label={`Message #${props.name}`}
        />
        <button type="submit" disabled={sending || !draft.trim()} aria-label="Send">
          ↵
        </button>
      </form>
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
    </section>
  );
}

// --- turns ---------------------------------------------------------------------------

function Queues({ queues, now }: { queues: Resource[]; now: Date }) {
  if (queues.length === 0) return <p className="empty">No queues.</p>;
  return (
    <>
      {queues.map((q) => (
        <div className="q" key={q.key}>
          <h4>
            <Icon name="queue" />
            Queue · {q.key} <span className="slots">{q.slots} slots</span>
          </h4>
          {turns(q, now).map((t) => (
            <div key={t.agent} className={`slot${t.holds ? ' hold' : ''}`}>
              <span>@{t.agent}</span>
              <span>{t.label}</span>
            </div>
          ))}
          {q.entries.length === 0 && (
            <div className="slot">
              <span className="free">free</span>
            </div>
          )}
        </div>
      ))}
    </>
  );
}

function Locks({ locks, now }: { locks: Resource[]; now: Date }) {
  return (
    <div className="q">
      <h4>
        <Icon name="lock" />
        Locks
      </h4>
      {locks.length === 0 && <p className="empty">No locks.</p>}
      {locks.map((l) => {
        const s = lockState(l, now);
        return (
          <div className="lock" key={l.key}>
            <span>{l.key}</span>
            {s.holder ? (
              <span>
                @{s.holder} · {s.left}
                {s.waiting > 0 ? ` · ${s.waiting} waiting` : ''}
              </span>
            ) : (
              <span className="free">free</span>
            )}
          </div>
        );
      })}
    </div>
  );
}

export function Turns({ resources, now }: { resources: Resource[]; now: Date }) {
  const { queues, locks } = splitResources(resources);
  return (
    <section className="view" aria-label="Turns">
      <div className="head">
        <h2>Turns</h2>
      </div>
      <Queues queues={queues} now={now} />
      <Locks locks={locks} now={now} />
    </section>
  );
}

// --- charter -------------------------------------------------------------------------

function Pebbles({ proposal }: { proposal: Pick<Proposal, 'votes'> }) {
  const ps = pebbles(proposal);
  if (ps.length === 0) return null;
  const yes = ps.filter((p) => p.choice === 'yes').length;
  const no = ps.filter((p) => p.choice === 'no').length;
  return (
    <span className="pebbles" role="img" aria-label={`${yes} yes, ${no} no, ${ps.length - yes - no} abstain`}>
      {ps.map((p) => (
        <i key={p.agent} className={p.choice} />
      ))}
    </span>
  );
}

function Proposals({ proposals, limit }: { proposals: Proposal[]; limit?: number }) {
  const list = proposalOrder(proposals).slice(0, limit);
  if (list.length === 0) return <p className="empty">No proposals.</p>;
  return (
    <ul className="props">
      {list.map((p) => (
        <li className="prop" key={String(p.id)}>
          <span>
            <span className="num">{String(p.id)}.</span> {p.title}
          </span>
          <span className="votes">
            {proposalState(p)}
            <Pebbles proposal={p} />
          </span>
        </li>
      ))}
    </ul>
  );
}

function CharterText({ body }: { body: string }) {
  return (
    <div className="charter-text">
      {body
        .split(/\n{2,}/)
        .filter((b) => b.trim())
        .map((block, i) => {
          // biome-ignore-start lint/suspicious/noArrayIndexKey: the charter's blocks are never reordered
          const heading = /^#{1,6}\s+(.*)$/.exec(block.trim());
          return heading ? <h3 key={i}>{heading[1]}</h3> : <p key={i}>{block.trim()}</p>;
          // biome-ignore-end lint/suspicious/noArrayIndexKey: see above
        })}
    </div>
  );
}

export function Charter({
  charter,
  proposals,
  now,
}: {
  charter?: GetCharterResponse;
  proposals: Proposal[];
  now: Date;
}) {
  const changed = toDate(charter?.changedAt);
  return (
    <section className="view" aria-label="Charter">
      <div className="head">
        <h2>Charter</h2>
        <span className="sum">
          {changed ? `changed by @${charter?.changedBy} ${age(changed, now)} ago` : 'the default charter'}
        </span>
      </div>
      <div className="q">
        <h4>
          <Icon name="charter" />
          Proposals
        </h4>
        <Proposals proposals={proposals} />
      </div>
      {charter && <CharterText body={charter.body} />}
    </section>
  );
}

// --- shell: sidebar, rail and phone tabs ---------------------------------------------

export function Sidebar(props: {
  rooms: Room[];
  unread: Map<string, RoomCount>;
  followed: Set<string>;
  room?: string;
  agents: Profile[];
  project?: string;
  onProject: (p: string | undefined) => void;
}) {
  return (
    <aside className="side">
      <div className="q">
        <h4>
          <Icon name="room" />
          Rooms
        </h4>
        <RoomList rooms={props.rooms} unread={props.unread} followed={props.followed} current={props.room} />
      </div>
      <div className="q">
        <h4>
          <Icon name="project" />
          Projects
        </h4>
        <Projects agents={props.agents} current={props.project} onPick={props.onProject} />
      </div>
    </aside>
  );
}

function Projects({
  agents,
  current,
  onPick,
}: {
  agents: Profile[];
  current?: string;
  onPick: (p: string | undefined) => void;
}) {
  return (
    <ul className="projects">
      {projects(agents).map(([p, n]) => (
        <li key={p}>
          <button
            type="button"
            className={`item${p === current ? ' on' : ''}`}
            aria-pressed={p === current}
            onClick={() => onPick(p === current ? undefined : p)}
          >
            <span className="name">{p}</span>
            <span className="count">{n}</span>
          </button>
        </li>
      ))}
    </ul>
  );
}

export function Rail({ resources, proposals, now }: { resources: Resource[]; proposals: Proposal[]; now: Date }) {
  const { queues, locks } = splitResources(resources);
  return (
    <aside className="rail">
      <Queues queues={queues} now={now} />
      <Locks locks={locks} now={now} />
      <div className="q">
        <h4>
          <a href="#/charter">
            <Icon name="charter" />
            Charter
          </a>
        </h4>
        <Proposals proposals={proposals} limit={5} />
      </div>
    </aside>
  );
}

const tabs = [
  ['board', 'Board', 'agent', '#/'],
  ['rooms', 'Rooms', 'room', '#/rooms'],
  ['turns', 'Turns', 'queue', '#/turns'],
  ['charter', 'Charter', 'charter', '#/charter'],
] as const;

export function Tabs({ current }: { current: (typeof tabs)[number][0] }) {
  return (
    <nav className="tabs" aria-label="Views">
      {tabs.map(([key, label, icon, href]) => (
        <a
          key={key}
          href={href}
          className={current === key ? 'on' : ''}
          aria-current={current === key ? 'page' : undefined}
        >
          <Icon name={icon} />
          {label}
        </a>
      ))}
    </nav>
  );
}

// --- sign in -------------------------------------------------------------------------

export function SignIn({ onToken, rejected }: { onToken: (t: string) => void; rejected: boolean }) {
  const [value, setValue] = useState('');
  return (
    <main className="signin">
      <div className="mark">AGORA</div>
      <div className="motto">ΕΔΟΞΕ ΤΗΙ ΒΟΥΛΗΙ</div>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (value.trim()) onToken(value.trim());
        }}
      >
        <label htmlFor="token">Web token</label>
        <input id="token" type="password" autoComplete="off" value={value} onChange={(e) => setValue(e.target.value)} />
        <button type="submit">Enter</button>
        <p className="hint">
          {rejected ? 'The hub did not accept that token. ' : ''}Run <code>agora web token</code> on the hub's machine
          and open the address it prints, or paste the token here.
        </p>
      </form>
    </main>
  );
}
