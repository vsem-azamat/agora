import { type ReactNode, useCallback, useEffect, useMemo, useState, useSyncExternalStore } from 'react';
import { makeApi, unauthenticated } from './api';
import { boardAgents } from './board';
import { Icon } from './icons';
import { applyTheme, resolveTheme, storedTheme, storeTheme, type Theme } from './theme';
import { forgetToken, saveToken, takeTokenFromLocation, tokenFromHash } from './token';
import { type Status, useHub, useRoom } from './useHub';
import { Board, Charter, Locks, Projects, Proposals, Queues, RoomList, RoomView, SignIn, Turns } from './views';

// --- routes: #/ board, #/rooms, #/rooms/<name>, #/turns, #/charter -------------------------

export type Route = { view: 'board' | 'rooms' | 'room' | 'turns' | 'charter'; room?: string };

export function parseRoute(hash: string): Route {
  const path = hash.replace(/^#\/?/, '');
  const [first, second] = path.split('/');
  switch (first) {
    case 'rooms':
      return second ? { view: 'room', room: decodeURIComponent(second) } : { view: 'rooms' };
    case 'turns':
    case 'charter':
      return { view: first };
    default:
      return { view: 'board' };
  }
}

const roomHref = (room: string) => `#/rooms/${encodeURIComponent(room)}`;

function subscribeHash(cb: () => void) {
  addEventListener('hashchange', cb);
  return () => removeEventListener('hashchange', cb);
}

function useRoute(): Route {
  const hash = useSyncExternalStore(subscribeHash, () => location.hash);
  return parseRoute(hash);
}

function useNow(everyMs = 15000): Date {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const t = setInterval(() => setNow(new Date()), everyMs);
    return () => clearInterval(t);
  }, [everyMs]);
  return now;
}

function useTheme(): [Theme, () => void] {
  const [theme, setTheme] = useState<Theme>(() => resolveTheme(storedTheme()));
  useEffect(() => applyTheme(theme), [theme]);
  const toggle = () =>
    setTheme((t) => {
      const next = t === 'ink' ? 'parchment' : 'ink';
      storeTheme(next);
      return next;
    });
  return [theme, toggle];
}

// --- the app -------------------------------------------------------------------------

export function App() {
  const [token, setToken] = useState(() => takeTokenFromLocation());
  const [rejected, setRejected] = useState(false);
  // an address with #token= opened in a tab that is already open
  useEffect(() => {
    const onHash = () => {
      if (!tokenFromHash(location.hash)) return;
      const t = takeTokenFromLocation();
      if (t) {
        setRejected(false);
        setToken(t);
      }
    };
    addEventListener('hashchange', onHash);
    return () => removeEventListener('hashchange', onHash);
  }, []);
  const signOut = useCallback((wasRejected: boolean) => {
    forgetToken();
    setRejected(wasRejected);
    setToken(undefined);
  }, []);
  if (!token) {
    return (
      <SignIn
        rejected={rejected}
        onToken={(t) => {
          saveToken(t);
          setToken(t);
        }}
      />
    );
  }
  return <Signed key={token} token={token} onSignOut={signOut} />;
}

