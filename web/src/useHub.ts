// Loads the board from the hub and reloads it whenever the hub's watch says something changed.

import { ConnectError } from '@connectrpc/connect';
import { useCallback, useEffect, useRef, useState } from 'react';
import type { Api } from './api';
import { unauthenticated } from './api';
import { type Mode, modeOf, type RoomCount } from './board';
import type { Profile } from './gen/agora/v1/agents_pb';
import type { Bridge } from './gen/agora/v1/bridges_pb';
import type { GetCharterResponse, Proposal } from './gen/agora/v1/governance_pb';
import type { Resource } from './gen/agora/v1/resources_pb';
import type { Message, Room, Thread } from './gen/agora/v1/rooms_pb';

export type Status = 'connecting' | 'live' | 'offline';

type HubData = {
  operator: string;
  /** The name the board posts its own messages under. */
  board: string;
  /** The room every agent follows. */
  general: string;
  /** The hub's version and the commit it was built from. */
  version: string;
  commit: string;
  agents: Profile[];
  /** The rooms that are not archived. */
  rooms: Room[];
  /** The archived rooms. */
  archived: Room[];
  /** Per room: the operator's unread and addressed counts, and the messages waiting for the operator to send. */
  unread: Map<string, RoomCount>;
  /** The bridges by the name of their room. */
  bridges: Map<string, Bridge>;
  followed: Set<string>;
  /**
   * The titled threads of the rooms that are not archived, and the untitled ones with unread
   * replies for the operator, the most recently active first.
   */
  threads: Thread[];
  /** The mode of each followed room. */
  modes: Map<string, Mode>;
  resources: Resource[];
  proposals: Proposal[];
  charter?: GetCharterResponse;
  revision: bigint;
};

const RETRY_MS = 2000;

// The hub's build (`version commit`) the page was loaded with, kept for the page rather than for
// a sign-in, so signing in again cannot take an upgraded hub's build for the page's own.
let loadedWith: string | undefined;

/** Forgets the page's build, as a new page would; for tests. */
export function forgetLoadedBuild() {
  loadedWith = undefined;
}

/** The counts per room: unread, addressed and thread replies from the hub's unread counts, scheduled from the rooms. */
export function roomCounts(
  unread: { room: string; unread: number; addressed: number; threads?: number }[],
  rooms: Pick<Room, 'name' | 'scheduled'>[],
): Map<string, RoomCount> {
  const out = new Map<string, RoomCount>(
    unread.map((r) => [r.room, { unread: r.unread, addressed: r.addressed, threads: r.threads || undefined }]),
  );
  for (const r of rooms) {
    if (r.scheduled > 0)
      out.set(r.name, { ...(out.get(r.name) ?? { unread: 0, addressed: 0 }), scheduled: r.scheduled });
  }
  return out;
}

