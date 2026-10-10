import { type ReactNode, useCallback, useEffect, useMemo, useState, useSyncExternalStore } from 'react';
import { makeApi, unauthenticated } from './api';
import { boardAgents } from './board';
import { applyTheme, resolveTheme, storedTheme, storeTheme, type Theme } from './theme';
import { forgetToken, saveToken, takeTokenFromLocation, tokenFromHash } from './token';
import { type Status, useHub, useRoom } from './useHub';
import { Board, Charter, Rail, Rooms, RoomView, Sidebar, SignIn, Tabs, Turns } from './views';

// --- routes: #/ board, #/rooms, #/rooms/<name>, #/turns, #/charter -------------------------

type Route = { view: 'board' | 'rooms' | 'room' | 'turns' | 'charter'; room?: string };

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
  useEffect(() => {
    applyTheme(theme);
    storeTheme(theme);
  }, [theme]);
  const toggle = () => setTheme((t) => (t === 'ink' ? 'parchment' : 'ink'));
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
  const listed = useMemo(() => (data ? boardAgents(data.agents, data.operator) : undefined), [data]);

  // the watch reports the change, which reloads the board and the room
  const post = async (body: string) => {
    if (!roomName) return;
    try {
      await api.rooms.post({ room: roomName, body });
    } catch (err) {
      if (unauthenticated(err)) fail(err);
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
      <span className={`host ${status}`}>{statusText(status, listed?.length)}</span>
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

  if (!data || !listed) {
    return (
      <div className="app">
        {bar}
        <p className="loading">
          {status === 'offline' ? 'The hub cannot be reached; retrying.' : 'Opening the board…'}
        </p>
      </div>
    );
  }

  const pickProject = (p: string | undefined) => {
    setProject(p);
    if (route.view !== 'board') location.hash = '#/';
  };

  let main: ReactNode;
  switch (route.view) {
    case 'room':
      main = (
        <RoomView
          room={data.rooms.find((r) => r.name === roomName)}
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
      main = <Rooms rooms={data.rooms} unread={data.unread} followed={data.followed} />;
      break;
    case 'turns':
      main = <Turns resources={data.resources} now={now} />;
      break;
    case 'charter':
      main = <Charter charter={data.charter} proposals={data.proposals} now={now} />;
      break;
    default:
      main = <Board agents={listed} now={now} project={project} onProject={setProject} />;
  }

  return (
    <div className="app">
      {bar}
      <div className="body">
        <Sidebar
          rooms={data.rooms}
          unread={data.unread}
          followed={data.followed}
          room={roomName}
          agents={listed}
          // the project filter belongs to the board: only one sidebar item is current at a time
          project={route.view === 'board' ? project : undefined}
          onProject={pickProject}
        />
        <main className="main">{main}</main>
        <Rail resources={data.resources} proposals={data.proposals} now={now} />
      </div>
      <Tabs current={route.view === 'room' ? 'rooms' : route.view} />
    </div>
  );
}

function statusText(s: Status, active?: number): string {
  if (s === 'offline') return 'hub unreachable';
  if (s === 'connecting') return 'connecting';
  return `${active ?? 0} active · live`;
}