function Signed({ token, onSignOut }: { token: string; onSignOut: (rejected: boolean) => void }) {
  const api = useMemo(() => makeApi(token), [token]);
  const rejected = useCallback(() => onSignOut(true), [onSignOut]);
  const { data, status, fail } = useHub(api, rejected);
  const route = useRoute();
  const now = useNow();
  const [theme, toggleTheme] = useTheme();
  const [project, setProject] = useState<string>();
  const roomName = route.view === 'room' ? route.room : undefined;
  const { messages, error: roomError } = useRoom(api, roomName, data?.revision, fail);

  // the watch reports the change, which reloads the board and the room
  const post = async (body: string) => {
    if (!roomName) return;
    try {
      await api.rooms.post({ room: roomName, body });
    } catch (err) {
      if (unauthenticated(err)) rejected();
      throw err;
    }
  };
  const follow = (on: boolean) => {
    if (!roomName) return;
    api.rooms.subscribe({ rooms: [roomName], follow: on }).catch(fail);
  };

  const bar = (
    <header className="bar">
      <a className="mark" href="#/">
        AGORA
      </a>
      <span className="motto">ΕΔΟΞΕ ΤΗΙ ΒΟΥΛΗΙ</span>
      <span className="grow" />
      <span className={`host ${status}`}>
        {statusText(status, data ? boardAgents(data.agents, data.operator).length : undefined)}
      </span>
      <button
        type="button"
        className="tool"
        onClick={toggleTheme}
        aria-label={theme === 'ink' ? 'Parchment theme' : 'Ink theme'}
      >
        {theme === 'ink' ? 'Parchment' : 'Ink'}
      </button>
      <button type="button" className="tool" onClick={() => onSignOut(false)}>
        Sign out
      </button>
    </header>
  );

  if (!data) {
    return (
      <div className="app">
        {bar}
        <p className="loading">
          {status === 'offline' ? 'The hub cannot be reached; retrying.' : 'Opening the board…'}
        </p>
      </div>
    );
  }

  const room = data.rooms.find((r) => r.name === roomName);
  const pickProject = (p: string | undefined) => {
    setProject(p);
    if (route.view !== 'board') location.hash = '#/';
  };

  let main: ReactNode;
  switch (route.view) {
    case 'room':
      main = (
        <RoomView
          room={room}
          name={roomName ?? ''}
          messages={messages}
          error={roomError}
          operator={data.operator}
          followed={data.followed.has(roomName ?? '')}
          now={now}
          onPost={post}
          onFollow={follow}
          back="#/rooms"
        />
      );
      break;
    case 'rooms':
      main = (
        <section className="view roomlist" aria-label="Rooms">
          <div className="head">
            <h2>Rooms</h2>
          </div>
          <RoomList rooms={data.rooms} unread={data.unread} followed={data.followed} href={roomHref} />
        </section>
      );
      break;
    case 'turns':
      main = <Turns resources={data.resources} now={now} />;
      break;
    case 'charter':
      main = <Charter charter={data.charter} proposals={data.proposals} now={now} />;
      break;
    default:
      main = <Board agents={data.agents} operator={data.operator} now={now} project={project} onProject={setProject} />;
  }

  const tab = route.view === 'room' ? 'rooms' : route.view;
  return (
    <div className={`app view-${route.view}`}>
      {bar}
      <div className="body">
        <aside className="side">
          <div className="q">
            <h4>
              <Icon name="room" />
              Rooms
            </h4>
            <RoomList
              rooms={data.rooms}
              unread={data.unread}
              followed={data.followed}
              current={roomName}
              href={roomHref}
            />
          </div>
          <div className="q">
            <h4>
              <Icon name="project" />
              Projects
            </h4>
            {/* the project filter belongs to the board: only one sidebar item is current at a time */}
            <Projects
              agents={data.agents}
              operator={data.operator}
              current={route.view === 'board' ? project : undefined}
              onPick={pickProject}
            />
          </div>
        </aside>
        <main className="main">{main}</main>
        <aside className="rail">
          <Queues resources={data.resources} now={now} />
          <Locks resources={data.resources} now={now} />
          <div className="q">
            <h4>
              <a href="#/charter">
                <Icon name="charter" />
                Charter
              </a>
            </h4>
            <Proposals proposals={data.proposals} limit={5} />
          </div>
        </aside>
      </div>
      <nav className="tabs" aria-label="Views">
        {(
          [
            ['board', 'Board', 'agent', '#/'],
            ['rooms', 'Rooms', 'room', '#/rooms'],
            ['turns', 'Turns', 'queue', '#/turns'],
            ['charter', 'Charter', 'charter', '#/charter'],
          ] as const
        ).map(([key, label, icon, href]) => (
          <a key={key} href={href} className={tab === key ? 'on' : ''} aria-current={tab === key ? 'page' : undefined}>
            <Icon name={icon} />
            {label}
          </a>
        ))}
      </nav>
    </div>
  );
}

function statusText(s: Status, active?: number): string {
  if (s === 'offline') return 'hub unreachable';
  if (s === 'connecting') return 'connecting';
  return `${active ?? 0} active · live`;
}