export function useHub(api: Api, onUnauthenticated: () => void) {
  const [data, setData] = useState<HubData>();
  const [status, setStatus] = useState<Status>('connecting');
  const loading = useRef<Promise<void> | undefined>(undefined);
  const again = useRef(false);
  const revision = useRef(0n);
  const [upgraded, setUpgraded] = useState(false);

  const fail = useCallback(
    (err: unknown) => {
      if (unauthenticated(err)) onUnauthenticated();
      else setStatus('offline');
    },
    [onUnauthenticated],
  );

  const loadOnce = useCallback(async () => {
    const [who, agents, rooms, unread, followed, resources, proposals, charter, bridges, threads] = await Promise.all([
      api.web.whoami({}),
      api.agents.listAgents({}),
      api.rooms.listRooms({ all: true }),
      api.rooms.unreadByRoom({}),
      api.rooms.listSubscriptions({}),
      api.resources.listResources({}),
      api.governance.listProposals({ all: true }),
      api.governance.getCharter({}),
      api.bridges.listBridges({}),
      api.rooms.listThreads({ unread: true }),
    ]);
    const build = `${who.version} ${who.commit}`;
    loadedWith ??= build;
    if (build !== loadedWith) setUpgraded(true);
    const active = rooms.rooms.filter((r) => !r.archivedBy);
    setData({
      operator: who.name,
      board: who.board,
      general: who.generalRoom,
      version: who.version,
      commit: who.commit,
      agents: agents.agents,
      rooms: active,
      archived: rooms.rooms.filter((r) => r.archivedBy),
      // nothing of an archived room counts: its unread are none, and what waits there cannot go out
      unread: roomCounts(unread.rooms, active),
      bridges: new Map(bridges.bridges.map((b) => [b.name, b])),
      followed: new Set(followed.rooms),
      threads: threads.threads,
      modes: new Map(followed.subscriptions.map((s) => [s.room, modeOf(s.mode)])),
      resources: resources.resources,
      proposals: proposals.proposals,
      charter,
      revision: revision.current,
    });
  }, [api]);

  // load reloads the board; calls during a load make one more load after it.
  const load = useCallback(async () => {
    if (loading.current) {
      again.current = true;
      return loading.current;
    }
    const run = (async () => {
      do {
        again.current = false;
        await loadOnce();
      } while (again.current);
    })();
    loading.current = run;
    try {
      await run;
    } finally {
      loading.current = undefined;
    }
  }, [loadOnce]);

  useEffect(() => {
    const abort = new AbortController();
    (async () => {
      while (!abort.signal.aborted) {
        try {
          for await (const m of api.web.watch({}, { signal: abort.signal })) {
            revision.current = m.revision;
            await load();
            setStatus('live');
          }
        } catch (err) {
          if (abort.signal.aborted) return;
          fail(err);
          if (unauthenticated(err)) return;
        }
        setStatus((s) => (s === 'live' ? 'connecting' : s));
        await new Promise((r) => setTimeout(r, RETRY_MS));
      }
    })();
    return () => abort.abort();
  }, [api, load, fail]);

  return { data, status, fail, upgraded };
}

function visible(): boolean {
  return typeof document === 'undefined' || document.visibilityState === 'visible';
}

/**
 * A room's last messages, reloaded with every board revision. The room view reports the newest
 * message the operator has seen with `markSeen`; the room is marked read up to it while the page
 * is visible, and a hidden page marks nothing until it is shown.
 */
export function useRoom(api: Api, room: string | undefined, revision: bigint | undefined, fail: (e: unknown) => void) {
  // what was loaded is kept with its room, so another room starts empty until its own load
  const [loaded, setLoaded] = useState<{ room: string; messages?: Message[]; error?: string }>();
  const [seen, setSeen] = useState<{ room: string; id: bigint }>();
  const [shown, setShown] = useState(visible);
  const marked = useRef(new Map<string, bigint>());

  useEffect(() => {
    const onChange = () => setShown(visible());
    document.addEventListener('visibilitychange', onChange);
    return () => document.removeEventListener('visibilitychange', onChange);
  }, []);

  // biome-ignore lint/correctness/useExhaustiveDependencies: a new board revision reloads the room
  useEffect(() => {
    if (!room) return;
    let current = true;
    api.rooms
      .history({ room, last: 100 })
      .then((h) => {
        if (current) setLoaded({ room, messages: h.messages });
      })
      .catch((err) => {
        if (!current) return;
        if (unauthenticated(err)) fail(err);
        else
          setLoaded((l) => ({
            room,
            messages: l?.room === room ? l.messages : undefined,
            error: ConnectError.from(err).rawMessage,
          }));
      });
    return () => {
      current = false;
    };
  }, [api, room, revision, fail]);

  const markSeen = useCallback(
    (id: bigint) => {
      if (room) setSeen((s) => (s?.room === room && s.id >= id ? s : { room, id }));
    },
    [room],
  );

  const through = room !== undefined && seen?.room === room ? seen.id : undefined;
  useEffect(() => {
    if (!room || !shown || through === undefined) return;
    if (through <= (marked.current.get(room) ?? 0n)) return;
    marked.current.set(room, through);
    api.rooms.markRoomRead({ room, throughId: through }).catch((err) => {
      marked.current.delete(room); // tried again with the next report
      if (unauthenticated(err)) fail(err);
    });
  }, [api, room, through, shown, fail]);

  const mine = room !== undefined && loaded?.room === room ? loaded : undefined;
  return { messages: mine?.messages, error: mine?.error, markSeen };
}

