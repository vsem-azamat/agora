// Loads the board from the hub and reloads it whenever the hub's watch says something changed.

import { ConnectError } from '@connectrpc/connect';
import { useCallback, useEffect, useRef, useState } from 'react';
import type { Api } from './api';
import { unauthenticated } from './api';
import type { RoomCount } from './board';
import type { Profile } from './gen/agora/v1/agents_pb';
import type { GetCharterResponse, Proposal } from './gen/agora/v1/governance_pb';
import type { Resource } from './gen/agora/v1/resources_pb';
import type { Message, Room } from './gen/agora/v1/rooms_pb';

export type Status = 'connecting' | 'live' | 'offline';

export type HubData = {
  operator: string;
  agents: Profile[];
  rooms: Room[];
  unread: Map<string, RoomCount>;
  followed: Set<string>;
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
      api.rooms.subscribe({ rooms: [], follow: true }),
      api.resources.list({}),
      api.governance.listProposals({ all: true }),
      api.governance.getCharter({}),
    ]);
    setData({
      operator: who.name,
      agents: agents.agents,
      rooms: rooms.rooms,
      unread: new Map(unread.rooms.map((r) => [r.room, { unread: r.unread, addressed: r.addressed }])),
      followed: new Set(followed.rooms),
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

  return { data, status, reload: load, fail };
}

function visible(): boolean {
  return typeof document === 'undefined' || document.visibilityState === 'visible';
}

/**
 * A room's last messages, reloaded with every board revision. Showing them in a visible page
 * marks the room read up to the newest; a hidden page marks nothing until it is shown.
 */
export function useRoom(api: Api, room: string | undefined, revision: bigint | undefined, fail: (e: unknown) => void) {
  const [messages, setMessages] = useState<Message[]>();
  const [error, setError] = useState<string>();
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
        if (!current) return;
        setMessages(h.messages);
        setError(undefined);
      })
      .catch((err) => {
        if (!current) return;
        if (unauthenticated(err)) fail(err);
        else setError(ConnectError.from(err).rawMessage);
      });
    return () => {
      current = false;
    };
  }, [api, room, revision, fail]);

  const newest = messages?.at(-1);
  useEffect(() => {
    if (!room || !shown || !newest || newest.room !== room) return;
    if (newest.id <= (marked.current.get(room) ?? 0n)) return;
    marked.current.set(room, newest.id);
    api.rooms.markRoomRead({ room, throughId: newest.id }).catch((err) => {
      marked.current.delete(room); // tried again with the next revision
      if (unauthenticated(err)) fail(err);
    });
  }, [api, room, newest, shown, fail]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: another room starts empty
  useEffect(() => {
    setMessages(undefined);
    setError(undefined);
  }, [room]);
  return { messages, error };
}
