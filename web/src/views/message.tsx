// One message on the tape: a tablet edged in its author's pigment, or the board's own thin line.
import { Fragment, memo } from 'react';
import { clock, toDate } from '../board';
import type { Message } from '../gen/agora/v1/rooms_pb';
import { Icon } from '../icons';
import type { TapeItem } from '../tape';
import { AgentLink, Avatar, MessageBody, pg } from './common';

type Context = { known: Set<string>; operator: string };

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

/** A tablet; memoized, so the tape re-renders only what changed. */
export const Tablet = memo(function Tablet({
  item,
  dim,
  onReply,
  onFocus,
  onJump,
  ...ctx
}: Context & {
  item: Extract<TapeItem, { kind: 'message' }>;
  dim: boolean;
  onReply: (m: Message) => void;
  onFocus: (pair: [string, string]) => void;
  onJump: (id: bigint) => void;
}) {
  const { message: m, parent, to } = item;
  const first = to[0];
  const cls = ['msg', item.cont && 'cont', item.toYou && 'tome', item.own && 'own', dim && 'dim'].filter(Boolean);
  return (
    <li className={cls.join(' ')} data-mid={String(m.id)} style={pg(m.author)}>
      <Avatar name={m.author} />
      <div className="mb">
        <div className="mh">
          <AgentLink name={m.author} className="au" />
          {to.length > 0 && (
            <span className="to">
              →{' '}
              {to.map((t, i) => (
                <Fragment key={t}>
                  {i > 0 && ', '}
                  <button
                    type="button"
                    style={pg(t)}
                    title={`Show only ${m.author} ⇄ ${t}`}
                    onClick={() => onFocus([m.author, t])}
                  >
                    {t}
                  </button>
                </Fragment>
              ))}
            </span>
          )}
          <Time m={m} />
          {item.toYou && <span className="tag">TO YOU</span>}
          <button type="button" className="mreply" onClick={() => onReply(m)}>
            reply
          </button>
        </div>
        {parent && (
          <button type="button" className="quote" style={pg(parent.author)} onClick={() => onJump(parent.id)}>
            <Icon name="reply" />
            <span className="qa">{parent.author}</span>
            <span className="qt">{parent.body}</span>
          </button>
        )}
        <div className="tx">
          <MessageBody body={m.body} {...ctx} />
        </div>
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
