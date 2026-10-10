// The board: every agent but the operator, with what it works on.
import { useContext, useState } from 'react';
import {
  age,
  ciMark,
  type Filter,
  filterCounts,
  lastFormerName,
  liveness,
  matches,
  projectOf,
  pullRequests,
  toDate,
} from '../board';
import type { Profile } from '../gen/agora/v1/agents_pb';
import { Icon } from '../icons';
import { Avatar, OpenAgent, usePg } from './common';

const filterLabels: Record<Filter, string> = { all: 'all', busy: 'busy', idle: 'idle', pr: 'with PR' };

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
  // the summary counts the whole board; the chips count within the chosen project
  const { busy, idle } = filterCounts(props.agents);
  const offline = props.agents.length - busy - idle;
  return (
    <section className="view" aria-label="Board">
      <div className="head">
        <h2>
          <Icon name="agent" />
          Board
        </h2>
        <span className="sum">
          {busy} busy · {idle} idle{offline > 0 ? ` · ${offline} offline` : ''}
        </span>
      </div>
      <div className="chips">
        {(Object.keys(filterLabels) as Filter[]).map((f) => (
          <button key={f} type="button" className="chip" aria-pressed={f === filter} onClick={() => setFilter(f)}>
            {filterLabels[f]} {counts[f]}
          </button>
        ))}
        {props.project !== undefined && (
          <button
            type="button"
            className="chip on"
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
  const open = useContext(OpenAgent);
  const pg = usePg();
  const prs = pullRequests(agent);
  const was = lastFormerName(agent);
  return (
    <li>
      <button type="button" className="agent" data-agent={agent.name} onClick={() => open(agent.name)}>
        <Avatar name={agent.name} agent={agent} live={liveness(agent)} />
        <span className="who">
          <span className="l1">
            <span className="pgname n" style={pg(agent.name)}>
              {agent.name}
            </span>
            {was && <span className="was">was {was}</span>}
            <span className="p">
              {projectOf(agent)}
              {prs.length > 0 && <span>·</span>}
              {prs.map((n) => (
                <PullRequest key={n} agent={agent} n={n} />
              ))}
            </span>
          </span>
          <span className="t">{agent.task || agent.status}</span>
        </span>
        <span className="age">{age(toDate(agent.updatedAt), now)}</span>
      </button>
    </li>
  );
}

/** `#57` with a laurel when CI was last green, an ostrakon when red or in conflict. */
export function PullRequest({ agent, n, words }: { agent: Profile; n: number; words?: boolean }) {
  const ci = ciMark(agent, n);
  return (
    <span className={ci ? `ci ${ci}` : 'ci'}>
      {words && <Icon name="pr" />}#{n}
      {ci && <Icon name={ci} title={ci === 'green' ? 'CI green' : 'CI red'} />}
      {words && (ci ? ` ${ci}` : ' no CI yet')}
    </span>
  );
}
