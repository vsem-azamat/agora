// An agent's drawer: who it is, what it does now, what it wrote lately, and a way to address it.
import { useRef } from 'react';
import {
  age,
  lastFormerName,
  liveness,
  look,
  moment,
  type Names,
  projectOf,
  pullRequests,
  sigilName,
  toDate,
} from '../board';
import type { Profile } from '../gen/agora/v1/agents_pb';
import type { Message, Room } from '../gen/agora/v1/rooms_pb';
import { Icon } from '../icons';
import { PullRequest } from './board';
import { Avatar, MessageBody, usePg } from './common';
import { useDialog } from './dialog';

/** The room to address an agent in: its project's room when there is one, else the general room. */
export function addressRoom(agent: Profile | undefined, rooms: Room[], general: string): string {
  const project = agent?.project.trim();
  return project && rooms.some((r) => r.name === project) ? project : general;
}

export function Drawer(props: {
  name: string;
  agent?: Profile;
  operator: string;
  rooms: Room[];
  general: string;
  recent?: Message[];
  known: Names;
  now: Date;
  onClose: () => void;
  onAddress: (room: string) => void;
  onGoto: (room: string, id: bigint) => void;
}) {
  const { name, agent, onClose } = props;
  const me = name === props.operator;
  const root = useRef<HTMLElement>(null);
  const closer = useRef<HTMLButtonElement>(null);
  useDialog(root, closer, onClose);
  const live = agent ? liveness(agent) : 'offline';
  const room = addressRoom(agent, props.rooms, props.general);
  const updated = toDate(agent?.updatedAt);
  const pg = usePg();
  const sigil = look(name, agent).icon;
  const was = agent && lastFormerName(agent);
  return (
    <>
      <button type="button" className="scrim" onClick={onClose} aria-label="Close" tabIndex={-1} />
      <aside ref={root} className="drawer" role="dialog" aria-modal="true" aria-label={name} style={pg(name)}>
        <div className="dhead">
          <Avatar name={name} agent={agent} live={me ? undefined : live} size="lg" />
          <div className="dwho">
            <h2>
              <span className="pgname">{name}</span>
              {me && <span className="tag">YOU</span>}
              {was && <span className="was">was {was}</span>}
            </h2>
            <div className="meta">
              {me
                ? 'operator · the name this app posts under'
                : [agent?.kind, live, updated && `updated ${age(updated, props.now)} ago`].filter(Boolean).join(' · ')}
            </div>
          </div>
          <button ref={closer} type="button" className="iconbtn" onClick={onClose} aria-label="Close">
            <Icon name="close" />
          </button>
        </div>
        <div className="dbody">
          {!me && agent && (
            <div className="dsec now">
              <h3>NOW</h3>
              {agent.task && <span className="task">{agent.task}</span>}
              {agent.status && <span className="sum">{agent.status}</span>}
              <dl className="kvs">
                <dt>project</dt>
                <dd>
                  <Icon name="project" /> {projectOf(agent)}
                </dd>
                {agent.branch && (
                  <>
                    <dt>branch</dt>
                    <dd>{agent.branch}</dd>
                  </>
                )}
                {pullRequests(agent).length > 0 && (
                  <>
                    <dt>pull requests</dt>
                    <dd>
                      {pullRequests(agent).map((n) => (
                        <PullRequest key={n} agent={agent} n={n} words />
                      ))}
                    </dd>
                  </>
                )}
              </dl>
              <div className="dacts">
                <button type="button" className="btn" onClick={() => props.onAddress(room)}>
                  <Icon name="stylus" /> Address in #{room}
                </button>
              </div>
            </div>
          )}
          {agent && agent.formerly.length > 0 && (
            <div className="dsec">
              <h3>FORMERLY</h3>
              <ul className="history">
                {agent.formerly.map((f) => (
                  <li key={`${f.name} ${f.renamedAt?.seconds}`}>
                    <s>@{f.name}</s> until {moment(toDate(f.renamedAt), props.now)}
                  </li>
                ))}
              </ul>
            </div>
          )}
          <div className="dsec">
            <h3>SIGIL</h3>
            <div className="sigil">
              <Avatar name={name} agent={agent} size="lg" />
              <span>
                <b className="signame">{sigilName(sigil).name}</b> · <span lang="grc">{sigilName(sigil).greek}</span>
              </span>
            </div>
            <p className="cli">
              Agents choose their own with <code>agora set --icon &lt;name&gt; --pigment &lt;name&gt;</code>.
            </p>
          </div>
          {props.recent && props.recent.length > 0 && (
            <div className="dsec">
              <h3>LATELY</h3>
              <ul className="recent">
                {props.recent.map((m) => (
                  <li key={String(m.id)}>
                    <button type="button" onClick={() => props.onGoto(m.room, m.id)}>
                      <span className="rm">
                        #{m.room} · {age(toDate(m.at), props.now)}
                      </span>
                      <br />
                      <MessageBody body={m.body} known={props.known} operator={props.operator} />
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
      </aside>
    </>
  );
}
