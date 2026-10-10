// Turns: queues with their holders and who waits, and locks with who holds them.
import { lockState, splitResources, turns } from '../board';
import type { Resource } from '../gen/agora/v1/resources_pb';
import { Icon } from '../icons';
import { AgentLink, Avatar } from './common';

function Queue({ q, now }: { q: Resource; now: Date }) {
  const all = turns(q, now);
  const holding = all.filter((t) => t.holds);
  const waiting = all.filter((t) => !t.holds);
  const free = Math.max(0, q.slots - holding.length);
  return (
    <div className="qrow" data-queue={q.key}>
      <span>
        <b>{q.key}</b> <span className="sum">{q.slots} slots</span>
      </span>
      <span className="sum">{waiting.length} waiting</span>
      <div className="slots">
        {holding.map((t) => (
          <span key={t.agent} className="slot hold">
            <Avatar name={t.agent} size="sm" />
            <AgentLink name={t.agent} />
            <span className="sum">{t.label}</span>
          </span>
        ))}
        {Array.from({ length: free }, (_, i) => (
          // biome-ignore lint/suspicious/noArrayIndexKey: free slots have nothing else to tell them apart
          <span key={i} className="slot free">
            free slot
          </span>
        ))}
      </div>
      {waiting.length > 0 && (
        <div className="wait">
          {waiting.map((t) => (
            <span key={t.agent} className="slot">
              <AgentLink name={t.agent} />
              <span className="sum">{t.label}</span>
            </span>
          ))}
        </div>
      )}
    </div>
  );
}

function Lock({ l, now }: { l: Resource; now: Date }) {
  const s = lockState(l, now);
  return (
    <div className="qrow" data-lock={l.key}>
      <span>
        <Icon name="lock" /> <b>{l.key}</b>
      </span>
      <span className={s.holder ? 'sum' : 'sum free'}>{s.holder ? s.left : 'free'}</span>
      {s.holder && (
        <div className="wait">
          <Avatar name={s.holder} size="sm" />
          <AgentLink name={s.holder} /> holds the seal
          {s.waiting > 0 && <span className="sum">· {s.waiting} waiting</span>}
        </div>
      )}
    </div>
  );
}

export function Turns({ resources, now }: { resources: Resource[]; now: Date }) {
  const { queues, locks } = splitResources(resources);
  return (
    <section className="view" aria-label="Turns">
      <div className="head">
        <h2>
          <Icon name="queue" />
          Turns
        </h2>
        <span className="sum">who holds what, who waits</span>
      </div>
      <div className="sect">
        <h3>
          <Icon name="queue" />
          QUEUES
        </h3>
        {queues.map((q) => (
          <Queue key={q.key} q={q} now={now} />
        ))}
        {queues.length === 0 && <p className="empty">No queues.</p>}
      </div>
      <div className="sect">
        <h3>
          <Icon name="lock" />
          LOCKS
        </h3>
        {locks.map((l) => (
          <Lock key={l.key} l={l} now={now} />
        ))}
        {locks.length === 0 && <p className="empty">No locks.</p>}
      </div>
    </section>
  );
}
