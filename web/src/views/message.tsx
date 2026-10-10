// One message on the tape: a tablet edged in its author's pigment, or the board's own thin line.
// In a bridged room a tablet also shows where the message stands on its way out.
import { Fragment, memo, useState } from 'react';
import type { Names } from '../board';
import { clock, toDate } from '../board';
import { authorName, type Delivery, deliveryOf } from '../bridges';
import type { Message } from '../gen/agora/v1/rooms_pb';
import { Icon } from '../icons';
import type { TapeItem } from '../tape';
import { AgentLink, Avatar, MessageBody, usePg } from './common';
import { OUTSIDE, OutsiderAvatar, OutsiderName } from './outside';

type Context = { known: Names; operator: string };

/** The operator's decision on a pending message: send it or not. */
export type Decide = (m: Message, send: boolean) => Promise<void>;

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

/** A pending message's buttons: it goes out only when the operator sends it. */
function Decision({ m, onDecide }: { m: Message; onDecide: Decide }) {
  const [busy, setBusy] = useState(false);
  const decide = (send: boolean) => {
    setBusy(true);
    onDecide(m, send).finally(() => setBusy(false));
  };
  return (
    <fieldset className="decide" aria-label={`Send the message of ${m.author} to ${m.room}?`}>
      <span className="why">Waiting for you</span>
      <button type="button" className="btn" disabled={busy} onClick={() => decide(true)}>
        <Icon name="send" />
        Send
      </button>
      <button type="button" className="btn quiet" disabled={busy} onClick={() => decide(false)}>
        Don’t send
      </button>
    </fieldset>
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
  ...ctx
}: Context & {
  item: Extract<TapeItem, { kind: 'message' }>;
  dim: boolean;
  onReply: (m: Message) => void;
  onFocus: (pair: [string, string]) => void;
  onJump: (id: bigint) => void;
  onDecide: Decide;
}) {
  const pg = usePg();
  const { message: m, parent, to } = item;
  const ext = m.externalAuthor;
  const d = deliveryOf(m);
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
          <DeliveryMark d={d} room={m.room} />
          {item.toYou && <span className="tag">TO YOU</span>}
          <button type="button" className="mreply" onClick={() => onReply(m)}>
            reply
          </button>
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
        {d === 'pending' && <Decision m={m} onDecide={onDecide} />}
        {d === 'failed' && <p className="dlerr">Not sent{m.deliveryError ? `: ${m.deliveryError}` : ''}</p>}
      </div>
      <div className="acts">
        <button type="button" onClick={() => onReply(m)}>
          <Icon name="reply" />
          Reply
        </button>
        {first !== undefined && (
          <button type="button" onClick={() => onFocus([m.author, first])}>
            <Icon name="thread" />
            Thread
          </button>
        )}
      </div>
    </li>
  );
});
