// A room: its messages on the tape, who talks with whom, and the compose box. The tape follows
// new messages only while the operator is at the bottom; see tape.ts for the decisions.
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { knownNames, type Liveness, liveness as livenessOf, livenessOrder, type Mode, type RoomCount } from '../board';
import { authorKey, POLICY_LABELS, type Policy, policyOf, runningOf, runningText } from '../bridges';
import type { Profile } from '../gen/agora/v1/agents_pb';
import type { Bridge } from '../gen/agora/v1/bridges_pb';
import type { ExternalAuthor, Message, Room } from '../gen/agora/v1/rooms_pb';
import { Icon } from '../icons';
import {
  addresseesById,
  atBottom,
  between,
  farFromBottom,
  firstUnread,
  landing,
  type Pair,
  pairs,
  type Reader,
  type Saved,
  seenThrough,
  tape,
  unseen,
} from '../tape';
import { ChoiceMenu } from './choice';
import { AgentLink, Avatar } from './common';
import { Composer } from './composer';
import { FollowControls } from './follow';
import { BoardLine, type Decide, Tablet } from './message';
import { OutsiderAvatar, OutsiderName } from './outside';
import { RoomIcon } from './rooms';

/** Why the room was opened from elsewhere: to show one message, or to write to someone. */
export type Arrival = { room: string; jump?: bigint; compose?: boolean };

type Pill = { n: number; forYou: number; first?: bigint; far: boolean };

function pageVisible(): boolean {
  return document.visibilityState === 'visible';
}

/** Smooth scrolling, unless the reader asked for less motion. */
function scrollBehavior(): ScrollBehavior {
  return typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth';
}

function scrollTape(el: HTMLElement, top: number) {
  if (el.scrollTo) el.scrollTo({ top, behavior: scrollBehavior() });
  else el.scrollTop = top;
}

