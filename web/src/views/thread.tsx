// Threads: the strip under a message whose thread has replies, and the thread itself, in a panel
// beside the room on wide screens and over the whole screen on a phone.
import { type FormEvent, type KeyboardEvent, useEffect, useMemo, useRef, useState } from 'react';
import { clock, type Names, toDate } from '../board';
import { authorName, deliveryOf } from '../bridges';
import type { Profile } from '../gen/agora/v1/agents_pb';
import type { Message } from '../gen/agora/v1/rooms_pb';
import { Icon } from '../icons';
import { atBottom } from '../tape';
import { AgentLink, Avatar, MessageBody, usePg } from './common';
import { Composer } from './composer';
import { type Decide, Decision, DeliveryMark, type Held, type SendOut, SendOutButton } from './message';
import { OUTSIDE, OutsiderAvatar, OutsiderName } from './outside';
import { roomHref } from './rooms';

/** The address of a room with the thread of root open. */
export const threadHref = (room: string, root: bigint) => `${roomHref(room)}/thread/${root}`;

/** The longest thread title, in characters, as the hub takes it (rooms.MaxTitle). */
export const MAX_TITLE = 100;

/** Where the panel covers the whole screen, as the styles say. */
export const PHONE = '(max-width: 760px)';

/** Whether the screen is a phone's, where the thread covers the room. */
export function onPhone(): boolean {
  return typeof matchMedia === 'function' && matchMedia(PHONE).matches;
}

/** The root of the thread a message belongs to: the thread it is a reply in, or its own. */
export const rootOf = (m: Message) => (m.thread !== 0n ? m.thread : m.id);

/** The strip under a message with replies in its thread; choosing it opens the thread. */
export function ThreadStrip({ m, onOpen }: { m: Message; onOpen: (root: bigint) => void }) {
  const sum = m.threadSummary;
  if (!sum || sum.replies === 0) return null;
  const last = toDate(sum.lastAt);
  const count = `${sum.replies} ${sum.replies === 1 ? 'reply' : 'replies'}`;
  // the label says all the strip shows, for a screen reader
  const label = [
    `Open the thread${m.threadTitle ? ` «${m.threadTitle}»` : ''}: ${count} from ${sum.repliers.join(', ')}`,
    last && `the last at ${clock(last)}`,
    sum.unread > 0 && `${sum.unread} new`,
  ]
    .filter(Boolean)
    .join(', ');
  return (
    <button type="button" className="tstrip" onClick={() => onOpen(m.id)} aria-label={label}>
      <Icon name="thread" />
      <b>{count}</b>
      <span className="who">
        {sum.repliers.slice(0, 3).map((r) =>
          r.includes('@') ? (
            <span key={r} className="av sm out" title={r}>
              {r.charAt(0).toUpperCase()}
            </span>
          ) : (
            <Avatar key={r} name={r} size="sm" />
          ),
        )}
        <span className="names">{sum.repliers.join(', ')}</span>
      </span>
      {last && <time>{clock(last)}</time>}
      {sum.unread > 0 && <em className="mention">{sum.unread} new</em>}
      {m.threadTitle && <span className="ttl">«{m.threadTitle}»</span>}
      <span className="open">open</span>
    </button>
  );
}

/** How the panel acts on a reply that goes out of a bridged room; none outside one. */
type Delivering = { onDecide: Decide; onSendOut?: SendOut; held?: Held };

function ThreadMessage({
  m,
  known,
  operator,
  delivering,
}: {
  m: Message;
  known: Names;
  operator: string;
  delivering?: Delivering;
}) {
  const pg = usePg();
  const ext = m.externalAuthor;
  const d = deliveryOf(m);
  return (
    <li
      className={['tmsg', m.author === operator && 'own', m.note && 'note', d === 'cancelled' && 'cancelled']
        .filter(Boolean)
        .join(' ')}
      data-tid={String(m.id)}
      style={ext ? OUTSIDE : pg(m.author)}
    >
      {ext ? <OutsiderAvatar who={ext} /> : <Avatar name={m.author} />}
      <div className="mb">
        <div className="mh">
          {ext ? <OutsiderName who={ext} /> : <AgentLink name={m.author} className="au" />}
          <time>{clock(toDate(m.at))}</time>
          {m.also && <span className="nt">also in the room</span>}
          {m.note && (
            <span className="nt" title={`Not sent to ${m.room}`}>
              note · stays in Agora
            </span>
          )}
          <DeliveryMark d={d} room={m.room} />
        </div>
        <div className="tx">
          <MessageBody body={m.body} known={known} operator={operator} />
        </div>
        {d === 'scheduled' && delivering && <Decision m={m} onDecide={delivering.onDecide} held={delivering.held} />}
        {d === 'failed' && <p className="dlerr">Not sent{m.deliveryError ? `: ${m.deliveryError}` : ''}</p>}
        {m.note && delivering?.onSendOut && <SendOutButton m={m} onSendOut={delivering.onSendOut} inline />}
      </div>
    </li>
  );
}

function TitleForm({ title, onTitle }: { title: string; onTitle: (t: string) => void }) {
  const [editing, setEditing] = useState(false);
  const [text, setText] = useState(title);
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (editing) input.current?.focus();
  }, [editing]);
  if (!editing)
    return (
      <button
        type="button"
        className="chip"
        onClick={() => {
          setText(title);
          setEditing(true);
        }}
      >
        {title ? 'Rename' : 'Give a title'}
      </button>
    );
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!text.trim()) return;
    onTitle(text.trim());
    setEditing(false);
  };
  return (
    <form className="ttlform" onSubmit={submit}>
      <input
        ref={input}
        aria-label="Thread title"
        value={text}
        onChange={(e) => setText([...e.target.value].slice(0, MAX_TITLE).join(''))}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            e.stopPropagation();
            setEditing(false);
          }
        }}
      />
      <button type="submit" className="chip">
        Save
      </button>
    </form>
  );
}

