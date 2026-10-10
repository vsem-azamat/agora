import { act, cleanup, fireEvent, render, renderHook, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { App, parseRoute } from './App';
import type { Api } from './api';
import { useRoom } from './useHub';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
  history.replaceState(null, '', '/');
  document.documentElement.removeAttribute('data-theme');
  document.documentElement.removeAttribute('data-size');
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
  'WebService/Whoami': { name: 'operator', board: 'agora', generalRoom: 'general' },
  'AgentService/ListAgents': {
    agents: [
      {
        name: 'builder',
        session: 'SESSION_STATE_BUSY',
        project: 'example-app',
        task: 'fixing the login timeout',
        active: true,
      },
      { name: 'reviewer', session: 'SESSION_STATE_IDLE', project: 'example-app', active: true },
      { name: 'operator', session: 'SESSION_STATE_OFFLINE', active: true },
    ],
  },
  'RoomService/ListRooms': {
    rooms: [
      { name: 'example-app', messages: 1 },
      { name: 'general', messages: 2 },
    ],
  },
  'RoomService/History': { messages: [{ id: '1', room: 'general', author: 'builder', body: 'hello' }] },
  'RoomService/UnreadByRoom': { rooms: [{ room: 'general', unread: 2, addressed: 1 }] },
  'RoomService/ListSubscriptions': { rooms: ['general'] },
  'ResourceService/ListResources': {},
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

async function signedIn() {
  vi.stubGlobal('fetch', fakeHub([]));
  history.replaceState(null, '', '/#token=good-token');
  const r = render(<App />);
  await waitFor(() => expect(r.container.querySelector('[data-agent="builder"]')).not.toBeNull());
  return r;
}

const openMenu = () => fireEvent.click(screen.getByRole('button', { name: 'Account: @operator' }));

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
    expect(screen.getByText('live · 2 on the board')).toBeTruthy();
    openMenu();
    expect(document.getElementById('account-menu')?.textContent).toContain('@operator');
    expect(screen.getAllByRole('button', { name: /theme$/ })).toHaveLength(5);
    fireEvent.click(screen.getByRole('button', { name: 'Sign out' }));
    expect(screen.getByLabelText('Web token')).toBeTruthy();
    expect(localStorage.getItem('agora.token')).toBeNull();
  });
});

describe('sidebar', () => {
  it('marks either a room or a project as current, never both', async () => {
    const { container } = await signedIn();
    const current = () => [...container.querySelectorAll('nav.side [aria-current]')].map((e) => e.textContent);
    const side = container.querySelector('nav.side') as HTMLElement;
    fireEvent.click(within(side).getByRole('button', { name: /example-app/ }));
    expect(current()).toEqual(['example-app2']);
    location.hash = '#/rooms/general';
    await waitFor(() => expect(current()).toEqual(['#general@1']));
    location.hash = '#/';
    await waitFor(() => expect(current()).toEqual(['example-app2']));
  });
});

