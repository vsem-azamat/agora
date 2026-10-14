// The rooms: in the sidebar, and as a view of its own on a phone.
import { type RoomCount, roomBadge, toDate } from '../board';
import { runningOf, runningText } from '../bridges';
import type { Bridge } from '../gen/agora/v1/bridges_pb';
import type { Room, Thread } from '../gen/agora/v1/rooms_pb';
import { Icon } from '../icons';
import { Notice } from './shell';
import { threadHref } from './thread';

export const roomHref = (room: string) => `#/rooms/${encodeURIComponent(room)}`;
/** The Rooms view on its archived rooms. */
export const archiveHref = '#/archive';

/** A day for an archived room: `9 Oct`, with the year when it is not this year's. */
export function shortDate(at: Date | undefined, now: Date): string {
  if (!at) return '';
  const opts: Intl.DateTimeFormatOptions = { day: 'numeric', month: 'short' };
  if (at.getFullYear() !== now.getFullYear()) opts.year = 'numeric';
  return at.toLocaleDateString('en-GB', opts);
}

type RoomsProps = {
  rooms: Room[];
  unread: Map<string, RoomCount>;
  followed: Set<string>;
  /** The bridges by the name of their room. */
  bridges?: Map<string, Bridge>;
  /** The archived rooms, listed apart. */
  archived?: Room[];
  /** The titled threads, listed under their rooms. */
  threads?: Thread[];
};

/** The titled threads of a room, under it in the room list. */
function ThreadLinks({ room, threads, current }: { room: string; threads?: Thread[]; current?: bigint }) {
  const mine = (threads ?? []).filter((t) => t.room === room);
  if (mine.length === 0) return null;
  return (
    <>
      {mine.map((t) => (
        <a
          key={String(t.root)}
          className="threadrow"
          href={threadHref(room, t.root)}
          aria-current={t.root === current ? 'page' : undefined}
        >
          <Icon name="thread" />
          <span className="n">{t.title}</span>
          {t.unread > 0 && <em>{t.unread}</em>}
        </a>
      ))}
    </>
  );
}

function Badge({ count }: { count: RoomCount | undefined }) {
  const badge = roomBadge(count);
  if (!badge) return null;
  return <em className={badge.mention ? 'mention' : undefined}>{badge.text}</em>;
}

/** How many messages of the room wait for the operator to send them. */
function Waiting({ count }: { count: RoomCount | undefined }) {
  const n = count?.scheduled ?? 0;
  if (n === 0) return null;
  return (
    <em className="scheduled" title={`${n} scheduled to go out`}>
      <Icon name="send" />
      {n}
      <span className="sr"> scheduled to go out</span>
    </em>
  );
}

/** A room's mark: the stoa, or for a bridged room the bridge, drawn by its state. */
export function RoomIcon({ bridge }: { bridge?: Bridge }) {
  if (!bridge) return <Icon name="room" />;
  return <Icon name="bridge" className={`br ${runningOf(bridge)}`} title={`bridge ${runningText(bridge)}`} />;
}

/** The rooms in the sidebar; followed rooms are set apart, and a row at the end leads to the archived ones. */
export function RoomNav(
  props: Omit<RoomsProps, 'archived'> & { current?: string; currentThread?: bigint; archived?: number },
) {
  return (
    <>
      <RoomLinks {...props} />
      {(props.archived ?? 0) > 0 && (
        <div className="nav">
          <a className="archivedrow" href={archiveHref}>
            <Icon name="archive" />
            <span className="n">{props.archived} archived</span>
            <span aria-hidden="true">→</span>
          </a>
        </div>
      )}
    </>
  );
}

function RoomLinks(props: Omit<RoomsProps, 'archived'> & { current?: string; currentThread?: bigint }) {
  return (
    <div className="nav rooms">
      {props.rooms.map((r) => {
        const count = props.unread.get(r.name);
        return [
          <a
            key={r.name}
            href={roomHref(r.name)}
            className={props.followed.has(r.name) ? 'followed' : undefined}
            aria-current={r.name !== props.current ? undefined : props.currentThread === undefined ? 'page' : 'true'}
          >
            <RoomIcon bridge={props.bridges?.get(r.name)} />
            <span className="n">#{r.name}</span>
            <Waiting count={count} />
            <Badge count={count} />
          </a>,
          <ThreadLinks
            key={`${r.name} threads`}
            room={r.name}
            threads={props.threads}
            current={r.name === props.current ? props.currentThread : undefined}
          />,
        ];
      })}
    </div>
  );
}

export function Rooms(
  props: RoomsProps & {
    /** Which rooms are shown. */
    tab?: 'active' | 'archived';
    onUnarchive?: (room: string) => void;
    now?: Date;
    /** A refused call to tell, numbered so a repeat shows for its full time. */
    notice?: { text: string; n: number };
    onCloseNotice?: () => void;
  },
) {
  const archived = props.tab === 'archived';
  const archivedRooms = props.archived ?? [];
  const now = props.now ?? new Date();
  return (
    <section className="view" aria-label="Rooms">
      <div className="head">
        <h2>
          <Icon name="room" />
          Rooms
        </h2>
      </div>
      <nav className="seg" aria-label="Rooms">
        <a href="#/rooms" aria-current={archived ? undefined : 'page'}>
          Active · {props.rooms.length}
        </a>
        <a href={archiveHref} aria-current={archived ? 'page' : undefined}>
          Archived · {archivedRooms.length}
        </a>
      </nav>
      {props.notice && props.onCloseNotice && (
        <Notice key={props.notice.n} text={props.notice.text} onClose={props.onCloseNotice} />
      )}
      {archived ? (
        <div className="nav roomlist archivelist">
          {archivedRooms.map((r) => (
            <div key={r.name} className="arow">
              <a href={roomHref(r.name)}>
                <Icon name="archive" />
                <span className="n">
                  <b>#{r.name}</b>
                  <span className="sum">
                    archived by {r.archivedBy} · {shortDate(toDate(r.archivedAt), now)} · {r.messages} msgs
                  </span>
                </span>
              </a>
              <button
                type="button"
                className="chip"
                aria-label={`Unarchive #${r.name}`}
                onClick={() => props.onUnarchive?.(r.name)}
              >
                Unarchive
              </button>
            </div>
          ))}
          {archivedRooms.length === 0 && <p className="empty">No archived rooms.</p>}
        </div>
      ) : (
        <ActiveRooms {...props} />
      )}
    </section>
  );
}

function ActiveRooms(props: RoomsProps) {
  return (
    <div className="nav rooms roomlist">
      {props.rooms.map((r) => {
        const count = props.unread.get(r.name);
        const bridge = props.bridges?.get(r.name);
        const down = bridge && runningOf(bridge) !== 'running';
        return [
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
          </a>,
          <ThreadLinks key={`${r.name} threads`} room={r.name} threads={props.threads} />,
        ];
      })}
    </div>
  );
}