/** The thread of a message: the path to its room, the root and its replies, and a compose box. */
export function ThreadPanel(props: {
  room: string;
  root: bigint;
  /** The root, then the replies, oldest first; undefined while loading. */
  messages?: Message[];
  error?: string;
  operator: string;
  board: string;
  agents: Profile[];
  known: Names;
  /** The room is archived: the thread is read only. */
  archived?: boolean;
  /** In a bridged room: its chat, whether nothing goes out, and how replies going out are acted on. */
  bridge?: { chat: string; readOnly: boolean } & Delivering;
  onClose: () => void;
  /** Posts a reply, also to the room when asked; a note stays in Agora. */
  onPost: (body: string, also: boolean, note: boolean) => Promise<void>;
  onTitle: (title: string) => void;
  onLeave: () => void;
  /** The newest reply shown, to count as read. */
  onSeen: (id: bigint) => void;
}) {
  const { messages } = props;
  const [draft, setDraft] = useState('');
  const [also, setAlso] = useState(false);
  const [noteChosen, setNote] = useState(false);
  // in a read-only bridged room every reply stays in Agora
  const note = props.bridge !== undefined && (noteChosen || props.bridge.readOnly);
  const box = useRef<HTMLTextAreaElement>(null);
  const list = useRef<HTMLOListElement>(null);
  const back = useRef<HTMLButtonElement>(null);
  // the replies follow the newest one while the operator reads at the bottom or has just posted
  const follow = useRef(true);
  const root = messages?.[0];
  const title = root?.threadTitle ?? '';
  // who a reply wakes: the thread's participants as the hub counts them, without the operator
  const wakes = useMemo(
    () => (root?.threadSummary?.participants ?? []).filter((p) => p !== props.operator),
    [root, props.operator],
  );
  const last = messages && messages.length > 1 ? messages.at(-1)?.id : undefined;
  const seen = useRef(props.onSeen);
  seen.current = props.onSeen;
  useEffect(() => {
    if (last !== undefined) seen.current(last);
    const el = list.current;
    if (el && follow.current) el.scrollTop = el.scrollHeight;
  }, [last]);
  // on a phone the panel covers the room: the focus moves into it
  useEffect(() => {
    if (onPhone()) back.current?.focus();
  }, []);
  // Escape closes the thread, unless it is pressed in a text field, where it would lose the draft;
  // the room's own Escape does not also run
  const onKey = (e: KeyboardEvent) => {
    if (e.key !== 'Escape') return;
    e.stopPropagation();
    const t = e.target;
    if (t instanceof HTMLElement && /^(INPUT|TEXTAREA)$/.test(t.tagName)) return;
    props.onClose();
  };
  const inRoom = useMemo(() => new Set(wakes), [wakes]);
  const post = async (body: string) => {
    follow.current = true; // what the operator posts is shown
    await props.onPost(body, also, note);
    setDraft('');
  };
  return (
    <aside className="threadpanel" aria-label={title ? `Thread «${title}»` : 'Thread'} onKeyDown={onKey}>
      <div className="thead">
        <button type="button" className="tback" ref={back} onClick={props.onClose} aria-label="Back to the room">
          <Icon name="back" />
        </button>
        <p className="tpath">
          <a href={roomHref(props.room)}>#{props.room}</a> ›{' '}
          <b>{title || (root ? `thread of ${authorName(root)}` : 'thread')}</b>
        </p>
        <div className="ttools">
          {!props.archived && <TitleForm title={title} onTitle={props.onTitle} />}
          {!props.archived && (
            <button type="button" className="chip" onClick={props.onLeave}>
              Leave
            </button>
          )}
          <button type="button" className="tclose btn quiet" onClick={props.onClose} aria-label="Close the thread">
            <Icon name="close" />
          </button>
        </div>
      </div>
      {!props.archived && (
        <p className="wakes">
          {wakes.length > 0 ? (
            <>
              A reply wakes <b>{wakes.join(', ')}</b>
            </>
          ) : (
            'A reply wakes nobody yet; mention someone to bring them in'
          )}
        </p>
      )}
      {props.error && (
        <p className="error" role="alert">
          {props.error}
        </p>
      )}
      <ol
        className="tmsgs"
        ref={list}
        onScroll={(e) => {
          follow.current = atBottom(e.currentTarget);
        }}
      >
        {(messages ?? []).flatMap((m, i) => [
          ...(i === 1
            ? [
                <li key="replies" className="day">
                  REPLIES
                </li>,
              ]
            : []),
          <ThreadMessage
            key={String(m.id)}
            m={m}
            known={props.known}
            operator={props.operator}
            delivering={props.bridge}
          />,
        ])}
        {messages?.length === 1 && <li className="empty">No replies yet.</li>}
      </ol>
      {props.archived ? (
        <p className="archivedpanel">The room is archived: the thread is read only.</p>
      ) : (
        <>
          <label className="also">
            <input type="checkbox" checked={also} onChange={(e) => setAlso(e.target.checked)} />
            Also to the room
          </label>
          <Composer
            room={props.room}
            operator={props.operator}
            agents={props.agents}
            inRoom={inRoom}
            draft={draft}
            onDraft={setDraft}
            onCancelReply={() => {}}
            onPost={post}
            inputRef={box}
            label={`Reply in the thread #${props.room}`}
            hint={props.bridge ? undefined : 'Reply in the thread · @ to address someone'}
            outbound={
              props.bridge && {
                chat: props.bridge.chat,
                note,
                readOnly: props.bridge.readOnly,
                onNote: setNote,
              }
            }
          />
        </>
      )}
    </aside>
  );
}
