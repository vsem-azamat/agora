// The rooms: in the sidebar, and as a view of its own on a phone.
import { type RoomCount, roomBadge } from '../board';
import type { Room } from '../gen/agora/v1/rooms_pb';
import { Icon } from '../icons';

export const roomHref = (room: string) => `#/rooms/${encodeURIComponent(room)}`;

type RoomsProps = { rooms: Room[]; unread: Map<string, RoomCount>; followed: Set<string> };

function Badge({ count }: { count: RoomCount | undefined }) {
  const badge = roomBadge(count);
  if (!badge) return null;
  return <em className={badge.mention ? 'mention' : undefined}>{badge.text}</em>;
}

/** The rooms in the sidebar; followed rooms are set apart. */
export function RoomNav(props: RoomsProps & { current?: string }) {
  return (
    <div className="nav rooms">
      {props.rooms.map((r) => (
        <a
          key={r.name}
          href={roomHref(r.name)}
          className={props.followed.has(r.name) ? 'followed' : undefined}
          aria-current={r.name === props.current ? 'page' : undefined}
        >
          <Icon name="room" />
          <span className="n">#{r.name}</span>
          <Badge count={props.unread.get(r.name)} />
        </a>
      ))}
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
        {props.rooms.map((r) => (
          <a key={r.name} href={roomHref(r.name)} className={props.followed.has(r.name) ? 'followed' : undefined}>
            <Icon name="room" />
            <span className="n">
              <b>#{r.name}</b>
              <span className="sum">{r.purpose}</span>
            </span>
            {roomBadge(props.unread.get(r.name)) ? (
              <Badge count={props.unread.get(r.name)} />
            ) : (
              <span className="c">{r.messages}</span>
            )}
          </a>
        ))}
      </div>
    </section>
  );
}
