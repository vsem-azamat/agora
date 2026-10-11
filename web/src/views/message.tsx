// One message on the tape: a tablet edged in its author's pigment, or the board's own thin line.
// In a bridged room a tablet also shows where the message stands on its way out, or that it is a
// note, which stays in Agora.
import { Fragment, memo, useEffect, useState } from 'react';
import type { Names } from '../board';
import { clock, toDate } from '../board';
import { authorName, type Delivery, deliveryOf } from '../bridges';
import type { Message } from '../gen/agora/v1/rooms_pb';
import { Icon } from '../icons';
import type { TapeItem } from '../tape';
import { AgentLink, Avatar, MessageBody, usePg } from './common';
import { OUTSIDE, OutsiderAvatar, OutsiderName } from './outside';

type Context = { known: Names; operator: string };

/** The operator's decision on a scheduled message: send it at once or cancel it; true once the hub took it. */
export type Decide = (m: Message, send: boolean) => Promise<boolean>;

/** Why a scheduled message cannot go out now: the room is read only, or has no bridge any more. */
export type Held = 'read' | 'unbridged';

const HELD: Record<Held, string> = { read: 'held while read only', unbridged: 'held: no bridge' };

/** The operator sends a note out; true once the hub took it. */
export type SendOut = (m: Message) => Promise<boolean>;

/** How long until a scheduled message goes out, counted every second: `goes out in 0:42`. */
function Countdown({ due }: { due: Date | undefined }) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!due) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [due]);
  if (!due) return <span className="why">scheduled</span>;
  const left = Math.ceil((due.getTime() - now) / 1000);
  if (left <= 0) return <span className="why">going out…</span>;
  return (
    <span className="why" title={`Goes out at ${clock(due)}`}>
      goes out in {Math.floor(left / 60)}:{String(left % 60).padStart(2, '0')}
    </span>
  );
}

function Time({ m }: { m: Message }) {
  const at = toDate(m.at);
  return <time title={at?.toLocaleString('en-GB')}>{clock(at)}</time>;
}

export const BoardLine = memo(function BoardLine({ m, dim, ...ctx }: Context & { m: Message; dim: boolean }) {
  return (
    <li className={dim ? 'sys dim' : 'sys'} data-mid={String(m.id)}>
      <Icon name="hub" title="the board" />
      <span>
        <MessageBody body={m.body} {...ctx} />
      </span>
      <Time m={m} />
    </li>
  );
});

/** Where a message that goes out stands, next to its time: sending, sent or not sent. */
function DeliveryMark({ d, room }: { d: Delivery | undefined; room: string }) {
  switch (d) {
    case 'sending':
      return <span className="dl sending">sending…</span>;
    case 'sent':
      return (
        <span className="dl sent" title={`Sent to ${room}`}>
          <Icon name="check" />
          <span className="sr">sent</span>
        </span>
      );
    case 'declined':
    case 'failed':
      return <span className={`dl ${d}`}>not sent</span>;
    default:
      return null;
  }
}

/**
 * A scheduled message's countdown and buttons: it goes out when its time comes, unless the
 * operator cancels it, or at once when the operator sends it. While the room is read only it is
 * held and cannot be sent, only cancelled.
 */
function Decision({ m, onDecide, held }: { m: Message; onDecide: Decide; held?: Held }) {
  const [busy, setBusy] = useState(false);
  const decide = (send: boolean) => {
    setBusy(true);
    // after a decision the hub took, the buttons stay off until the message stops waiting
    void onDecide(m, send).then((took) => {
      if (!took) setBusy(false);
    });
  };
  return (
    <fieldset className="decide" aria-label={`Send the message of ${m.author} to ${m.room}?`}>
      {held ? <span className="why">{HELD[held]}</span> : <Countdown due={toDate(m.dueAt)} />}
      {held === 'read' && <span className="hint">read only — switch Outbound to send</span>}
      <button type="button" className="btn" disabled={busy || held !== undefined} onClick={() => decide(true)}>
        <Icon name="send" />
        Send now
      </button>
      <button type="button" className="btn quiet" disabled={busy} onClick={() => decide(false)}>
        Cancel
      </button>
    </fieldset>
  );
}

/**
 * Send out on a note: off while the call runs, on again when the hub refused it. Inline, it is the
 * phone's, next to its inline reply.
 */
