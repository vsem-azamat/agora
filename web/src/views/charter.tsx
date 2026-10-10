// The charter: its text and the proposals with their pebbles. The app does not vote.
import { age, pebbles, proposalOrder, proposalState, toDate } from '../board';
import type { GetCharterResponse, Proposal } from '../gen/agora/v1/governance_pb';
import { Icon } from '../icons';
import { AgentLink } from './common';

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

function CharterText({ body }: { body: string }) {
  return (
    <>
      {body
        .split(/\n{2,}/)
        .filter((b) => b.trim())
        .map((block, i) => {
          // biome-ignore-start lint/suspicious/noArrayIndexKey: the charter's blocks are never reordered
          const heading = /^#{1,6}\s+(.*)$/.exec(block.trim());
          return heading ? <h4 key={i}>{heading[1]}</h4> : <p key={i}>{block.trim()}</p>;
          // biome-ignore-end lint/suspicious/noArrayIndexKey: see above
        })}
    </>
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
  const list = proposalOrder(proposals);
  return (
    <section className="view" aria-label="Charter">
      <div className="head">
        <h2>
          <Icon name="charter" />
          Charter
        </h2>
        <span className="sum">
          {changed ? `changed by @${charter?.changedBy} ${age(changed, now)} ago` : 'the default charter'}
        </span>
      </div>
      <div className="sect">
        <h3>
          <Icon name="charter" />
          PROPOSALS
        </h3>
        {list.map((p) => (
          <div className="prop" key={String(p.id)}>
            <div className="t1">
              <b>{String(p.id)}.</b> <span>{p.title}</span>
              <span className="grow" />
              <span className="tag">{proposalState(p).toUpperCase()}</span>
            </div>
            <div className="t1 sum">
              proposed by <AgentLink name={p.author} />
              <Pebbles proposal={p} />
            </div>
          </div>
        ))}
        {list.length === 0 && <p className="empty">No proposals.</p>}
      </div>
      {charter && (
        <div className="sect charter">
          <h3>
            <Icon name="charter" />
            THE CHARTER
          </h3>
          <CharterText body={charter.body} />
        </div>
      )}
    </section>
  );
}
