import { cleanup, fireEvent, render, renderHook, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { App } from './App';
import type { Api } from './api';
import { useRoom } from './useHub';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
  history.replaceState(null, '', '/');
});

// --- a fake hub behind fetch, speaking the Connect protocol with JSON ---------------------

function envelope(flags: number, json: unknown): Uint8Array {
  const body = new TextEncoder().encode(JSON.stringify(json));
  const out = new Uint8Array(5 + body.length);
  out[0] = flags;
  new DataView(out.buffer).setUint32(1, body.length);
  out.set(body, 5);
  return out;
}

const answers: Record<string, unknown> = {
  'WebService/Whoami': { name: 'operator' },
  'AgentService/ListAgents': {
    agents: [
      { name: 'builder', sessionState: 'busy', project: 'example-app', task: 'fixing the login timeout', active: true },
      { name: 'operator', sessionState: 'offline', active: true },
    ],
  },
  'RoomService/ListRooms': { rooms: [{ name: 'general', messages: 2 }] },
  'RoomService/UnreadByRoom': { rooms: [{ room: 'general', unread: 2, addressed: 1 }] },
  'RoomService/Subscribe': { rooms: ['general'] },
  'ResourceService/List': {},
  'GovernanceService/ListProposals': {},
  'GovernanceService/GetCharter': { body: 'Be kind.' },
};

function fakeHub(seen: string[]) {
  return vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input instanceof Request ? input.url : input);
    const proc = url.replace(/^.*\/agora\.v1\./, '');
    const auth = new Headers(init?.headers).get('Authorization');
    seen.push(`${proc} ${auth}`);
    if (proc === 'WebService/Watch') {
      const stream = new ReadableStream<Uint8Array>({
        start(c) {
          c.enqueue(envelope(0, { revision: '1' }));
        },
      });
      return new Response(stream, { status: 200, headers: { 'Content-Type': 'application/connect+json' } });
    }
    return new Response(JSON.stringify(answers[proc] ?? {}), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });
  });
}

describe('App with a hub', () => {
  it('opens the printed address, shows the board and signs out', async () => {
    const seen: string[] = [];
    vi.stubGlobal('fetch', fakeHub(seen));
    history.replaceState(null, '', '/#token=good-token');
    const { container } = render(<App />);
    await waitFor(() => expect(container.querySelector('[data-agent="builder"]')).not.toBeNull());
    expect(container.querySelector('[data-agent="operator"]')).toBeNull();
    expect(location.href).not.toContain('good-token');
    expect(seen.every((s) => s.endsWith('Bearer good-token'))).toBe(true);
    expect(container.querySelector('em.mention')?.textContent).toBe('@1');
    fireEvent.click(screen.getByText('Sign out'));
    expect(screen.getByLabelText('Web token')).toBeTruthy();
    expect(localStorage.getItem('agora.token')).toBeNull();
  });
});

// --- reading a room ----------------------------------------------------------------------

function fakeRoomApi(history: () => Promise<unknown>) {
  const markRoomRead = vi.fn(async () => ({}));
  const api = { rooms: { history: vi.fn(history), markRoomRead } } as unknown as Api;
  return { api, markRoomRead };
}

const msgs = { messages: [{ id: 7n, room: 'example-app', author: 'reviewer', body: 'hello' }] };

describe('useRoom', () => {
  it('marks the room read up to the newest message shown', async () => {
    const { api, markRoomRead } = fakeRoomApi(async () => msgs);
    const fail = vi.fn();
    const { result } = renderHook(() => useRoom(api, 'example-app', 1n, fail));
    await waitFor(() => expect(result.current.messages).toHaveLength(1));
    await waitFor(() => expect(markRoomRead).toHaveBeenCalledWith({ room: 'example-app', throughId: 7n }));
    expect(fail).not.toHaveBeenCalled();
  });

  it('marks nothing while the page is hidden', async () => {
    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
    const { api, markRoomRead } = fakeRoomApi(async () => msgs);
    const { result } = renderHook(() => useRoom(api, 'example-app', 1n, vi.fn()));
    await waitFor(() => expect(result.current.messages).toHaveLength(1));
    expect(markRoomRead).not.toHaveBeenCalled();
    vi.restoreAllMocks();
    document.dispatchEvent(new Event('visibilitychange'));
    await waitFor(() => expect(markRoomRead).toHaveBeenCalled());
  });

  it('shows a missing room as an error without signing out', async () => {
    const { ConnectError, Code } = await import('@connectrpc/connect');
    const { api } = fakeRoomApi(async () => {
      throw new ConnectError('room not found', Code.NotFound);
    });
    const fail = vi.fn();
    const { result } = renderHook(() => useRoom(api, 'nowhere', 1n, fail));
    await waitFor(() => expect(result.current.error).toBe('room not found'));
    expect(fail).not.toHaveBeenCalled();
  });
});
