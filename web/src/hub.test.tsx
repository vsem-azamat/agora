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
        icon: 'anvil',
        pigment: 'terracotta',
        formerly: [{ name: 'fixer', renamedAt: '2026-10-09T11:00:00Z' }],
      },
      { name: 'reviewer', session: 'SESSION_STATE_IDLE', project: 'example-app', active: true },
      { name: 'operator', session: 'SESSION_STATE_OFFLINE', active: true, icon: 'olive', pigment: 'umber' },
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
  'RoomService/ListSubscriptions': {
    rooms: ['general'],
    subscriptions: [{ room: 'general', mode: 'SUBSCRIPTION_MODE_MENTIONS' }],
  },
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

describe('following', () => {
  it('shows the mode of a room and changes it', async () => {
    const bodies: string[] = [];
    const hub = fakeHub([]);
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        if (String(input).endsWith('RoomService/Subscribe'))
          bodies.push(typeof init?.body === 'string' ? init.body : new TextDecoder().decode(init?.body as Uint8Array));
        return hub(input, init);
      }),
    );
    history.replaceState(null, '', '/#token=good-token');
    const { container } = render(<App />);
    await waitFor(() => expect(container.querySelector('[data-agent="builder"]')).not.toBeNull());
    vi.stubGlobal('Notification', { permission: 'default', requestPermission: vi.fn(async () => 'granted') });
    location.hash = '#/rooms/general';
    fireEvent.click(await screen.findByRole('button', { name: 'Follow mode: Mentions only' }));
    fireEvent.click(screen.getByRole('menuitemradio', { name: 'Every message notifies' }));
    expect(Notification.requestPermission).toHaveBeenCalledOnce();
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(JSON.parse(bodies[0] ?? '{}')).toEqual({ rooms: ['general'], follow: true, mode: 'SUBSCRIPTION_MODE_WAKE' });
  });
});