export function RoomView(props: {
  name: string;
  room?: Room;
  /** The room's bridge, when it has one. */
  bridge?: Bridge;
  messages?: Message[];
  error?: string;
  reader: Reader;
  general: string;
  agents: Profile[];
  followed: boolean;
  /** How the operator follows the room; all when unknown. */
  mode?: Mode;
  /** The operator's unread count in the room when it was opened. */
  unread?: RoomCount;
  now: Date;
  draft: string;
  onDraft: (v: string) => void;
  onPost: (body: string, replyTo?: bigint) => Promise<void>;
  onFollow: (follow: boolean, mode?: Mode) => void;
  onSeen: (id: bigint) => void;
  /** Sends or declines a pending message. */
  onDecide: Decide;
  /** Changes what goes out through the room's bridge. */
  onPolicy: (p: Policy) => void;
  /** Where each room was left, kept while the app is open. */
  memory: Map<string, Saved>;
  arrival?: Arrival;
  onArrived: () => void;
}) {
  const { messages, reader, name } = props;
  const [focus, setFocus] = useState<[string, string]>();
  const [reply, setReply] = useState<Message>();
  const [backTo, setBackTo] = useState<number>();
  const [pill, setPill] = useState<Pill>({ n: 0, forYou: 0, far: false });
  // the NEW line moves to the first message that came while the page was hidden
  const [cameHidden, setCameHidden] = useState<bigint>();
  const list = useRef<HTMLOListElement>(null);
  const box = useRef<HTMLTextAreaElement>(null);
  const follow = useRef(true);
  const landed = useRef(false);
  const seen = useRef(0n);
  const frame = useRef(0);
  // the first unread message is fixed when the room's messages first arrive
  const entry = useRef<{ firstNew?: bigint }>(undefined);
  const known = useMemo(() => knownNames(props.agents, reader.operator), [props.agents, reader.operator]);
  if (!entry.current && messages)
    entry.current = {
      firstNew: firstUnread(
        messages,
        props.unread?.unread ?? 0,
        reader,
        props.followed && props.mode !== 'mentions',
        known,
      ),
    };
  const firstNew = cameHidden ?? entry.current?.firstNew;

  const live = useMemo(() => new Map(props.agents.map((a) => [a.name, livenessOf(a)])), [props.agents]);
  const to = useMemo(() => addresseesById(messages ?? [], known, reader.board), [messages, known, reader.board]);
  // the clock only matters for the day headings, so the tape is rebuilt once a day, not every tick
  const today = props.now.toDateString();
  const items = useMemo(
    () => tape(messages ?? [], to, reader, new Date(today), firstNew),
    [messages, to, reader, today, firstNew],
  );
  const people = useMemo(() => roomPeople(messages ?? [], reader, live), [messages, reader, live]);
  const outsiders = useMemo(() => roomOutsiders(messages ?? []), [messages]);
  const talks = useMemo(() => pairs(messages ?? [], to, reader.board), [messages, to, reader.board]);

  /** Counts what is in view as seen (only while the page is visible) and updates the pill. */
  const read = () => {
    const el = list.current;
    if (!el || !messages) return;
    if (pageVisible()) {
      const placed = [...el.querySelectorAll<HTMLElement>('[data-mid]')].map((li) => ({
        id: BigInt(li.dataset.mid ?? 0),
        top: li.offsetTop,
        height: li.offsetHeight,
      }));
      const through = seenThrough(placed, el.scrollTop + el.clientHeight);
      if (through !== undefined && through > seen.current) {
        seen.current = through;
        props.onSeen(through);
      }
    }
    const next = { ...unseen(messages, seen.current, to, reader.operator), far: farFromBottom(el) };
    setPill((p) =>
      p.n === next.n && p.forYou === next.forYou && p.first === next.first && p.far === next.far ? p : next,
    );
    props.memory.set(name, { top: el.scrollTop, bottom: atBottom(el) });
  };
  const reading = useRef(read);
  reading.current = read;

  // biome-ignore lint/correctness/useExhaustiveDependencies: placed once per new list of messages
  useLayoutEffect(() => {
    const el = list.current;
    if (!el || !messages) return;
    if (!landed.current) {
      landed.current = true;
      seen.current = firstNew !== undefined ? firstNew - 1n : (messages.at(-1)?.id ?? 0n);
      const saved = props.memory.get(name);
      const how = landing(firstNew !== undefined, saved);
      if (how === 'new') el.scrollTop = Math.max(0, (el.querySelector<HTMLElement>('.newline')?.offsetTop ?? 0) - 40);
      else if (how === 'saved' && saved) el.scrollTop = saved.top;
      else el.scrollTop = el.scrollHeight;
      follow.current = atBottom(el);
    } else if (follow.current && pageVisible()) el.scrollTop = el.scrollHeight;
    read();
  }, [messages]);

  // Coming back to the page: what came meanwhile gets the NEW line, and what is in view is read.
  const comeBack = () => {
    const first = unseen(messages ?? [], seen.current, to, reader.operator).first;
    if (first !== undefined) setCameHidden(first);
    read();
  };
  const returning = useRef(comeBack);
  returning.current = comeBack;
  useEffect(() => {
    const onChange = () => {
      if (pageVisible()) returning.current();
    };
    document.addEventListener('visibilitychange', onChange);
    return () => document.removeEventListener('visibilitychange', onChange);
  }, []);

  useEffect(() => () => cancelAnimationFrame(frame.current), []);
  const onScroll = () => {
    const el = list.current;
    if (!el) return;
    follow.current = atBottom(el);
    if (frame.current) return;
    frame.current = requestAnimationFrame(() => {
      frame.current = 0;
      reading.current();
    });
  };

  const flash = useCallback((id: bigint) => {
    const li = list.current?.querySelector<HTMLElement>(`[data-mid="${id}"]`);
    if (!li) return;
    li.scrollIntoView?.({ block: 'center', behavior: scrollBehavior() });
    li.classList.remove('flash');
    void li.offsetWidth; // restarts the animation
    li.classList.add('flash');
  }, []);

  // arriving from elsewhere: show the message asked for, or start writing; after the landing above
  // biome-ignore lint/correctness/useExhaustiveDependencies: runs when an arrival or the messages come
  useEffect(() => {
    const a = props.arrival;
    if (!a || a.room !== name || !landed.current) return;
    if (a.jump !== undefined) flash(a.jump);
    if (a.compose) {
      box.current?.focus();
      box.current?.setSelectionRange(props.draft.length, props.draft.length);
    }
    props.onArrived();
  }, [props.arrival, messages]);

  // Escape ends a reply, then a focused conversation; `/` goes to the compose box
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const typing = e.target instanceof HTMLElement && /^(INPUT|TEXTAREA)$/.test(e.target.tagName);
      if (e.key === 'Escape' && reply) setReply(undefined);
      else if (e.key === 'Escape' && focus) setFocus(undefined);
      else if (e.key === '/' && !typing) {
        e.preventDefault();
        box.current?.focus();
      }
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [reply, focus]);

  // the tablets are memoized: they get one callback that always calls the latest onDecide
  const deciding = useRef(props.onDecide);
  deciding.current = props.onDecide;
  const decide = useCallback<Decide>((m, send) => deciding.current(m, send), []);

  const startReply = useCallback((m: Message) => {
    setReply(m);
    box.current?.focus();
  }, []);
  const jump = useCallback(
    (id: bigint) => {
      setBackTo(list.current?.scrollTop);
      setFocus(undefined);
      flash(id);
    },
    [flash],
  );
  const back = () => {
    if (list.current && backTo !== undefined) scrollTape(list.current, backTo);
    setBackTo(undefined);
  };
  const latest = () => {
    const el = list.current;
    if (!el) return;
    const first = pill.first !== undefined ? el.querySelector<HTMLElement>(`[data-mid="${pill.first}"]`) : null;
    if (first && first.offsetTop > el.scrollTop + el.clientHeight - 40) scrollTape(el, first.offsetTop - 40);
    else scrollTape(el, el.scrollHeight);
  };
  const post = async (body: string) => {
    follow.current = true; // what the operator posts is shown
    await props.onPost(body, reply?.id);
    setReply(undefined);
  };

  const readOnly = props.bridge !== undefined && policyOf(props.bridge.policy) === 'read';
  const dimmed = (m: Message) => focus !== undefined && !between(m, to.get(m.id) ?? [], focus);
  const ctx = { known, operator: reader.operator };
  const inRoom = new Set(people.map((p) => p.name));

  return (
    <div className="roomwrap">
      <section className="room" aria-label={`#${name}`}>
        <div className="roomhead">
          <h2>
            <RoomIcon bridge={props.bridge} />#{name}
          </h2>
          <p className="purpose" title={props.room?.purpose}>
            {props.room?.purpose}
          </p>
          <div className="strip">
            {people.map((p) => (
              <AgentLink key={p.name} name={p.name} className="member">
                <Avatar name={p.name} live={p.live} size="sm" />
              </AgentLink>
            ))}
          </div>
          <span className="sum">
            {props.room?.messages ?? 0} msgs · {people.length} in room
          </span>
          {props.bridge && <BridgeControls room={name} bridge={props.bridge} onPolicy={props.onPolicy} />}
          <FollowControls
            room={name}
            always={name === props.general}
            followed={props.followed}
            mode={props.mode ?? 'all'}
            onFollow={props.onFollow}
          />
        </div>
        {focus && (
          <FocusBar
            pair={focus}
            n={(messages ?? []).filter((m) => between(m, to.get(m.id) ?? [], focus)).length}
            onEnd={() => setFocus(undefined)}
          />
        )}
        {props.error && (
          <p className="error" role="alert">
            {props.error}
          </p>
        )}
        <div className="tape">
          <ol className="msgs" ref={list} onScroll={onScroll}>
            {items.map((it) => {
              switch (it.kind) {
                case 'day':
                  return (
                    <li key={it.key} className="day">
                      {it.label}
                    </li>
                  );
                case 'new':
                  return (
                    <li key={it.key} className="newline">
                      NEW
                    </li>
                  );
                case 'board':
                  return <BoardLine key={it.key} m={it.message} dim={focus !== undefined} {...ctx} />;
                default:
                  return (
                    <Tablet
                      key={it.key}
                      item={it}
                      dim={dimmed(it.message)}
                      onReply={startReply}
                      onFocus={setFocus}
                      onJump={jump}
                      onDecide={decide}
                      readOnly={readOnly}
                      {...ctx}
                    />
                  );
              }
            })}
            {messages?.length === 0 && <li className="empty">No messages yet.</li>}
          </ol>
          <div className="pills">
            {backTo !== undefined && (
              <button type="button" className="pill quiet" onClick={back}>
                <Icon name="reply" />
                Back to where you were
              </button>
            )}
            {(pill.n > 0 || pill.far) && (
              <button type="button" className="pill latest" onClick={latest}>
                <Icon name="down" />
                {pill.n > 0 ? (
                  <>
                    <b>{pill.n} new</b>
                    {pill.forYou > 0 && ` · ${pill.forYou} for you`}
                  </>
                ) : (
                  'Latest'
                )}
              </button>
            )}
          </div>
        </div>
        <Composer
          room={name}
          operator={reader.operator}
          agents={props.agents}
          inRoom={inRoom}
          draft={props.draft}
          onDraft={props.onDraft}
          reply={reply}
          onCancelReply={() => setReply(undefined)}
          onPost={post}
          inputRef={box}
        />
      </section>
      <aside className="rail" aria-label="Who is here">
        <div>
          <h3>CONVERSATIONS</h3>
          <Conversations talks={talks} focus={focus} onFocus={setFocus} />
        </div>
        <div>
          <h3>IN ROOM · {people.length}</h3>
          <div className="people">
            {people.map((p) => (
              <AgentLink key={p.name} name={p.name} className="person">
                <Avatar name={p.name} live={p.live} size="sm" />
                <span className="pgname">{p.name}</span>
                {p.name === reader.operator && <span className="r">you</span>}
              </AgentLink>
            ))}
          </div>
        </div>
        {outsiders.length > 0 && (
          <div className="outsiders">
            <h3>OUTSIDE · {outsiders.length}</h3>
            <div className="people">
              {outsiders.map((who) => (
                <OutsiderName
                  key={authorKey({ author: '', externalAuthor: who })}
                  who={who}
                  className="person"
                  via={false}
                >
                  <OutsiderAvatar who={who} size="sm" />
                  <span className="pgname">{who.name || who.id}</span>
                </OutsiderName>
              ))}
            </div>
          </div>
        )}
      </aside>
    </div>
  );
}

