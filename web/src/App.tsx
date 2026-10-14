import { ConnectError } from '@connectrpc/connect';
import { type ReactNode, useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react';
import { makeApi, unauthenticated } from './api';
import { Avatars, MAX_AVATAR, makeAvatarLoader } from './avatars';
import {
  boardAgents,
  heldTurns,
  knownNames,
  liveness,
  type Mode,
  openProposals,
  projectOf,
  roomsTabCount,
  subscriptionMode,
} from './board';
import { bridgePolicy, type Policy } from './bridges';
import { type Draft, forgetDrafts, loadDrafts, saveDrafts } from './drafts';
import type { Profile } from './gen/agora/v1/agents_pb';
import type { Room } from './gen/agora/v1/rooms_pb';
import { Icon, type IconName } from './icons';
import { askToNotify, useNotifications } from './notify';
import { applySize, NOTIFY_KEY, type Notify, resolveNotify, resolveSize, SIZE_KEY, useChoice } from './prefs';
import type { Saved } from './tape';
import { applyTheme, resolveTheme, THEME_KEY } from './theme';
import { forgetToken, saveToken, takeTokenFromLocation, tokenFromHash } from './token';
import { useAgentMessages, useHub, useRoom, useThread } from './useHub';
import { Board } from './views/board';
import { Charter } from './views/charter';
import { Avatar, OpenAgent, Profiles } from './views/common';
import { Drawer } from './views/drawer';
import type { Decide, SendOut } from './views/message';
import { type Arrival, RoomView } from './views/room';
import { Rooms, roomHref } from './views/rooms';
import { Settings } from './views/settings';
import { AccountMenu, Bar, Palette, type PaletteItem, Sidebar, Tabs, type View } from './views/shell';
import { SignIn } from './views/signin';
import { threadHref } from './views/thread';
import { Turns } from './views/turns';

// --- routes: #/ board, #/rooms, #/archive, #/rooms/<name>[/thread/<root>], #/turns, #/charter, #/settings ---

/** Where the app is; the Rooms view is on its archived rooms at #/archive, a name no room can clash with. */
type Route = { view: View; room?: string; archived?: true; thread?: bigint };

/**
 * How opening or closing a thread moves in the browser's history. A thread opened from its room is
 * a new entry, so the phone's Back returns to the room; closing it goes back to that room's entry
 * instead of adding one. Moving to another thread, or closing a thread reached by its address,
 * replaces the entry. `pushed` is the address of the thread opened over its room, kept between calls.
 */
export function moveThread(
  hash: string,
  pushed: string | undefined,
  room: string,
  open: bigint | undefined,
  root: bigint | undefined,
): { push?: string; replace?: string; back?: true; pushed?: string } {
  if (root === undefined) {
    if (open === undefined) return { pushed };
    return pushed === hash ? { back: true } : { replace: roomHref(room) };
  }
  const to = threadHref(room, root);
  if (open === undefined) return { push: to, pushed: to };
  return { replace: to, pushed: pushed === hash ? to : pushed };
}

export function parseRoute(hash: string): Route {
  const path = hash.replace(/^#\/?/, '');
  const [first, second, third, fourth] = path.split('/');
  switch (first) {
    case 'rooms':
      if (!second) return { view: 'rooms' };
      if (third === 'thread' && fourth && /^\d+$/.test(fourth))
        return { view: 'room', room: decodeURIComponent(second), thread: BigInt(fourth) };
      return { view: 'room', room: decodeURIComponent(second) };
    case 'archive':
      return { view: 'rooms', archived: true };
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

function redraft(drafts: Record<string, Draft>, room: string, change: Partial<Draft>): Record<string, Draft> {
  return { ...drafts, [room]: { text: '', note: false, ...drafts[room], ...change } };
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
function paletteItems(
  agents: Profile[],
  rooms: Room[],
  archived: Room[],
  bridged: ReadonlySet<string>,
  openAgent: (name: string) => void,
): PaletteItem[] {
  return [
    ...agents.map((a) => ({
      key: `agent ${a.name}`,
      label: a.name,
      sub: `${projectOf(a)} · ${liveness(a)}`,
      mark: <Avatar name={a.name} agent={a} live={liveness(a)} size="sm" />,
      go: () => openAgent(a.name),
    })),
    ...rooms.map((r) => ({
      key: `room ${r.name}`,
      label: `#${r.name}`,
      sub: bridged.has(r.name) ? 'bridged room' : 'room',
      mark: <Icon name={bridged.has(r.name) ? 'bridge' : 'room'} />,
      go: () => openRoom(r.name),
    })),
    ...archived.map((r) => ({
      key: `room ${r.name}`,
      label: `#${r.name}`,
      sub: 'archived room',
      mark: <Icon name="archive" />,
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
    // a refused token keeps the drafts for the next sign-in; signing out drops them
    if (!wasRejected) forgetDrafts();
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

/** The page itself, so tests can see a reload. */
export const page = { reload: () => location.reload() };

function Signed({ token, onSignOut }: { token: string; onSignOut: (rejected: boolean) => void }) {
  const api = useMemo(() => makeApi(token), [token]);
  const avatars = useMemo(() => makeAvatarLoader(token), [token]);
  const rejected = useCallback(() => onSignOut(true), [onSignOut]);
  const { data, status, fail, upgraded } = useHub(api, rejected);
  const route = useRoute();
  const now = useNow();
  // only an explicit choice is stored; main.tsx applies the stored ones when the page loads
  const [theme, chooseTheme] = useChoice(THEME_KEY, resolveTheme, applyTheme);
  const [size, chooseSize] = useChoice(SIZE_KEY, resolveSize, applySize);
  const [notify, chooseNotify] = useChoice(NOTIFY_KEY, resolveNotify);
  const [project, setProject] = useState<string>();
  const [drawer, setDrawer] = useState<string>();
  const [palette, setPalette] = useState(false);
  // what the operator was writing in each room, and whether it is a note, kept for the tab
  const [drafts, setDrafts] = useState(loadDrafts);
  useEffect(() => saveDrafts(drafts), [drafts]);
  // an upgraded hub serves a newer app: load it, once no draft would be lost
  const writing = Object.values(drafts).some((d) => d.text.trim() !== '');
  useEffect(() => {
    if (upgraded && !writing) page.reload();
  }, [upgraded, writing]);
  const [arrival, setArrival] = useState<Arrival>();
  // each notice has its own number, so the same text told again shows for its full time
  const [notice, setNotice] = useState<{ text: string; n: number; where: string }>();
  const closeNotice = useCallback(() => setNotice(undefined), []);
  const memory = useRef(new Map<string, Saved>()).current;
  const roomName = route.view === 'room' ? route.room : undefined;
  // a notice belongs to the view where the operator asked: it is shown only there
  const where = `${route.view} ${roomName ?? ''} ${route.archived ?? ''}`;
  const asked = useRef(where);
  asked.current = where;
  const shown = notice?.where === where ? notice : undefined;
  // and is dropped on leaving it, so it does not come back later
  // biome-ignore lint/correctness/useExhaustiveDependencies: cleared whenever the view changes
  useEffect(() => setNotice(undefined), [where]);
  // a room left with nothing written opens again on To <chat>; a draft keeps its choice
  useEffect(() => {
    if (!roomName) return;
    return () =>
      setDrafts((d) => (d[roomName]?.note && !d[roomName].text.trim() ? redraft(d, roomName, { note: false }) : d));
  }, [roomName]);
  const { messages, error: roomError, markSeen } = useRoom(api, roomName, data?.revision, fail);
  const threadRoot = route.view === 'room' ? route.thread : undefined;
  // the address of the thread this app opened over its room, as a history entry of its own
  const pushedThread = useRef<string>(undefined);
  const thread = useThread(api, roomName, threadRoot, data?.revision, fail);
  const listed = useMemo(() => (data ? boardAgents(data.agents, data.operator) : undefined), [data]);
  const recent = useAgentMessages(api, drawer, data?.rooms ?? [], fail);
  const modes = data?.modes;
  const wake = useMemo(
    () => new Set([...(modes ?? [])].filter(([, m]) => m === 'wake').map(([room]) => room)),
    [modes],
  );
  useNotifications(data?.unread, notify, openRoom, wake);
  usePaletteKey(useCallback(() => setPalette((p) => !p), []));
  const closeDrawer = useCallback(() => setDrawer(undefined), []);
  const operator = data?.operator ?? '';
  const board = data?.board ?? '';
  const reader = useMemo(() => ({ operator, board }), [operator, board]);
  const agents = data?.agents;
  const known = useMemo(() => knownNames(agents ?? [], operator), [agents, operator]);
  const profiles = useMemo(() => new Map((agents ?? []).map((a) => [a.name, a])), [agents]);
  const rooms = data?.rooms;
  const bridges = data?.bridges;
  const bridged = useMemo(() => new Set(bridges?.keys()), [bridges]);
  const archivedRooms = data?.archived;
  const items = useMemo(
    () => paletteItems(listed ?? [], rooms ?? [], archivedRooms ?? [], bridged, setDrawer),
    [listed, rooms, archivedRooms, bridged],
  );
  /**
   * A call the operator made failed: a refused token signs out, anything else is told in the view
   * where the call was made, so it is called when the call is made, not when its answer comes.
   */
  const refused = useCallback(
    (what: string) => {
      const at = asked.current;
      return (err: unknown) => {
        if (unauthenticated(err)) fail(err);
        else
          setNotice((was) => ({
            text: `${what}: ${ConnectError.from(err).rawMessage}`,
            n: (was?.n ?? 0) + 1,
            where: at,
          }));
      };
    },
    [fail],
  );
  // the watch reports the change, which reloads the room with the message's new state
  const sendOut = useCallback<SendOut>(
    async (m) => {
      const tell = refused('Not sent out');
      try {
        await api.bridges.sendNoteOut({ messageId: m.id });
        return true;
      } catch (err) {
        tell(err);
        return false;
      }
    },
    [api, refused],
  );
  const decide = useCallback<Decide>(
    async (m, send) => {
      const tell = refused(send ? 'Not sent' : 'Not cancelled');
      try {
        if (send) await api.bridges.sendScheduled({ messageId: m.id });
        else await api.bridges.cancelScheduled({ messageId: m.id });
        return true;
      } catch (err) {
        tell(err);
        return false;
      }
    },
    [api, refused],
  );

  /** Changes the operator's avatar; resolves to the reason when it is refused. */
  const changeAvatar = useCallback(
    async (change: () => Promise<unknown>): Promise<string | undefined> => {
      try {
        await change();
        return undefined;
      } catch (err) {
        if (unauthenticated(err)) fail(err);
        return ConnectError.from(err).rawMessage;
      }
    },
    [fail],
  );
  const setAvatar = useCallback(
    async (file: File) => {
      if (file.size > MAX_AVATAR)
        return `An avatar is at most ${MAX_AVATAR / 1024} KiB; this file is ${Math.ceil(file.size / 1024)} KiB`;
      const image = new Uint8Array(await file.arrayBuffer());
      return changeAvatar(() => api.agents.setAvatar({ agent: operator, image }));
    },
    [api, operator, changeAvatar],
  );
  const clearAvatar = useCallback(
    () => changeAvatar(() => api.agents.clearAvatar({ agent: operator })),
    [api, operator, changeAvatar],
  );

  const signOut = () => onSignOut(false);
  const account = data ? (
    <AccountMenu
      operator={data.operator}
      theme={theme}
      onTheme={chooseTheme}
      onSignOut={signOut}
      hasAvatar={!!profiles.get(data.operator)?.avatar}
      onAvatar={setAvatar}
      onClearAvatar={clearAvatar}
    />
  ) : (
    <button type="button" className="btn quiet" onClick={signOut}>
      Sign out
    </button>
  );
  const active = listed?.filter((a) => liveness(a) !== 'offline').length;
  const bar = (
    <Bar
      status={status}
      active={active}
      onSearch={data && (() => setPalette(true))}
      onReload={upgraded ? page.reload : undefined}
      account={account}
    />
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

  // the watch reports the change, which reloads the board and the room
  const post = async (body: string, replyTo: bigint | undefined, note: boolean) => {
    if (!roomName) return;
    try {
      await api.rooms.post({ room: roomName, body, replyTo: replyTo ?? 0n, note });
    } catch (err) {
      if (unauthenticated(err)) fail(err);
      throw err;
    }
  };
  const follow = (on: boolean, mode?: Mode) => {
    if (!roomName) return;
    // asked within the operator's click, as browsers require
    if (mode === 'wake' && notify !== 'none') void askToNotify();
    api.rooms.subscribe({ rooms: [roomName], follow: on, mode: mode && subscriptionMode(mode) }).catch(fail);
  };
  const archive = (room: string, archived: boolean) => {
    api.rooms
      .archiveRoom({ room, archived })
      .catch(refused(archived ? 'The room was not archived' : 'The room was not unarchived'));
  };
  const setPolicy = (policy: Policy) => {
    if (!roomName) return;
    api.bridges
      .setBridgePolicy({ name: roomName, policy: bridgePolicy(policy) })
      .catch(refused('The policy did not change'));
  };
  const openThread = (root: bigint | undefined) => {
    if (!roomName) return;
    const to = moveThread(location.hash, pushedThread.current, roomName, threadRoot, root);
    pushedThread.current = to.pushed;
    if (to.back) history.back();
    else if (to.replace) location.replace(to.replace);
    else if (to.push) location.hash = to.push;
  };
  const openThreadProps = roomName &&
    threadRoot !== undefined && {
      root: threadRoot,
      messages: thread.messages,
      error: thread.error,
      onPost: async (body: string, also: boolean, note: boolean) => {
        try {
          await api.rooms.post({ room: roomName, body, thread: threadRoot, also, note });
        } catch (err) {
          if (unauthenticated(err)) fail(err);
          throw err;
        }
      },
      onTitle: (title: string) => {
        api.rooms.setThreadTitle({ messageId: threadRoot, title }).catch(refused('The thread was not titled'));
      },
      onLeave: () => {
        api.rooms
          .leaveThread({ messageId: threadRoot })
          .then(() => openThread(undefined))
          .catch(refused('The thread was not left'));
      },
      onSeen: thread.markSeen,
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
          room={[...data.rooms, ...data.archived].find((r) => r.name === roomName)}
          bridge={data.bridges.get(roomName ?? '')}
          messages={messages}
          error={roomError}
          reader={reader}
          general={data.general}
          agents={data.agents}
          followed={data.followed.has(roomName ?? '')}
          mode={data.modes.get(roomName ?? '')}
          unread={data.unread.get(roomName ?? '')}
          now={now}
          draft={drafts[roomName ?? '']?.text ?? ''}
          onDraft={(text) => setDrafts((d) => redraft(d, roomName ?? '', { text }))}
          note={drafts[roomName ?? '']?.note ?? false}
          onNote={(note) => setDrafts((d) => redraft(d, roomName ?? '', { note }))}
          onPost={post}
          onFollow={follow}
          onSeen={markSeen}
          onDecide={decide}
          onSendOut={sendOut}
          onPolicy={setPolicy}
          onArchive={(archived) => roomName && archive(roomName, archived)}
          notice={shown}
          onCloseNotice={closeNotice}
          memory={memory}
          arrival={arrival}
          onArrived={() => setArrival(undefined)}
          thread={openThreadProps || undefined}
          threadUnread={data.unread.get(roomName ?? '')?.threads}
          unreadThreads={data.threads.filter((t) => t.room === roomName)}
          onThread={openThread}
        />
      );
      break;
    case 'rooms':
      main = (
        <Rooms
          tab={route.archived ? 'archived' : 'active'}
          rooms={data.rooms}
          archived={data.archived}
          onUnarchive={(room) => archive(room, false)}
          unread={data.unread}
          followed={data.followed}
          bridges={data.bridges}
          threads={data.threads}
          now={now}
          notice={shown}
          onCloseNotice={closeNotice}
        />
      );
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
          hub={{ version: data.version, commit: data.commit }}
        />
      );
      break;
    default:
      main = <Board agents={listed} now={now} project={project} onProject={setProject} />;
  }

  const unreadTotal = roomsTabCount(data.unread);

  return (
    <Avatars.Provider value={avatars}>
      <Profiles.Provider value={profiles}>
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
                archived={data.archived.length}
                unread={data.unread}
                followed={data.followed}
                bridges={data.bridges}
                threads={data.threads}
                thread={threadRoot}
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
                  // addressing an agent in a bridged room is a note for it, not a message to the chat
                  setDrafts((d) => ({ ...d, [room]: { text: `@${drawer} `, note: bridged.has(room) } }));
                  arrive({ room, compose: true });
                }}
                onGoto={(room, id) => arrive({ room, jump: id })}
              />
            )}
            {palette && <Palette items={items} onClose={() => setPalette(false)} />}
          </div>
        </OpenAgent.Provider>
      </Profiles.Provider>
    </Avatars.Provider>
  );
}