function SendOutButton({ m, onSendOut, inline }: { m: Message; onSendOut: SendOut; inline?: boolean }) {
  const [busy, setBusy] = useState(false);
  return (
    <button
      type="button"
      className={inline ? 'mreply' : undefined}
      disabled={busy}
      title={`Send this note to ${m.room} as a message of ${m.author}`}
      onClick={() => {
        setBusy(true);
        void onSendOut(m).then(() => setBusy(false));
      }}
    >
      {inline ? (
        'send out'
      ) : (
        <>
          <Icon name="send" />
          Send out
        </>
      )}
    </button>
  );
}

/** A tablet; memoized, so the tape re-renders only what changed. */
export const Tablet = memo(function Tablet({
  item,
  dim,
  onReply,
  onFocus,
  onJump,
  onDecide,
  onSendOut,
  held,
  ...ctx
}: Context & {
  item: Extract<TapeItem, { kind: 'message' }>;
  dim: boolean;
  /** Starts a reply; none in a room that takes no messages. */
  onReply?: (m: Message) => void;
  onFocus: (pair: [string, string]) => void;
  onJump: (id: bigint) => void;
  onDecide: Decide;
  /** Sends a note out; none while nothing goes out of the room. */
  onSendOut?: SendOut;
  /** Why nothing goes out of the room now, if nothing does. */
  held?: Held;
}) {
  const pg = usePg();
  const { message: m, parent, to } = item;
  const ext = m.externalAuthor;
  // a note never goes out, so it has no state on the way out to show
  const d = m.note ? undefined : deliveryOf(m);
  // someone outside is not an agent: no conversation to focus on
  const first = ext ? undefined : to[0];
  const cls = [
    'msg',
    item.cont && 'cont',
    item.toYou && 'tome',
    item.waiting && 'waiting',
    item.own && 'own',
    ext && 'outside',
    d === 'declined' && 'declined',
    m.note && 'note',
    dim && 'dim',
  ].filter(Boolean);
  return (
    <li className={cls.join(' ')} data-mid={String(m.id)} style={ext ? OUTSIDE : pg(m.author)}>
      {ext ? <OutsiderAvatar who={ext} /> : <Avatar name={m.author} />}
      <div className="mb">
        <div className="mh">
          {ext ? <OutsiderName who={ext} /> : <AgentLink name={m.author} className="au" />}
          {to.length > 0 && (
            <span className="to">
              →{' '}
              {to.map((t, i) => (
                <Fragment key={t}>
                  {i > 0 && ', '}
                  {ext ? (
                    <span style={pg(t)}>{t}</span>
                  ) : (
                    <button
                      type="button"
                      style={pg(t)}
                      title={`Show only ${m.author} ⇄ ${t}`}
                      onClick={() => onFocus([m.author, t])}
                    >
                      {t}
                    </button>
                  )}
                </Fragment>
              ))}
            </span>
          )}
          <Time m={m} />
          {m.note && (
            <span className="nt" title={`Not sent to ${m.room}`}>
              note · stays in Agora
            </span>
          )}
          <DeliveryMark d={d} room={m.room} />
          {item.toYou && <span className="tag">TO YOU</span>}
          {onReply && (
            <button type="button" className="mreply" onClick={() => onReply(m)}>
              reply
            </button>
          )}
          {m.note && onSendOut && <SendOutButton m={m} onSendOut={onSendOut} inline />}
        </div>
        {parent && (
          <button
            type="button"
            className="quote"
            style={parent.externalAuthor ? OUTSIDE : pg(parent.author)}
            onClick={() => onJump(parent.id)}
          >
            <Icon name="reply" />
            <span className="qa">{authorName(parent)}</span>
            <span className="qt">{parent.body}</span>
          </button>
        )}
        <div className="tx">
          <MessageBody body={m.body} {...ctx} />
        </div>
        {d === 'pending' && <Decision m={m} onDecide={onDecide} held={held} />}
        {d === 'failed' && <p className="dlerr">Not sent{m.deliveryError ? `: ${m.deliveryError}` : ''}</p>}
      </div>
      <div className="acts">
        {onReply && (
          <button type="button" onClick={() => onReply(m)}>
            <Icon name="reply" />
            Reply
          </button>
        )}
        {first !== undefined && (
          <button type="button" onClick={() => onFocus([m.author, first])}>
            <Icon name="thread" />
            Thread
          </button>
        )}
        {m.note && onSendOut && <SendOutButton m={m} onSendOut={onSendOut} />}
      </div>
    </li>
  );
});