/** The people outside who wrote the loaded messages, by name. */
function roomOutsiders(messages: Message[]): ExternalAuthor[] {
  const found = new Map<string, ExternalAuthor>();
  for (const m of messages) if (m.externalAuthor) found.set(authorKey(m), m.externalAuthor);
  return [...found.values()].sort((a, b) => (a.name || a.id).localeCompare(b.name || b.id));
}

type Person = { name: string; live?: Liveness };

/** Who is in the room: the authors of its loaded messages and the operator, by liveness and name, the operator last. */
function roomPeople(messages: Message[], reader: Reader, live: Map<string, Liveness>): Person[] {
  const names = new Set(
    messages
      .filter((m) => !m.externalAuthor) // people outside are not agents
      .map((m) => m.author)
      .filter((a) => a !== reader.board && a !== reader.operator),
  );
  const rank = (n: string) => livenessOrder[live.get(n) ?? 'offline'];
  const agents = [...names].sort((a, b) => rank(a) - rank(b) || a.localeCompare(b));
  return [...agents, reader.operator].map((name) => ({
    name,
    live: name === reader.operator ? undefined : (live.get(name) ?? 'offline'),
  }));
}

/** A bridged room's state and its outbound policy, which only the operator changes. */
function BridgeControls(props: { room: string; bridge: Bridge; onPolicy: (p: Policy) => void }) {
  const running = runningOf(props.bridge);
  return (
    <div className="bridge">
      <span className={`brstate ${running}`} title={`Bridge ${runningText(props.bridge)}`}>
        {running === 'running' ? 'bridged' : `bridge ${running}`}
      </span>
      <ChoiceMenu
        name="Outbound"
        shown
        id={`outbound-${props.room}`}
        labels={POLICY_LABELS}
        value={policyOf(props.bridge.policy)}
        onChoose={props.onPolicy}
      />
    </div>
  );
}

