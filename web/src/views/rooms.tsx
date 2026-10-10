// The rooms: in the sidebar, and as a view of its own on a phone.
import { type RoomCount, roomBadge } from '../board';
import { runningOf, runningText } from '../bridges';
import type { Bridge } from '../gen/agora/v1/bridges_pb';
import type { Room } from '../gen/agora/v1/rooms_pb';
import { Icon } from '../icons';

export const roomHref = (room: string) => `#/rooms/${encodeURIComponent(room)}`;

type RoomsProps = {
  rooms: Room[];
  unread: Map<string, RoomCount>;
  followed: Set<string>;
  /** The bridges by the name of their room. */
  bridges?: Map<string, Bridge>;
};

function Badge({ count }: { count: RoomCount | undefined }) {
  const badge = roomBadge(count);
  if (!badge) return null;
  return <em className={badge.mention ? 'mention' : undefined}>{badge.text}</em>;
}

/** How many messages of the room wait for the operator to send them. */
function Waiting({ count }: { count: RoomCount | undefined }) {
  const n = count?.pending ?? 0;
  if (n === 0) return null;
  return (
    <em className="pending" title={`${n} waiting for you to send`}>
      <Icon name="send" />
      {n}
      <span className="sr"> waiting for you to send</span>
    </em>
  );
}

/** A room's mark: the stoa, or for a bridged room the bridge, drawn by its state. */
export function RoomIcon({ bridge }: { bridge?: Bridge }) {
  if (!bridge) return <Icon name="room" />;
  return <Icon name="bridge" className={`br ${runningOf(bridge)}`} title={`bridge ${runningText(bridge)}`} />;
}

/** The rooms in the sidebar; followed rooms are set apart. */
export function RoomNav(props: RoomsProps & { current?: string }) {
  return (
    <div className="nav rooms">
      {props.rooms.map((r) => {
        const count = props.unread.get(r.name);
        return (
          <a
            key={r.name}
            href={roomHref(r.name)}
            className={props.followed.has(r.name) ? 'followed' : undefined}
            aria-current={r.name === props.current ? 'page' : undefined}
          >
            <RoomIcon bridge={props.bridges?.get(r.name)} />
            <span className="n">#{r.name}</span>
            <Waiting count={count} />
            <Badge count={count} />
          </a>
        );
      })}
    </div>
  );
}

export function Rooms(props: RoomsProps) {
  return (
    <section className="view" aria-label="Rooms">
      <div className="head">
        <h2>
          <Icon name="room" />
          Rooms
        </h2>
      </div>
      <div className="nav rooms roomlist">
        {props.rooms.map((r) => {
          const count = props.unread.get(r.name);
          const bridge = props.bridges?.get(r.name);
          const down = bridge && runningOf(bridge) !== 'running';
          return (
            <a key={r.name} href={roomHref(r.name)} className={props.followed.has(r.name) ? 'followed' : undefined}>
              <RoomIcon bridge={bridge} />
              <span className="n">
                <b>#{r.name}</b>
                <span className={down ? 'sum down' : 'sum'}>{down ? `bridge ${runningText(bridge)}` : r.purpose}</span>
              </span>
              <span className="cs">
                <Waiting count={count} />
                {roomBadge(count) ? <Badge count={count} /> : <span className="c">{r.messages}</span>}
              </span>
            </a>
          );
        })}
      </div>
    </section>
  );
}