describe('bridged rooms', () => {
  /** The fake hub with #example-chat bridged under `policy`, one message from outside and one waiting. */
  function bridgedHub(policy: string, calls: [string, unknown][]) {
    const hub = fakeHub([]);
    const reply = (json: unknown, status = 200) =>
      new Response(JSON.stringify(json), { status, headers: { 'Content-Type': 'application/json' } });
    return vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const proc = String(input).replace(/^.*\/agora\.v1\./, '');
      const raw = typeof init?.body === 'string' ? init.body : new TextDecoder().decode(init?.body as Uint8Array);
      switch (proc) {
        case 'BridgeService/ListBridges':
          return reply({ bridges: [{ name: 'example-chat', policy, state: 'BRIDGE_STATE_RUNNING' }] });
        case 'RoomService/ListRooms':
          return reply({
            rooms: [
              { name: 'example-chat', messages: 2, pending: 1 },
              { name: 'general', messages: 2 },
            ],
          });
        case 'RoomService/History':
          return reply({
            messages: [
              {
                id: '6',
                room: 'example-chat',
                body: 'can you look?',
                externalAuthor: { bridge: 'example-chat', id: '42', name: 'Ada' },
              },
              {
                id: '7',
                room: 'example-chat',
                author: 'builder',
                body: 'looked, all fine',
                replyTo: '6',
                deliveryState: 'DELIVERY_STATE_PENDING',
              },
            ],
          });
        case 'BridgeService/SendPending':
          calls.push([proc, JSON.parse(raw)]);
          // the fake has the message pending only under approve; another policy stands for a
          // decision taken meanwhile
          return policy === 'BRIDGE_POLICY_APPROVE'
            ? reply({})
            : reply({ code: 'not_found', message: 'message 7 is not pending' }, 404);
        case 'BridgeService/SetBridgePolicy':
          calls.push([proc, JSON.parse(raw)]);
          return reply({});
      }
      return hub(input, init);
    });
  }

  const waiting = (c: HTMLElement) => c.querySelector('[data-mid="7"]') as HTMLElement;

  async function openChat(policy: string, calls: [string, unknown][]) {
    vi.stubGlobal('fetch', bridgedHub(policy, calls));
    history.replaceState(null, '', '/#token=good-token');
    const r = render(<App />);
    await waitFor(() => expect(r.container.querySelector('[data-agent="builder"]')).not.toBeNull());
    location.hash = '#/rooms/example-chat';
    await waitFor(() => expect(r.container.querySelector('[data-mid="7"]')).not.toBeNull());
    return r;
  }

  it('shows the waiting message in the room list and sends it', async () => {
    const calls: [string, unknown][] = [];
    const { container } = await openChat('BRIDGE_POLICY_APPROVE', calls);
    const link = container.querySelector('.side a[href="#/rooms/example-chat"]') as HTMLElement;
    expect(link.querySelector('svg.br title')?.textContent).toBe('bridge running');
    expect(link.querySelector('em.pending')?.textContent).toBe('1 waiting for you to send');
    expect(container.querySelector('[data-mid="6"] .mh')?.textContent).toContain('Ada · example-chat');
    fireEvent.click(within(waiting(container)).getByRole('button', { name: 'Send' }));
    await waitFor(() => expect(calls).toEqual([['BridgeService/SendPending', { messageId: '7' }]]));
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('keeps Send from a read-only room and says why', async () => {
    const { container } = await openChat('BRIDGE_POLICY_READ', []);
    const t = waiting(container);
    expect((within(t).getByRole('button', { name: 'Send' }) as HTMLButtonElement).disabled).toBe(true);
    expect(t.querySelector('.decide .hint')?.textContent).toBe('read only — switch Outbound to send');
    expect((within(t).getByRole('button', { name: 'Don’t send' }) as HTMLButtonElement).disabled).toBe(false);
  });

  it('says why the hub refused to send a message', async () => {
    const calls: [string, unknown][] = [];
    const { container } = await openChat('BRIDGE_POLICY_OPEN', calls);
    fireEvent.click(within(waiting(container)).getByRole('button', { name: 'Send' }));
    const notice = await screen.findByRole('alert');
    expect(notice.textContent).toBe('Not sent: message 7 is not pending');
    await waitFor(() =>
      expect((within(waiting(container)).getByRole('button', { name: 'Send' }) as HTMLButtonElement).disabled).toBe(
        false,
      ),
    ); // it still waits
    fireEvent.click(within(notice).getByRole('button', { name: 'Dismiss' }));
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('changes the outbound policy', async () => {
    const calls: [string, unknown][] = [];
    await openChat('BRIDGE_POLICY_APPROVE', calls);
    fireEvent.click(screen.getByRole('button', { name: 'Outbound: Ask before sending' }));
    fireEvent.click(screen.getByRole('menuitemradio', { name: 'Send at once' }));
    await waitFor(() =>
      expect(calls).toEqual([
        ['BridgeService/SetBridgePolicy', { name: 'example-chat', policy: 'BRIDGE_POLICY_OPEN' }],
      ]),
    );
  });
});

describe('sigils', () => {
  it('draws agents and the operator as they chose, everywhere', async () => {
    const { container } = await signedIn();
    const sigil = (sel: string) => (container.querySelector(sel) as HTMLElement | null)?.dataset.sigil;
    expect(sigil('.acctbtn .av')).toBe('olive');
    expect(sigil('[data-agent="builder"] .av')).toBe('anvil');
    expect(sigil('[data-agent="reviewer"] .av')).toBe('helmet');
    expect(container.querySelector('[data-agent="builder"] .was')?.textContent).toBe('was fixer');
    location.hash = '#/rooms/general';
    await waitFor(() => expect(container.querySelector('[data-mid="1"]')).not.toBeNull());
    expect(sigil('[data-mid="1"] .av')).toBe('anvil');
    expect((container.querySelector('[data-mid="1"]') as HTMLElement).style.getPropertyValue('--pg')).toBe(
      'var(--pg-terracotta)',
    );
    location.hash = '#/';
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