function FocusBar({ pair, n, onEnd }: { pair: [string, string]; n: number; onEnd: () => void }) {
  const [a, b] = pair;
  return (
    <div className="focusbar">
      <Avatar name={a} size="sm" />
      <AgentLink name={a} /> ⇄ <Avatar name={b} size="sm" />
      <AgentLink name={b} />
      <span className="sum">
        {n} {n === 1 ? 'message' : 'messages'} between them
      </span>
      <span className="grow" />
      <button type="button" className="btn quiet" onClick={onEnd}>
        Show everyone
      </button>
    </div>
  );
}

function Conversations(props: {
  talks: Pair[];
  focus?: [string, string];
  onFocus: (pair: [string, string] | undefined) => void;
}) {
  if (props.talks.length === 0) return <p className="empty">Nobody has addressed anyone yet.</p>;
  return (
    <div className="pairs">
      {props.talks.map((p) => {
        const on = props.focus?.includes(p.a) && props.focus.includes(p.b);
        return (
          <button
            key={`${p.a} ${p.b}`}
            type="button"
            className="pair"
            aria-pressed={!!on}
            onClick={() => props.onFocus(on ? undefined : [p.a, p.b])}
          >
            <Avatar name={p.a} size="sm" />
            <Avatar name={p.b} size="sm" />
            <span className="nm">
              {p.a} ⇄ {p.b}
            </span>
            <span className="k">{p.n}</span>
          </button>
        );
      })}
    </div>
  );
}