/**
 * The thread of root in room: its root and last replies, reloaded with every board revision. The
 * panel reports the newest reply it shows with `markSeen`; the room is marked read up to it, which
 * reads the thread's replies up to it, while the page is visible. Each thread keeps its own mark,
 * and a hidden page marks nothing until it is shown. A root of another room is refused.
 */
export function useThread(
  api: Api,
  room: string | undefined,
  root: bigint | undefined,
  revision: bigint | undefined,
  fail: (e: unknown) => void,
) {
  const [loaded, setLoaded] = useState<{ root: bigint; messages?: Message[]; error?: string }>();
  const [seen, setSeen] = useState<{ root: bigint; id: bigint }>();
  const [shown, setShown] = useState(visible);
  const marked = useRef(new Map<bigint, bigint>());

  useEffect(() => {
    const onChange = () => setShown(visible());
    document.addEventListener('visibilitychange', onChange);
    return () => document.removeEventListener('visibilitychange', onChange);
  }, []);

  // biome-ignore lint/correctness/useExhaustiveDependencies: a new board revision reloads the thread
  useEffect(() => {
    if (!room || root === undefined) return;
    let current = true;
    api.rooms
      .history({ room, thread: root, last: 100 })
      .then((h) => {
        if (!current) return;
        // the hub reads a thread by its root alone; one of another room is not this room's
        const first = h.messages[0];
        if (first && first.room !== room) setLoaded({ root, error: `message ${root} is not in #${room}` });
        else setLoaded({ root, messages: h.messages });
      })
      .catch((err) => {
        if (!current) return;
        if (unauthenticated(err)) fail(err);
        else setLoaded({ root, error: ConnectError.from(err).rawMessage });
      });
    return () => {
      current = false;
    };
  }, [api, room, root, revision, fail]);

  const markSeen = useCallback(
    (id: bigint) => {
      if (root !== undefined) setSeen((s) => (s?.root === root && s.id >= id ? s : { root, id }));
    },
    [root],
  );

  const through = root !== undefined && seen?.root === root ? seen.id : undefined;
  useEffect(() => {
    if (!room || root === undefined || !shown || through === undefined) return;
    if (through <= (marked.current.get(root) ?? 0n)) return;
    marked.current.set(root, through);
    api.rooms.markRoomRead({ room, throughId: through }).catch((err) => {
      marked.current.delete(root); // tried again with the next report
      if (unauthenticated(err)) fail(err);
    });
  }, [api, room, root, through, shown, fail]);

  const mine = root !== undefined && loaded?.root === root ? loaded : undefined;
  return { messages: mine?.messages, error: mine?.error, markSeen };
}

/** How many recent messages of an agent its drawer shows. */
const RECENT = 3;
/** The drawer reads the last messages of this many rooms, the most recently active first. */
const RECENT_ROOMS = 10;
const RECENT_DEPTH = 50;

/**
 * An agent's latest messages, newest first, read when the drawer opens from the history of the
 * most recently active rooms (at most RECENT_ROOMS calls of RECENT_DEPTH messages).
 */
export function useAgentMessages(api: Api, agent: string | undefined, rooms: Room[], fail: (e: unknown) => void) {
  const [found, setFound] = useState<{ agent: string; messages: Message[] }>();
  const active = rooms
    .filter((r) => r.lastAt)
    .sort((a, b) => Number((b.lastAt?.seconds ?? 0n) - (a.lastAt?.seconds ?? 0n)))
    .slice(0, RECENT_ROOMS)
    .map((r) => r.name);
  const key = active.join('\n');
  // biome-ignore lint/correctness/useExhaustiveDependencies: the rooms are compared by their names
  useEffect(() => {
    if (!agent) return;
    let current = true;
    Promise.all(active.map((room) => api.rooms.history({ room, last: RECENT_DEPTH })))
      .then((hs) => {
        if (!current) return;
        const mine = hs.flatMap((h) => h.messages).filter((m) => m.author === agent);
        mine.sort((a, b) => Number(b.id - a.id)); // ids grow with every message, in every room
        setFound({ agent, messages: mine.slice(0, RECENT) });
      })
      .catch((err) => {
        if (current && unauthenticated(err)) fail(err);
      });
    return () => {
      current = false;
    };
  }, [api, agent, key, fail]);
  return found && found.agent === agent ? found.messages : undefined;
}
