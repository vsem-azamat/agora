import { type ReactNode, useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react';
import { makeApi, unauthenticated } from './api';
import { boardAgents, heldTurns, liveness, openProposals, projectOf } from './board';
import type { Profile } from './gen/agora/v1/agents_pb';
import type { Room } from './gen/agora/v1/rooms_pb';
import { Icon, type IconName } from './icons';
import { askToNotify, useNotifications } from './notify';
import { applySize, NOTIFY_KEY, type Notify, resolveNotify, resolveSize, SIZE_KEY, useChoice } from './prefs';
import type { Saved } from './tape';
import { applyTheme, resolveTheme, THEME_KEY } from './theme';
import { forgetToken, saveToken, takeTokenFromLocation, tokenFromHash } from './token';
import { useAgentMessages, useHub, useRoom } from './useHub';
import { Board } from './views/board';
import { Charter } from './views/charter';
import { Avatar, OpenAgent } from './views/common';
import { Drawer } from './views/drawer';
import { type Arrival, RoomView } from './views/room';
import { Rooms, roomHref } from './views/rooms';
import { Settings } from './views/settings';
import { AccountMenu, Bar, Palette, type PaletteItem, Sidebar, Tabs, type View } from './views/shell';
import { SignIn } from './views/signin';
import { Turns } from './views/turns';

// --- routes: #/ board, #/rooms, #/rooms/<name>, #/turns, #/charter, #/settings -------------

type Route = { view: View; room?: string };

export function parseRoute(hash: string): Route {
  const path = hash.replace(/^#\/?/, '');
  const [first, second] = path.split('/');
  switch (first) {
    case 'rooms':
      return second ? { view: 'room', room: decodeURIComponent(second) } : { view: 'rooms' };
    case 'turns':
    case 'charter':
    case 'settings':
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

/** Ctrl+K or ⌘K toggles the palette. */
function usePaletteKey(toggle: () => void) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        toggle();
      }
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [toggle]);
}

const openRoom = (room: string) => {
  location.hash = roomHref(room);
};

const views: [string, string, IconName, string][] = [
  ['Board', '#/', 'agent', 'board'],
  ['Rooms', '#/rooms', 'room', 'rooms'],
  ['Turns', '#/turns', 'queue', 'turns'],
  ['Charter', '#/charter', 'charter', 'charter'],
  ['Settings', '#/settings', 'settings', 'settings'],
];
/** What the palette finds: the listed agents, the rooms and the views. */
function paletteItems(agents: Profile[], rooms: Room[], openAgent: (name: string) => void): PaletteItem[] {
  return [
    ...agents.map((a) => ({
      key: `agent ${a.name}`,
      label: a.name,
      sub: `${projectOf(a)} · ${liveness(a)}`,
      mark: <Avatar name={a.name} live={liveness(a)} size="sm" />,
      go: () => openAgent(a.name),
    })),
    ...rooms.map((r) => ({
      key: `room ${r.name}`,
      label: `#${r.name}`,
      sub: 'room',
      mark: <Icon name="room" />,
      go: () => openRoom(r.name),
    })),
    ...views.map(([label, href, icon, key]) => ({
      key: `view ${key}`,
      label,
      sub: 'view',
      mark: <Icon name={icon} />,
      go: () => {
        location.hash = href;
      },
    })),
  ];
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
  // only an explicit choice is stored; main.tsx applies the stored ones when the page loads
  const [theme, chooseTheme] = useChoice(THEME_KEY, resolveTheme, applyTheme);
  const [size, chooseSize] = useChoice(SIZE_KEY, resolveSize, applySize);
  const [notify, chooseNotify] = useChoice(NOTIFY_KEY, resolveNotify);
  const [project, setProject] = useState<string>();
  const [drawer, setDrawer] = useState<string>();
  const [palette, setPalette] = useState(false);
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [arrival, setArrival] = useState<Arrival>();
  const memory = useRef(new Map<string, Saved>()).current;
  const roomName = route.view === 'room' ? route.room : undefined;
  const { messages, error: roomError, markSeen } = useRoom(api, roomName, data?.revision, fail);
  const listed = useMemo(() => (data ? boardAgents(data.agents, data.operator) : undefined), [data]);
  const recent = useAgentMessages(api, drawer, data?.rooms ?? [], fail);
  useNotifications(data?.unread, notify, openRoom);
  usePaletteKey(useCallback(() => setPalette((p) => !p), []));
  const closeDrawer = useCallback(() => setDrawer(undefined), []);
  const operator = data?.operator ?? '';
  const board = data?.board ?? '';
  const reader = useMemo(() => ({ operator, board }), [operator, board]);
  const agents = data?.agents;
  const known = useMemo(() => new Set([...(agents ?? []).map((a) => a.name), operator]), [agents, operator]);
  const rooms = data?.rooms;
  const items = useMemo(() => paletteItems(listed ?? [], rooms ?? [], setDrawer), [listed, rooms]);

  const signOut = () => onSignOut(false);
  const account = data ? (
    <AccountMenu operator={data.operator} theme={theme} onTheme={chooseTheme} onSignOut={signOut} />
  ) : (
    <button type="button" className="btn quiet" onClick={signOut}>
      Sign out
    </button>
  );
  const active = listed?.filter((a) => liveness(a) !== 'offline').length;
  const bar = <Bar status={status} active={active} onSearch={data && (() => setPalette(true))} account={account} />;

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

  // the watch reports the change, which reloads the board and the room
  const post = async (body: string, replyTo?: bigint) => {
    if (!roomName) return;
    try {
      await api.rooms.post({ room: roomName, body, replyTo: replyTo ?? 0n });
    } catch (err) {
      if (unauthenticated(err)) fail(err);
      throw err;
    }
  };
  const follow = (on: boolean) => {
    if (!roomName) return;
    api.rooms.subscribe({ rooms: [roomName], follow: on }).catch(fail);
  };
  const pickProject = (p: string | undefined) => {
    setProject(p);
    if (route.view !== 'board') location.hash = '#/';
  };
  const chooseNotifications = (n: Notify) => {
    if (n !== 'none') void askToNotify();
    chooseNotify(n);
  };
  const arrive = (a: Arrival) => {
    setArrival(a);
    setDrawer(undefined);
    openRoom(a.room);
  };

  let main: ReactNode;
  switch (route.view) {
    case 'room':
      main = (
        <RoomView
          key={roomName}
          name={roomName ?? ''}
          room={data.rooms.find((r) => r.name === roomName)}
          messages={messages}
          error={roomError}
          reader={reader}
          general={data.general}
          agents={data.agents}
          followed={data.followed.has(roomName ?? '')}
          unread={data.unread.get(roomName ?? '')}
          now={now}
          draft={drafts[roomName ?? ''] ?? ''}
          onDraft={(v) => setDrafts((d) => ({ ...d, [roomName ?? '']: v }))}
          onPost={post}
          onFollow={follow}
          onSeen={markSeen}
          memory={memory}
          arrival={arrival}
          onArrived={() => setArrival(undefined)}
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
    case 'settings':
      main = (
        <Settings
          operator={data.operator}
          theme={theme}
          onTheme={chooseTheme}
          size={size}
          onSize={chooseSize}
          notify={notify}
          onNotify={chooseNotifications}
          onSignOut={signOut}
        />
      );
      break;
    default:
      main = <Board agents={listed} now={now} project={project} onProject={setProject} />;
  }

  const unreadTotal = [...data.unread.values()].reduce((n, c) => n + c.unread, 0);

  return (
    <OpenAgent.Provider value={setDrawer}>
      <div className="app">
        {bar}
        <div className="body">
          <Sidebar
            view={route.view}
            room={roomName}
            // the project filter belongs to the board: only one sidebar item is current at a time
            project={route.view === 'board' ? project : undefined}
            agents={listed}
            rooms={data.rooms}
            unread={data.unread}
            followed={data.followed}
            counts={{ active: active ?? 0, turns: heldTurns(data.resources), open: openProposals(data.proposals) }}
            onProject={pickProject}
          />
          <main className="main">{main}</main>
        </div>
        <Tabs view={route.view} unread={unreadTotal} />
        {drawer && (
          <Drawer
            name={drawer}
            agent={data.agents.find((a) => a.name === drawer)}
            operator={data.operator}
            rooms={data.rooms}
            general={data.general}
            recent={recent}
            known={known}
            now={now}
            onClose={closeDrawer}
            onAddress={(room) => {
              setDrafts((d) => ({ ...d, [room]: `@${drawer} ` }));
              arrive({ room, compose: true });
            }}
            onGoto={(room, id) => arrive({ room, jump: id })}
          />
        )}
        {palette && <Palette items={items} onClose={() => setPalette(false)} />}
      </div>
    </OpenAgent.Provider>
  );
}
