// Loads the board from the hub and reloads it whenever the hub's watch says something changed.

import { ConnectError } from '@connectrpc/connect';
import { useCallback, useEffect, useRef, useState } from 'react';
import type { Api } from './api';
import { unauthenticated } from './api';
import { type Mode, modeOf, type RoomCount } from './board';
import type { Profile } from './gen/agora/v1/agents_pb';
import type { GetCharterResponse, Proposal } from './gen/agora/v1/governance_pb';
import type { Resource } from './gen/agora/v1/resources_pb';
import type { Message, Room } from './gen/agora/v1/rooms_pb';

export type Status = 'connecting' | 'live' | 'offline';

type HubData = {
  operator: string;
  /** The name the board posts its own messages under. */
  board: string;
  /** The room every agent follows. */
  general: string;
  agents: Profile[];
  rooms: Room[];
  unread: Map<string, RoomCount>;
  followed: Set<string>;
  /** The mode of each followed room. */
  modes: Map<string, Mode>;
  resources: Resource[];
  proposals: Proposal[];
  charter?: GetCharterResponse;
  revision: bigint;
};

const RETRY_MS = 2000;

export function useHub(api: Api, onUnauthenticated: () => void) {
  const [data, setData] = useState<HubData>();
  const [status, setStatus] = useState<Status>('connecting');
  const loading = useRef<Promise<void> | undefined>(undefined);
  const again = useRef(false);
  const revision = useRef(0n);

  const fail = useCallback(
    (err: unknown) => {
      if (unauthenticated(err)) onUnauthenticated();
      else setStatus('offline');
    },
    [onUnauthenticated],
  );

  const loadOnce = useCallback(async () => {
    const [who, agents, rooms, unread, followed, resources, proposals, charter] = await Promise.all([
      api.web.whoami({}),
      api.agents.listAgents({}),
      api.rooms.listRooms({}),
      api.rooms.unreadByRoom({}),
      api.rooms.listSubscriptions({}),
      api.resources.listResources({}),
      api.governance.listProposals({ all: true }),
      api.governance.getCharter({}),
    ]);
    setData({
      operator: who.name,
      board: who.board,
      general: who.generalRoom,
      agents: agents.agents,
      rooms: rooms.rooms,
      unread: new Map(unread.rooms.map((r) => [r.room, { unread: r.unread, addressed: r.addressed }])),
      followed: new Set(followed.rooms),
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

  return { data, status, fail };
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