describe('palette', () => {
  it('opens a room by name with Ctrl+K', async () => {
    await signedIn();
    fireEvent.keyDown(document, { key: 'k', ctrlKey: true });
    const input = screen.getByRole('combobox', { name: 'Jump to' });
    fireEvent.change(input, { target: { value: 'gen' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(location.hash).toBe('#/rooms/general');
    expect(screen.queryByRole('dialog', { name: 'Jump to' })).toBeNull();
  });
  it('moves with the arrow keys and opens an agent', async () => {
    await signedIn();
    fireEvent.click(screen.getByText('Jump to an agent, room or view'));
    const input = screen.getByRole('combobox', { name: 'Jump to' });
    expect(document.activeElement).toBe(input);
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    const option = screen.getByRole('option', { selected: true });
    expect(option.textContent).toContain('reviewer');
    expect(input.getAttribute('aria-activedescendant')).toBe(option.id);
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(screen.getByRole('dialog', { name: 'reviewer' })).toBeTruthy();
  });
  it('closes before the drawer under it on Escape', async () => {
    const { container } = await signedIn();
    fireEvent.click(container.querySelector('[data-agent="builder"]') as HTMLElement);
    fireEvent.keyDown(document, { key: 'k', ctrlKey: true });
    fireEvent.keyDown(screen.getByRole('combobox', { name: 'Jump to' }), { key: 'Escape' });
    expect(screen.queryByRole('dialog', { name: 'Jump to' })).toBeNull();
    expect(screen.getByRole('dialog', { name: 'builder' })).toBeTruthy();
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(screen.queryByRole('dialog', { name: 'builder' })).toBeNull();
  });
});

describe('drawer', () => {
  it('takes the focus, keeps Tab inside and gives the focus back', async () => {
    const { container } = await signedIn();
    const row = container.querySelector('[data-agent="builder"]') as HTMLElement;
    row.focus();
    fireEvent.click(row);
    const dialog = screen.getByRole('dialog', { name: 'builder' });
    expect(dialog.getAttribute('aria-modal')).toBe('true');
    const close = within(dialog).getByRole('button', { name: 'Close' });
    expect(document.activeElement).toBe(close);
    expect(dialog.contains(document.activeElement)).toBe(true);
    fireEvent.keyDown(document, { key: 'Tab', shiftKey: true });
    expect(dialog.contains(document.activeElement)).toBe(true);
    fireEvent.click(close);
    expect(document.activeElement).toBe(row);
  });
  it('addresses an agent in its project room', async () => {
    const { container } = await signedIn();
    fireEvent.click(container.querySelector('[data-agent="builder"]') as HTMLElement);
    fireEvent.click(screen.getByText(/Address in #example-app/));
    await waitFor(() => expect(location.hash).toBe('#/rooms/example-app'));
    await waitFor(() =>
      expect((screen.getByLabelText('Message #example-app') as HTMLTextAreaElement).value).toBe('@builder '),
    );
    expect(screen.queryByRole('dialog', { name: 'builder' })).toBeNull();
  });
});

describe('theme', () => {
  it('stores nothing until the operator chooses', async () => {
    await signedIn();
    expect(document.documentElement.dataset.theme).not.toBe('ink');
    expect(localStorage.getItem('agora.theme')).toBeNull();
  });
  it('switches from the account menu, which closes', async () => {
    await signedIn();
    openMenu();
    const ink = screen.getByRole('button', { name: 'Ink theme' });
    fireEvent.pointerEnter(ink);
    fireEvent.click(ink);
    await waitFor(() => expect(document.getElementById('account-menu')).toBeNull());
    expect(document.documentElement.dataset.theme).toBe('ink');
    expect(localStorage.getItem('agora.theme')).toBe('ink');
  });
  it('switches to ink in Settings and remembers it', async () => {
    await signedIn();
    location.hash = '#/settings';
    fireEvent.click(await screen.findByText('Ink'));
    await waitFor(() => expect(document.documentElement.dataset.theme).toBe('ink'));
    expect(localStorage.getItem('agora.theme')).toBe('ink');
  });
});

describe('settings', () => {
  it('keeps a larger text size', async () => {
    await signedIn();
    location.hash = '#/settings';
    fireEvent.click(await screen.findByText('Large'));
    expect(document.documentElement.dataset.size).toBe('l');
    expect(localStorage.getItem('agora.size')).toBe('l');
  });
  it('asks for permission to notify when notifications are chosen', async () => {
    const requestPermission = vi.fn(async () => 'granted');
    vi.stubGlobal('Notification', { permission: 'default', requestPermission });
    await signedIn();
    location.hash = '#/settings';
    fireEvent.click(await screen.findByText('Everything'));
    expect(requestPermission).toHaveBeenCalled();
    expect(localStorage.getItem('agora.notify')).toBe('all');
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
  it('marks the room read up to the newest message seen', async () => {
    const { api, markRoomRead } = fakeRoomApi(async () => msgs);
    const fail = vi.fn();
    const { result } = renderHook(() => useRoom(api, 'example-app', 1n, fail));
    await waitFor(() => expect(result.current.messages).toHaveLength(1));
    expect(markRoomRead).not.toHaveBeenCalled();
    act(() => result.current.markSeen(7n));
    await waitFor(() => expect(markRoomRead).toHaveBeenCalledWith({ room: 'example-app', throughId: 7n }));
    expect(fail).not.toHaveBeenCalled();
  });

  it('marks nothing while the page is hidden', async () => {
    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
    const { api, markRoomRead } = fakeRoomApi(async () => msgs);
    const { result } = renderHook(() => useRoom(api, 'example-app', 1n, vi.fn()));
    await waitFor(() => expect(result.current.messages).toHaveLength(1));
    act(() => result.current.markSeen(7n));
    expect(markRoomRead).not.toHaveBeenCalled();
    vi.restoreAllMocks();
    document.dispatchEvent(new Event('visibilitychange'));
    await waitFor(() => expect(markRoomRead).toHaveBeenCalled());
  });

  it('starts another room empty instead of showing the previous room', async () => {
    const { api } = fakeRoomApi(async () => msgs);
    const { result, rerender } = renderHook(({ room }) => useRoom(api, room, 1n, vi.fn()), {
      initialProps: { room: 'example-app' },
    });
    await waitFor(() => expect(result.current.messages).toHaveLength(1));
    vi.mocked(api.rooms.history).mockReturnValue(new Promise(() => {}) as never);
    rerender({ room: 'general' });
    expect(result.current.messages).toBeUndefined();
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

describe('App', () => {
  it('routes by the fragment', () => {
    expect(parseRoute('#/')).toEqual({ view: 'board' });
    expect(parseRoute('#/rooms/example-app')).toEqual({ view: 'room', room: 'example-app' });
    expect(parseRoute('#/turns')).toEqual({ view: 'turns' });
    expect(parseRoute('#/settings')).toEqual({ view: 'settings' });
  });

  it('asks for a token when none is stored', () => {
    render(<App />);
    expect(screen.getByLabelText('Web token')).toBeTruthy();
  });

  it('forgets a token the hub refuses and asks again', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () =>
          new Response(JSON.stringify({ code: 'unauthenticated', message: 'a valid web token is required' }), {
            status: 401,
            headers: { 'Content-Type': 'application/json' },
          }),
      ),
    );
    history.replaceState(null, '', '/#token=rotated-away');
    render(<App />);
    await waitFor(() => expect(screen.getByLabelText('Web token')).toBeTruthy());
    expect(screen.getByText(/did not accept that token/)).toBeTruthy();
    expect(localStorage.getItem('agora.token')).toBeNull();
    expect(location.href).not.toContain('rotated-away');
  });
});
