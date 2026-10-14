// A room: its messages on the tape, who talks with whom, and the compose box. The tape follows
// new messages only while the operator is at the bottom; see tape.ts for the decisions.
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import {
  knownNames,
  type Liveness,
  liveness as livenessOf,
  livenessOrder,
  type Mode,
  type RoomCount,
  toDate,
} from '../board';
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
import { BoardLine, type Decide, type SendOut, Tablet } from './message';
import { OutsiderAvatar, OutsiderName } from './outside';
import { RoomIcon, shortDate } from './rooms';
import { Notice } from './shell';
import { onPhone, ThreadPanel } from './thread';

/** The thread open beside the room, and what the operator does in it. */
export type OpenThread = {
  root: bigint;
  /** The root, then its replies; undefined while loading. */
  messages?: Message[];
  error?: string;
  /** Posts a reply, also to the room when asked; a note stays in Agora. */
  onPost: (body: string, also: boolean, note: boolean) => Promise<void>;
  onTitle: (title: string) => void;
  onLeave: () => void;
  onSeen: (id: bigint) => void;
};

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
  /** In a bridged room: the next message is a note for the agents; kept with the draft. */
  note: boolean;
  onNote: (note: boolean) => void;
  /** Posts as the operator; a note stays in Agora (only a bridged room has notes). */
  onPost: (body: string, replyTo: bigint | undefined, note: boolean) => Promise<void>;
  onFollow: (follow: boolean, mode?: Mode) => void;
  onSeen: (id: bigint) => void;
  /** Sends a scheduled message at once or cancels it. */
  onDecide: Decide;
  /** Sends a note out. */
  onSendOut: SendOut;
  /** Changes what goes out through the room's bridge. */
  onPolicy: (p: Policy) => void;
  /** Archives the room, or brings it back. */
  onArchive: (archived: boolean) => void;
  /** A refused call to tell under the header, numbered so a repeat shows for its full time. */
  notice?: { text: string; n: number };
  onCloseNotice?: () => void;
  /** Where each room was left, kept while the app is open. */
  memory: Map<string, Saved>;
  arrival?: Arrival;
  onArrived: () => void;
  /** The thread open beside the room, if one is. */
  thread?: OpenThread;
  /** The operator's unread replies in the room's threads now, which the room's own unread leaves out. */
  threadUnread?: number;
  /** The room's threads the hub lists with unread replies for the operator. */
  unreadThreads?: { root: bigint; unread: number }[];
  /** Opens the thread of a root, or closes the open one. */
  onThread?: (root: bigint | undefined) => void;
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
  const threadOpen = useRef(props.thread !== undefined);
  threadOpen.current = props.thread !== undefined;
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
    // on a phone an open thread covers the room: nothing in it is seen until the thread closes
    if (pageVisible() && !(threadOpen.current && onPhone())) {
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
    if (first !== undefined) {
      setCameHidden(first);
      // the NEW line above what came meanwhile stays in view, unless nothing is below the view
      if (list.current) follow.current = atBottom(list.current);
    }
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
  // a tape that showed the newest message keeps showing it when it gets shorter, as when the
  // on-screen keyboard comes up: the browser keeps its scroll position, not its bottom
  useEffect(() => {
    const el = list.current;
    if (!el || typeof ResizeObserver === 'undefined') return;
    let height = el.clientHeight;
    const keep = new ResizeObserver(() => {
      const shorter = el.clientHeight < height;
      height = el.clientHeight;
      if (shorter && follow.current) el.scrollTop = el.scrollHeight;
    });
    keep.observe(el);
    return () => keep.disconnect();
  }, []);
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
    // the tape follows only if it ends at the bottom: a resize must not pull it away from the message
    const el = list.current;
    if (el) {
      const top = Math.min(
        Math.max(0, li.offsetTop + li.offsetHeight / 2 - el.clientHeight / 2),
        el.scrollHeight - el.clientHeight,
      );
      follow.current = atBottom({ scrollHeight: el.scrollHeight, clientHeight: el.clientHeight, scrollTop: top });
    }
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
  const sending = useRef(props.onSendOut);
  sending.current = props.onSendOut;
  const sendOut = useCallback<SendOut>((m) => sending.current(m), []);

  // a reply to a note is a note too, unless the operator chooses otherwise
  const noting = useRef(props.onNote);
  noting.current = props.onNote;
  const startReply = useCallback((m: Message) => {
    setReply(m);
    if (m.note) noting.current(true);
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
  const readOnly = props.bridge !== undefined && policyOf(props.bridge.policy) === 'read';
  const archived = props.room?.archivedBy ? props.room : undefined;
  // once the room the operator archived here is archived, the focus goes to Unarchive
  const archiving = useRef(false);
  const unarchive = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (!archived || !archiving.current) return;
    archiving.current = false;
    unarchive.current?.focus();
  }, [archived]);
  // nothing goes out of a read-only room, so everything the operator writes there is a note, and
  // stays one when Outbound opens again; the flag goes with every post, the hub ignores it outside
  // a bridged room
  const note = props.note || readOnly;
  // biome-ignore lint/correctness/useExhaustiveDependencies: stored once the room is or becomes read only
  useEffect(() => {
    if (readOnly && !props.note) props.onNote(true);
  }, [readOnly]);
  const post = async (body: string) => {
    follow.current = true; // what the operator posts is shown
    await props.onPost(body, reply?.id, note);
    setReply(undefined);
  };

  const opening = useRef(props.onThread);
  opening.current = props.onThread;
  // what opened the thread gets the focus back when it closes
  const opener = useRef<HTMLElement | null>(null);
  // the tablets are memoized: they get callbacks that stay the same
  const openThread = useCallback((root: bigint) => {
    opener.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    opening.current?.(root);
  }, []);
  const closeThread = useCallback(() => opening.current?.(undefined), []);
  const section = useRef<HTMLElement>(null);
  const isOpen = props.thread !== undefined;
  useEffect(() => {
    // on a phone the room under an open thread is out of reach, also for the keyboard
    const el = section.current;
    el?.toggleAttribute('inert', isOpen && onPhone());
    if (isOpen) return;
    reading.current(); // what the thread covered is seen now
    if (opener.current?.isConnected) opener.current.focus();
    opener.current = null;
  }, [isOpen]);
  const dimmed = (m: Message) => focus !== undefined && !between(m, to.get(m.id) ?? [], focus);
  const ctx = { known, operator: reader.operator };
  const inRoom = new Set(people.map((p) => p.name));

  return (
    <div className={props.thread ? 'roomwrap withthread' : 'roomwrap'}>
      <section className="room" ref={section} aria-label={`#${name}`}>
        <div className={archived ? 'roomhead archived' : 'roomhead'}>
          <div className="rtitle">
            <h2>
              {archived ? <Icon name="archive" /> : <RoomIcon bridge={props.bridge} />}#{name}
            </h2>
            {archived && <span className="badge">archived</span>}
            <p className="purpose" title={props.room?.purpose}>
              {props.room?.purpose}
            </p>
          </div>
          <div className="rtools">
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
            {(props.threadUnread ?? 0) > 0 && props.onThread && (
              <UnreadInThreads
                n={props.threadUnread ?? 0}
                next={nextUnreadThread(props.unreadThreads ?? [], messages ?? [], props.thread?.root)}
                onOpen={openThread}
              />
            )}
            {props.bridge && <BridgeControls room={name} bridge={props.bridge} onPolicy={props.onPolicy} />}
            {!archived && (
              <FollowControls
                room={name}
                always={name === props.general}
                followed={props.followed}
                mode={props.mode ?? 'all'}
                onFollow={props.onFollow}
              />
            )}
            {!archived && props.room && name !== props.general && !props.bridge && (
              <ArchiveControl
                room={name}
                onArchive={() => {
                  archiving.current = true;
                  props.onArchive(true);
                }}
              />
            )}
          </div>
        </div>
        {props.notice && props.onCloseNotice && (
          <Notice key={props.notice.n} text={props.notice.text} onClose={props.onCloseNotice} />
        )}
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
                      onReply={archived ? undefined : startReply}
                      onFocus={setFocus}
                      onJump={jump}
                      onDecide={decide}
                      onSendOut={props.bridge && !readOnly && !archived ? sendOut : undefined}
                      held={readOnly ? 'read' : props.bridge ? undefined : 'unbridged'}
                      onThread={props.onThread && openThread}
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
        {archived ? (
          <div className="archivedpanel">
            <p>
              <b>ARCHIVED</b> — by {archived.archivedBy} on {shortDate(toDate(archived.archivedAt), props.now)}. New
              messages are not accepted, nobody is woken.
            </p>
            <button type="button" className="btn" ref={unarchive} onClick={() => props.onArchive(false)}>
              Unarchive
            </button>
          </div>
        ) : (
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
            outbound={props.bridge && { chat: props.bridge.name, note, readOnly, onNote: props.onNote }}
          />
        )}
      </section>
      {props.thread && props.onThread ? (
        <ThreadPanel
          key={String(props.thread.root)}
          archived={archived !== undefined}
          bridge={
            props.bridge && {
              chat: props.bridge.name,
              readOnly,
              onDecide: decide,
              onSendOut: !readOnly && !archived ? sendOut : undefined,
              held: readOnly ? 'read' : undefined,
            }
          }
          room={name}
          root={props.thread.root}
          messages={props.thread.messages}
          error={props.thread.error}
          operator={reader.operator}
          board={reader.board}
          agents={props.agents}
          known={known}
          onClose={closeThread}
          onPost={props.thread.onPost}
          onTitle={props.thread.onTitle}
          onLeave={props.thread.onLeave}
          onSeen={props.thread.onSeen}
        />
      ) : (
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
      )}
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

/**
 * The next thread with unread replies to open, other than the open one: those the hub lists, then
 * the roots in the loaded history, oldest root first.
 */
export function nextUnreadThread(
  listed: { root: bigint; unread: number }[],
  messages: Message[],
  open: bigint | undefined,
): bigint | undefined {
  const roots = new Set<bigint>();
  for (const t of listed) if (t.unread > 0) roots.add(t.root);
  for (const m of messages) if ((m.threadSummary?.unread ?? 0) > 0) roots.add(m.id);
  roots.delete(open ?? -1n);
  return [...roots].sort((a, b) => (a < b ? -1 : a > b ? 1 : 0))[0];
}

/** The unread replies in the room's threads; choosing it opens the next thread that has some. */
function UnreadInThreads({ n, next, onOpen }: { n: number; next?: bigint; onOpen: (root: bigint) => void }) {
  return (
    <button
      type="button"
      className="chip mention"
      disabled={next === undefined}
      title={
        next === undefined
          ? 'Open the thread from its strip or the room list'
          : 'Open the next thread with unread replies'
      }
      onClick={() => next !== undefined && onOpen(next)}
    >
      <Icon name="thread" />
      {n} unread in threads
    </button>
  );
}

/** Archive room, asked again in the page before the room is archived. */
function ArchiveControl(props: { room: string; onArchive: () => void }) {
  const [asking, setAsking] = useState(false);
  const cancel = useRef<HTMLButtonElement>(null);
  const again = useRef<HTMLButtonElement>(null);
  const was = useRef(false);
  // the focus goes to Cancel when asked, and back to Archive room when the question closes
  useEffect(() => {
    if (asking) cancel.current?.focus();
    else if (was.current) again.current?.focus();
    was.current = asking;
  }, [asking]);
  if (!asking) {
    return (
      <button type="button" className="chip" ref={again} onClick={() => setAsking(true)}>
        Archive room
      </button>
    );
  }
  return (
    // biome-ignore lint/a11y/useSemanticElements: a fieldset would bring a form's border and legend into the header
    <div
      className="ask"
      role="group"
      aria-label={`Archive #${props.room}?`}
      onKeyDown={(e) => {
        if (e.key !== 'Escape') return;
        e.stopPropagation(); // the room's own Escape would also end a reply
        setAsking(false);
      }}
    >
      <span>Archive #{props.room}?</span>
      <button
        type="button"
        className="chip warn"
        onClick={() => {
          setAsking(false);
          props.onArchive();
        }}
      >
        Archive
      </button>
      <button type="button" className="chip" ref={cancel} onClick={() => setAsking(false)}>
        Cancel
      </button>
    </div>
  );
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
