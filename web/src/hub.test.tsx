import { act, cleanup, fireEvent, render, renderHook, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { App, moveThread, page, parseRoute } from './App';
import type { Api } from './api';
import { forgetLoadedBuild, roomCounts, useRoom, useThread } from './useHub';

afterEach(() => {
  cleanup();
  forgetLoadedBuild();
  vi.unstubAllGlobals();
  localStorage.clear();
  sessionStorage.clear();
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
  'WebService/Whoami': {
    name: 'operator',
    board: 'agora',
    generalRoom: 'general',
    version: 'v0.4.0',
    commit: '3bc332799d4c',
  },
  'AgentService/ListAgents': {
    agents: [
      {
        name: 'builder',
        session: 'SESSION_STATE_BUSY',
        project: 'example-app',
        projectRoom: 'example-app',
        task: 'fixing the login timeout',
        active: true,
        icon: 'anvil',
        pigment: 'terracotta',
        formerly: [{ name: 'fixer', renamedAt: '2026-10-09T11:00:00Z' }],
      },
      {
        name: 'reviewer',
        session: 'SESSION_STATE_IDLE',
        project: 'example-app',
        projectRoom: 'example-app',
        active: true,
      },
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

describe('an upgraded hub', () => {
  // a hub whose watch reports a new revision on demand, and whose commit can change in between
  function upgradingHub() {
    const hub = fakeHub([]);
    const state = { commit: '3bc332799d4c', push: (_: number) => {} };
    let revision = 1;
    const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input instanceof Request ? input.url : input);
      if (url.endsWith('WebService/Whoami'))
        return Response.json({ ...(answers['WebService/Whoami'] as object), commit: state.commit });
      if (url.endsWith('WebService/Watch')) {
        const stream = new ReadableStream<Uint8Array>({
          start(c) {
            c.enqueue(envelope(0, { revision: String(revision) }));
            state.push = (r) => c.enqueue(envelope(0, { revision: String(r) }));
          },
        });
        return new Response(stream, { status: 200, headers: { 'Content-Type': 'application/connect+json' } });
      }
      return hub(input, init);
    });
    const comeBack = async (commit: string) => {
      state.commit = commit;
      revision++;
      await act(async () => state.push(revision));
    };
    return { fetch, comeBack };
  }

  async function opened() {
    const reload = vi.spyOn(page, 'reload').mockImplementation(() => {});
    const h = upgradingHub();
    vi.stubGlobal('fetch', h.fetch);
    history.replaceState(null, '', '/#token=good-token');
    const r = render(<App />);
    await waitFor(() => expect(r.container.querySelector('[data-agent="builder"]')).not.toBeNull());
    return { ...h, reload };
  }

  it('loads the app again when the hub comes back on another commit', async () => {
    const { comeBack, reload } = await opened();
    await comeBack('a964c856ff7e');
    await waitFor(() => expect(reload).toHaveBeenCalledOnce());
  });

  it('does not load the app again when the hub restarts on the same commit', async () => {
    const { comeBack, reload } = await opened();
    await comeBack('3bc332799d4c');
    await act(async () => new Promise((r) => setTimeout(r, 50)));
    expect(reload).not.toHaveBeenCalled();
    expect(screen.queryByRole('button', { name: /Reload/ })).toBeNull();
  });

  it('waits for a draft left in another room', async () => {
    const { comeBack, reload } = await opened();
    location.hash = '#/rooms/general';
    fireEvent.change(await screen.findByLabelText('Message #general'), { target: { value: 'almost done' } });
    location.hash = '#/rooms/example-app';
    await screen.findByLabelText('Message #example-app');
    await comeBack('a964c856ff7e');
    await screen.findByRole('button', { name: 'Updated · Reload' });
    expect(reload).not.toHaveBeenCalled();
  });

  it('keeps the build of the page when the operator signs in again', async () => {
    const { comeBack, reload } = await opened();
    location.hash = '#/rooms/general';
    fireEvent.change(await screen.findByLabelText('Message #general'), { target: { value: 'almost done' } });
    await comeBack('a964c856ff7e');
    await screen.findByRole('button', { name: 'Updated · Reload' });
    // a new link in the same tab signs in again, with the upgraded hub already running
    location.hash = '#token=other-token';
    await waitFor(() => expect(localStorage.getItem('agora.token')).toBe('other-token'));
    location.hash = '#/rooms/general';
    const box = await screen.findByLabelText('Message #general');
    await screen.findByRole('button', { name: 'Updated · Reload' });
    expect(reload).not.toHaveBeenCalled();
    expect((box as HTMLTextAreaElement).value).toBe('almost done');
    fireEvent.change(box, { target: { value: '' } });
    await waitFor(() => expect(reload).toHaveBeenCalledOnce());
  });

  it('keeps a draft, reloads when the operator asks, and by itself once the draft is erased', async () => {
    const { comeBack, reload } = await opened();
    location.hash = '#/rooms/general';
    const box = await screen.findByLabelText('Message #general');
    fireEvent.change(box, { target: { value: 'almost done' } });
    await comeBack('a964c856ff7e');
    const button = await screen.findByRole('button', { name: 'Updated · Reload' });
    expect(reload).not.toHaveBeenCalled();
    expect((box as HTMLTextAreaElement).value).toBe('almost done');
    fireEvent.click(button);
    expect(reload).toHaveBeenCalledOnce();
    fireEvent.change(box, { target: { value: '' } });
    await waitFor(() => expect(reload).toHaveBeenCalledTimes(2));
  });
});

describe('drafts', () => {
  const box = () => screen.getByLabelText('Message #general') as HTMLTextAreaElement;
  async function inGeneral() {
    const r = await signedIn();
    location.hash = '#/rooms/general';
    await screen.findByLabelText('Message #general');
    return r;
  }

  it('come back after the page is reloaded', async () => {
    await inGeneral();
    fireEvent.change(box(), { target: { value: 'almost done' } });
    cleanup();
    await inGeneral();
    expect(box().value).toBe('almost done');
  });

  it('are gone once erased', async () => {
    await inGeneral();
    fireEvent.change(box(), { target: { value: 'almost done' } });
    fireEvent.change(box(), { target: { value: '' } });
    cleanup();
    await inGeneral();
    expect(box().value).toBe('');
  });

  it('are dropped by signing out', async () => {
    await inGeneral();
    fireEvent.change(box(), { target: { value: 'almost done' } });
    openMenu();
    fireEvent.click(screen.getByRole('button', { name: 'Sign out' }));
    cleanup();
    await inGeneral();
    expect(box().value).toBe('');
  });

  it('are kept when the hub refuses the token', async () => {
    await inGeneral();
    fireEvent.change(box(), { target: { value: 'almost done' } });
    cleanup();
    const refused = async () =>
      new Response(JSON.stringify({ code: 'unauthenticated', message: 'a valid web token is required' }), {
        status: 401,
        headers: { 'Content-Type': 'application/json' },
      });
    vi.stubGlobal('fetch', vi.fn(refused));
    render(<App />);
    fireEvent.change(await screen.findByLabelText('Web token'), { target: { value: 'new-token' } });
    expect(screen.getByText(/did not accept that token/)).toBeTruthy();
    vi.stubGlobal('fetch', fakeHub([]));
    fireEvent.click(screen.getByRole('button', { name: 'Enter' }));
    await screen.findByLabelText('Message #general');
    expect(box().value).toBe('almost done');
  });

  it('last while the page is open where the browser keeps nothing', async () => {
    const off = () => {
      throw new DOMException('storage is off', 'SecurityError');
    };
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(off);
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(off);
    vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(off);
    try {
      await inGeneral();
      fireEvent.change(box(), { target: { value: 'almost done' } });
      location.hash = '#/rooms/example-app';
      await screen.findByLabelText('Message #example-app');
      location.hash = '#/rooms/general';
      await screen.findByLabelText('Message #general');
      expect(box().value).toBe('almost done');
    } finally {
      vi.restoreAllMocks();
    }
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

describe('following under notifications set to Nothing', () => {
  it('does not ask for permission when a room is set to notify', async () => {
    localStorage.setItem('agora.notify', 'none');
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
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(JSON.parse(bodies[0] ?? '{}')).toEqual({ rooms: ['general'], follow: true, mode: 'SUBSCRIPTION_MODE_WAKE' });
    expect(Notification.requestPermission).not.toHaveBeenCalled();
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
              { name: 'example-chat', messages: 2, scheduled: 1 },
              { name: 'general', messages: 2 },
            ],
          });
        case 'RoomService/History':
          return reply({
            messages: [
              {
                id: '4',
                room: 'example-chat',
                author: 'operator',
                body: 'on it',
                deliveryState: 'DELIVERY_STATE_SENT',
              },
              { id: '5', room: 'example-chat', author: 'operator', body: 'morning all', incoming: true },
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
                deliveryState: 'DELIVERY_STATE_SCHEDULED',
              },
            ],
          });
        case 'RoomService/Post':
          calls.push([proc, JSON.parse(raw)]);
          return reply({ id: '8' });
        case 'BridgeService/SendScheduled':
          calls.push([proc, JSON.parse(raw)]);
          // the fake has the message scheduled only under delay; another policy stands for a
          // decision taken meanwhile
          return policy === 'BRIDGE_POLICY_DELAY'
            ? reply({})
            : reply({ code: 'not_found', message: 'message 7 is not scheduled' }, 404);
        case 'BridgeService/SetBridgePolicy': {
          const body = JSON.parse(raw);
          calls.push([proc, body]);
          // the fake stands for a bridge removed meanwhile when asked for read only
          return body.policy === 'BRIDGE_POLICY_READ'
            ? reply({ code: 'not_found', message: 'no bridge example-chat' }, 404)
            : reply({});
        }
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

  it('marks what the operator wrote in the chat', async () => {
    const { container } = await openChat('BRIDGE_POLICY_DELAY', []);
    const mark = container.querySelector('[data-mid="5"] .mh .wi');
    expect(mark?.textContent).toBe('written in example-chat');
    expect(container.querySelector('[data-mid="5"] .mh .au')?.textContent).toContain('operator');
    expect(container.querySelector('[data-mid="4"] .mh .au')?.textContent).toContain('operator');
    expect(container.querySelector('[data-mid="4"] .wi')).toBeNull();
  });

  it('shows the waiting message in the room list and sends it', async () => {
    const calls: [string, unknown][] = [];
    const { container } = await openChat('BRIDGE_POLICY_DELAY', calls);
    const link = container.querySelector('.side a[href="#/rooms/example-chat"]') as HTMLElement;
    expect(link.querySelector('svg.br title')?.textContent).toBe('bridge running');
    expect(link.querySelector('em.scheduled')?.textContent).toBe('1 scheduled to go out');
    expect(container.querySelector('[data-mid="6"] .mh')?.textContent).toContain('Ada · example-chat');
    fireEvent.click(within(waiting(container)).getByRole('button', { name: 'Send now' }));
    await waitFor(() => expect(calls).toEqual([['BridgeService/SendScheduled', { messageId: '7' }]]));
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('keeps Send now from a read-only room and says why', async () => {
    const { container } = await openChat('BRIDGE_POLICY_READ', []);
    const t = waiting(container);
    expect((within(t).getByRole('button', { name: 'Send now' }) as HTMLButtonElement).disabled).toBe(true);
    expect(t.querySelector('.decide .hint')?.textContent).toBe('read only — switch Outbound to send');
    expect((within(t).getByRole('button', { name: 'Cancel' }) as HTMLButtonElement).disabled).toBe(false);
  });

  it('says why the hub refused to send a message', async () => {
    const calls: [string, unknown][] = [];
    const { container } = await openChat('BRIDGE_POLICY_OPEN', calls);
    fireEvent.click(within(waiting(container)).getByRole('button', { name: 'Send now' }));
    const notice = await screen.findByRole('alert');
    expect(notice.textContent).toBe('Not sent: message 7 is not scheduled');
    await waitFor(() =>
      expect((within(waiting(container)).getByRole('button', { name: 'Send now' }) as HTMLButtonElement).disabled).toBe(
        false,
      ),
    ); // it still waits
    fireEvent.click(within(notice).getByRole('button', { name: 'Dismiss' }));
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('changes the outbound policy', async () => {
    const calls: [string, unknown][] = [];
    await openChat('BRIDGE_POLICY_DELAY', calls);
    fireEvent.click(screen.getByRole('button', { name: 'Outbound: Send after a delay' }));
    fireEvent.click(screen.getByRole('menuitemradio', { name: 'Send at once' }));
    await waitFor(() =>
      expect(calls).toEqual([
        ['BridgeService/SetBridgePolicy', { name: 'example-chat', policy: 'BRIDGE_POLICY_OPEN' }],
      ]),
    );
  });

  it('says why the policy did not change', async () => {
    await openChat('BRIDGE_POLICY_DELAY', []);
    fireEvent.click(screen.getByRole('button', { name: 'Outbound: Send after a delay' }));
    fireEvent.click(screen.getByRole('menuitemradio', { name: 'Read only' }));
    const notice = await screen.findByRole('alert');
    expect(notice.textContent).toBe('The policy did not change: no bridge example-chat');
    // a band in the room under its header: the Outbound control stays free to try again
    expect(notice.previousElementSibling?.classList).toContain('roomhead');
    expect(notice.closest('.room')).not.toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Outbound: Send after a delay' }));
    expect(screen.getByRole('menu', { name: 'Outbound' })).toBeTruthy();
  });

  const chosen = (name: string) => (screen.getByRole('radio', { name }) as HTMLInputElement).checked;
  const chatBox = () => screen.getByLabelText('Message #example-chat') as HTMLTextAreaElement;
  async function visitGeneral(c: HTMLElement) {
    location.hash = '#/rooms/general';
    await screen.findByLabelText('Message #general');
    location.hash = '#/rooms/example-chat';
    await waitFor(() => expect(c.querySelector('[data-mid="7"]')).not.toBeNull());
  }

  it('brings a note draft back as a note after another room', async () => {
    const calls: [string, unknown][] = [];
    const { container } = await openChat('BRIDGE_POLICY_DELAY', calls);
    fireEvent.click(screen.getByRole('radio', { name: 'Note for agents' }));
    fireEvent.change(chatBox(), { target: { value: '@secretary keep it short' } });
    await visitGeneral(container);
    expect(chatBox().value).toBe('@secretary keep it short');
    expect(chosen('Note for agents')).toBe(true);
    fireEvent.keyDown(chatBox(), { key: 'Enter' });
    await waitFor(() =>
      expect(calls).toEqual([
        ['RoomService/Post', { room: 'example-chat', body: '@secretary keep it short', note: true }],
      ]),
    );
  });

  it('brings a note draft back as a note after a reload', async () => {
    await openChat('BRIDGE_POLICY_DELAY', []);
    fireEvent.click(screen.getByRole('radio', { name: 'Note for agents' }));
    fireEvent.change(chatBox(), { target: { value: '@secretary keep it short' } });
    cleanup();
    await openChat('BRIDGE_POLICY_DELAY', []);
    expect(chatBox().value).toBe('@secretary keep it short');
    expect(chosen('Note for agents')).toBe(true);
  });

  it('opens on the chat again when reloaded with nothing written', async () => {
    await openChat('BRIDGE_POLICY_DELAY', []);
    fireEvent.click(screen.getByRole('radio', { name: 'Note for agents' }));
    cleanup();
    await openChat('BRIDGE_POLICY_DELAY', []);
    expect(chosen('To example-chat')).toBe(true);
  });

  it('opens on the chat again when left with nothing written', async () => {
    const { container } = await openChat('BRIDGE_POLICY_DELAY', []);
    fireEvent.click(screen.getByRole('radio', { name: 'Note for agents' }));
    await visitGeneral(container);
    expect(chosen('To example-chat')).toBe(true);
  });

  it('addresses an agent in a bridged room with a note', async () => {
    const hub = bridgedHub('BRIDGE_POLICY_DELAY', []);
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) =>
        String(input).endsWith('AgentService/ListAgents')
          ? new Response(
              JSON.stringify({
                agents: [{ name: 'builder', project: 'example-chat', projectRoom: 'example-chat', active: true }],
              }),
              {
                headers: { 'Content-Type': 'application/json' },
              },
            )
          : hub(input, init),
      ),
    );
    history.replaceState(null, '', '/#token=good-token');
    const { container } = render(<App />);
    await waitFor(() => expect(container.querySelector('[data-agent="builder"]')).not.toBeNull());
    fireEvent.click(container.querySelector('[data-agent="builder"]') as HTMLElement);
    fireEvent.click(screen.getByText(/Address in #example-chat/));
    await waitFor(() => expect(chatBox().value).toBe('@builder '));
    expect(chosen('Note for agents')).toBe(true);
  });

  it('sends a note out', async () => {
    const calls: [string, unknown][] = [];
    const hub = bridgedHub('BRIDGE_POLICY_DELAY', calls);
    const reply = (json: unknown) =>
      new Response(JSON.stringify(json), { status: 200, headers: { 'Content-Type': 'application/json' } });
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const proc = String(input).replace(/^.*\/agora\.v1\./, '');
        if (proc === 'RoomService/History')
          return reply({
            messages: [{ id: '9', room: 'example-chat', author: 'builder', body: 'Draft: all fine', note: true }],
          });
        if (proc === 'BridgeService/SendNoteOut') {
          calls.push([proc, JSON.parse(new TextDecoder().decode(init?.body as Uint8Array))]);
          return reply({ id: '10' });
        }
        return hub(input, init);
      }),
    );
    history.replaceState(null, '', '/#token=good-token');
    const { container } = render(<App />);
    await waitFor(() => expect(container.querySelector('[data-agent="builder"]')).not.toBeNull());
    location.hash = '#/rooms/example-chat';
    fireEvent.click(await screen.findByRole('button', { name: 'Send out' }));
    await waitFor(() => expect(calls).toEqual([['BridgeService/SendNoteOut', { messageId: '9' }]]));
  });

  it('counts a waiting message once on the phone’s Rooms tab', async () => {
    await openChat('BRIDGE_POLICY_DELAY', []);
    // general has 2 unread; #example-chat 1 waiting, which is not unread here
    expect(document.querySelector('.tabs em')?.textContent).toBe('3');
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

describe('avatars', () => {
  const HASH = 'b'.repeat(64);
  const PNG = new Uint8Array([137, 80, 78, 71]);

  /** The fake hub where builder and the operator have avatars, builder wrote ten messages in general and Ada one from outside; `avatar` answers /avatars/. */
  function avatarHub(avatar: (url: string) => Response, calls: [string, unknown][] = []) {
    const hub = fakeHub([]);
    const reply = (json: unknown, status = 200) =>
      new Response(JSON.stringify(json), { status, headers: { 'Content-Type': 'application/json' } });
    return vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/avatars/')) return avatar(url);
      const proc = url.replace(/^.*\/agora\.v1\./, '');
      const raw = typeof init?.body === 'string' ? init.body : new TextDecoder().decode(init?.body as Uint8Array);
      switch (proc) {
        case 'AgentService/ListAgents':
          return reply({
            agents: [
              { name: 'builder', session: 'SESSION_STATE_BUSY', active: true, icon: 'anvil', avatar: HASH },
              { name: 'operator', session: 'SESSION_STATE_OFFLINE', active: true, icon: 'olive' },
            ],
          });
        case 'RoomService/History':
          return reply({
            messages: [
              ...Array.from({ length: 10 }, (_, i) => ({
                id: String(i + 1),
                room: 'general',
                author: 'builder',
                body: `note ${i}`,
              })),
              {
                id: '11',
                room: 'general',
                body: 'hi',
                externalAuthor: { bridge: 'example-chat', id: '42', name: 'Ada', avatar: HASH },
              },
            ],
          });
        case 'AgentService/SetAvatar':
          calls.push([proc, JSON.parse(raw)]);
          return raw.includes(btoa('<svg><script'))
            ? reply(
                { code: 'invalid_argument', message: 'the SVG is not accepted: the element script is not allowed' },
                400,
              )
            : reply({});
        case 'AgentService/ClearAvatar':
          calls.push([proc, JSON.parse(raw)]);
          return reply({});
      }
      return hub(input, init);
    });
  }

  it('draws an agent and a person outside with their avatars and fetches each once', async () => {
    const fetched: string[] = [];
    vi.stubGlobal(
      'fetch',
      avatarHub((url) => {
        fetched.push(url);
        return new Response(PNG, { headers: { 'Content-Type': 'image/png' } });
      }),
    );
    history.replaceState(null, '', '/#token=good-token');
    const { container } = render(<App />);
    await waitFor(() => expect(container.querySelector('[data-agent="builder"] .av img.pic')).not.toBeNull());
    location.hash = '#/rooms/general';
    await waitFor(() => expect(container.querySelectorAll('[data-mid] .av img.pic').length).toBeGreaterThan(1));
    expect(container.querySelector('[data-mid="11"] .av.ext img.pic')?.getAttribute('src')).toBe(
      'data:image/png;base64,iVBORw==',
    );
    expect(container.querySelector('.acctbtn .av img')).toBeNull();
    expect(fetched).toEqual([`${location.origin}/avatars/${HASH}?size=128`]);
    location.hash = '#/';
  });

  it('draws the sigil when the avatar cannot be had', async () => {
    vi.stubGlobal(
      'fetch',
      avatarHub(() => new Response('', { status: 404 })),
    );
    history.replaceState(null, '', '/#token=good-token');
    const { container } = render(<App />);
    await waitFor(() => expect(container.querySelector('[data-agent="builder"] .av')).not.toBeNull());
    await new Promise((r) => setTimeout(r, 20));
    const av = container.querySelector('[data-agent="builder"] .av') as HTMLElement;
    expect(av.querySelector('img')).toBeNull();
    expect(av.dataset.sigil).toBe('anvil');
  });

  it('lets the operator pick its avatar in the account menu and says why one is refused', async () => {
    const calls: [string, unknown][] = [];
    vi.stubGlobal(
      'fetch',
      avatarHub(() => new Response('', { status: 404 }), calls),
    );
    history.replaceState(null, '', '/#token=good-token');
    const { container } = render(<App />);
    await waitFor(() => expect(container.querySelector('[data-agent="builder"]')).not.toBeNull());
    openMenu();
    expect(screen.queryByRole('button', { name: 'Remove avatar' })).toBeNull();
    const input = screen.getByLabelText('Set avatar…') as HTMLInputElement;
    expect(input.accept).toBe('image/png,image/jpeg,image/webp,image/svg+xml');
    fireEvent.change(input, {
      target: { files: [new File(['<svg><script/></svg>'], 'face.svg', { type: 'image/svg+xml' })] },
    });
    expect(await screen.findByRole('alert')).toHaveProperty(
      'textContent',
      'the SVG is not accepted: the element script is not allowed',
    );
    expect(calls).toEqual([['AgentService/SetAvatar', { agent: 'operator', image: btoa('<svg><script/></svg>') }]]);
    fireEvent.change(input, { target: { files: [new File([new Uint8Array(300 * 1024)], 'big.png')] } });
    await waitFor(() =>
      expect(screen.getByRole('alert').textContent).toBe('An avatar is at most 256 KiB; this file is 300 KiB'),
    );
    expect(calls).toHaveLength(1);
    fireEvent.change(input, { target: { files: [new File(['<svg/>'], 'face.svg')] } });
    await waitFor(() => expect(document.getElementById('account-menu')).toBeNull());
    expect(calls[1]).toEqual(['AgentService/SetAvatar', { agent: 'operator', image: btoa('<svg/>') }]);
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
    expect(document.querySelector('.settings .ver')?.textContent).toBe('Hub v0.4.0 · 3bc332799d4c');
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

const thread = (root: bigint, room = 'example-app') => ({
  messages: [
    { id: root, room, author: 'builder', body: 'root' },
    { id: root + 10n, room, author: 'reviewer', body: 'reply', thread: root },
  ],
});

describe('useThread', () => {
  it('marks each thread read up to its own newest reply', async () => {
    const markRoomRead = vi.fn(async () => ({}));
    const history = vi.fn(async ({ thread: root }: { thread: bigint }) => thread(root));
    const api = { rooms: { history, markRoomRead } } as unknown as Api;
    const { result, rerender } = renderHook(({ root }) => useThread(api, 'example-app', root, 1n, vi.fn()), {
      initialProps: { root: 490n },
    });
    await waitFor(() => expect(result.current.messages).toHaveLength(2));
    act(() => result.current.markSeen(500n));
    await waitFor(() => expect(markRoomRead).toHaveBeenCalledWith({ room: 'example-app', throughId: 500n }));
    // another thread whose newest reply is older is still marked
    rerender({ root: 440n });
    await waitFor(() => expect(result.current.messages?.[0]?.id).toBe(440n));
    act(() => result.current.markSeen(450n));
    await waitFor(() => expect(markRoomRead).toHaveBeenCalledWith({ room: 'example-app', throughId: 450n }));
  });

  it('marks a reply seen while the page was hidden once the page is shown', async () => {
    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
    const markRoomRead = vi.fn(async () => ({}));
    const api = { rooms: { history: vi.fn(async () => thread(40n)), markRoomRead } } as unknown as Api;
    const { result } = renderHook(() => useThread(api, 'example-app', 40n, 1n, vi.fn()));
    await waitFor(() => expect(result.current.messages).toHaveLength(2));
    act(() => result.current.markSeen(50n));
    expect(markRoomRead).not.toHaveBeenCalled();
    vi.restoreAllMocks();
    document.dispatchEvent(new Event('visibilitychange'));
    await waitFor(() => expect(markRoomRead).toHaveBeenCalledWith({ room: 'example-app', throughId: 50n }));
  });

  it('refuses a thread of another room instead of showing it under this one', async () => {
    const markRoomRead = vi.fn(async () => ({}));
    const api = { rooms: { history: vi.fn(async () => thread(41n, 'dev')), markRoomRead } } as unknown as Api;
    const { result } = renderHook(() => useThread(api, 'general', 41n, 1n, vi.fn()));
    await waitFor(() => expect(result.current.error).toBe('message 41 is not in #general'));
    expect(result.current.messages).toBeUndefined();
  });
});

describe('archived rooms', () => {
  function archivedHub(calls: [string, unknown][]) {
    const hub = fakeHub([]);
    const reply = (json: unknown, status = 200) =>
      new Response(JSON.stringify(json), { status, headers: { 'Content-Type': 'application/json' } });
    return vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const proc = String(input).replace(/^.*\/agora\.v1\./, '');
      const raw = typeof init?.body === 'string' ? init.body : new TextDecoder().decode(init?.body as Uint8Array);
      switch (proc) {
        case 'RoomService/ListRooms':
          calls.push([proc, JSON.parse(raw)]);
          return reply({
            rooms: [
              { name: 'example-app', messages: 1 },
              { name: 'general', messages: 2 },
              {
                name: 'old-app',
                messages: 12,
                scheduled: 1,
                archivedBy: 'builder',
                archivedAt: '2026-10-09T10:00:00Z',
              },
            ],
          });
        case 'RoomService/ArchiveRoom':
          calls.push([proc, JSON.parse(raw)]);
          return reply({ code: 'failed_precondition', message: '#old-app is not archived' }, 400);
      }
      return hub(input, init);
    });
  }

  async function open(calls: [string, unknown][]) {
    vi.stubGlobal('fetch', archivedHub(calls));
    history.replaceState(null, '', '/#token=good-token');
    const r = render(<App />);
    await waitFor(() => expect(r.container.querySelector('[data-agent="builder"]')).not.toBeNull());
    return r;
  }

  it('leaves archived rooms out of the sidebar and opens them on the Rooms view', async () => {
    const calls: [string, unknown][] = [];
    const { container } = await open(calls);
    expect(calls[0]).toEqual(['RoomService/ListRooms', { all: true }]);
    expect(container.querySelector('.side a[href="#/rooms/old-app"]')).toBeNull();
    // what waits in an archived room is no count of the Rooms tab
    expect(container.querySelector('.tabs em')?.textContent).toBe('2'); // the 2 unread in general
    const row = container.querySelector('.side a.archivedrow') as HTMLAnchorElement;
    expect(row.textContent).toBe('1 archived→');
    fireEvent.click(row);
    location.hash = row.getAttribute('href') ?? '';
    const tab = await screen.findByRole('link', { name: 'Archived · 1' });
    expect(tab.getAttribute('aria-current')).toBe('page');
    fireEvent.click(screen.getByRole('button', { name: 'Unarchive #old-app' }));
    const notice = await screen.findByRole('alert');
    expect(notice.textContent).toBe('The room was not unarchived: #old-app is not archived');
    expect(calls).toContainEqual(['RoomService/ArchiveRoom', { room: 'old-app' }]);
    // the notice stays with the view where it was asked
    location.hash = '#/rooms/general';
    await waitFor(() => expect(container.querySelector('.room')).not.toBeNull());
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('tells a refusal that comes after the operator left only where it was asked', async () => {
    let answer: (r: Response) => void = () => {};
    const hub = archivedHub([]);
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) =>
        String(input).endsWith('RoomService/ArchiveRoom')
          ? new Promise<Response>((r) => {
              answer = r;
            })
          : hub(input, init),
      ),
    );
    history.replaceState(null, '', '/#token=good-token');
    const { container } = render(<App />);
    await waitFor(() => expect(container.querySelector('[data-agent="builder"]')).not.toBeNull());
    location.hash = '#/archive';
    fireEvent.click(await screen.findByRole('button', { name: 'Unarchive #old-app' }));
    location.hash = '#/rooms/general';
    await waitFor(() => expect(container.querySelector('.room')).not.toBeNull());
    answer(
      new Response(JSON.stringify({ code: 'failed_precondition', message: '#old-app is not archived' }), {
        status: 400,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('finds an archived room in the palette, marked archived', async () => {
    await open([]);
    fireEvent.keyDown(document, { key: 'k', ctrlKey: true });
    fireEvent.change(screen.getByRole('combobox', { name: 'Jump to' }), { target: { value: 'old' } });
    const found = screen.getByRole('option', { name: /#old-app/ });
    expect(found.textContent).toContain('archived room');
  });
});

describe('App', () => {
  it('routes by the fragment', () => {
    expect(parseRoute('#/')).toEqual({ view: 'board' });
    expect(parseRoute('#/rooms')).toEqual({ view: 'rooms' });
    expect(parseRoute('#/archive')).toEqual({ view: 'rooms', archived: true });
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

describe('threads in the app', () => {
  it('moves in the history so that Back from a thread opened in its room returns to the room once', () => {
    const t = '#/rooms/example-app/thread/40';
    // opened from the room: a new entry
    expect(moveThread('#/rooms/example-app', undefined, 'example-app', undefined, 40n)).toEqual({ push: t, pushed: t });
    // closed: back to the room's entry instead of a new one
    expect(moveThread(t, t, 'example-app', 40n, undefined)).toEqual({ back: true });
    // reached by its address: closing replaces it with the room
    expect(moveThread(t, undefined, 'example-app', 40n, undefined)).toEqual({ replace: '#/rooms/example-app' });
    // another thread from an open one: in place, still over the room
    expect(moveThread(t, t, 'example-app', 40n, 42n)).toEqual({
      replace: '#/rooms/example-app/thread/42',
      pushed: '#/rooms/example-app/thread/42',
    });
  });

  it('posts a reply in the thread, also to the room, and leaves the thread back to the room', async () => {
    const calls: [string, unknown][] = [];
    const hub = fakeHub([]);
    const reply = (json: unknown) =>
      new Response(JSON.stringify(json), { status: 200, headers: { 'Content-Type': 'application/json' } });
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const proc = String(input).replace(/^.*\/agora\.v1\./, '');
        const raw = typeof init?.body === 'string' ? init.body : new TextDecoder().decode(init?.body as Uint8Array);
        switch (proc) {
          case 'RoomService/History':
            return reply({
              messages: [
                { id: '1', room: 'general', author: 'builder', body: 'hello' },
                { id: '3', room: 'general', author: 'reviewer', body: 'hi', thread: '1' },
              ],
            });
          case 'RoomService/Post':
          case 'RoomService/LeaveThread':
            calls.push([proc, JSON.parse(raw)]);
            return reply({ id: '4' });
          default:
            return hub(input, init);
        }
      }),
    );
    history.replaceState(null, '', '/#token=good-token');
    render(<App />);
    location.hash = '#/rooms/general/thread/1';
    const box = (await screen.findByRole('combobox', { name: 'Reply in the thread #general' })) as HTMLTextAreaElement;
    fireEvent.click(screen.getByLabelText('Also to the room'));
    fireEvent.change(box, { target: { value: 'the CSV one' } });
    fireEvent.keyDown(box, { key: 'Enter' });
    await waitFor(() =>
      expect(calls).toContainEqual([
        'RoomService/Post',
        { room: 'general', body: 'the CSV one', thread: '1', also: true },
      ]),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Leave' }));
    await waitFor(() => expect(calls).toContainEqual(['RoomService/LeaveThread', { messageId: '1' }]));
    await waitFor(() => expect(location.hash).toBe('#/rooms/general'));
    // an address naming a reply opens its thread under the root's address
    location.hash = '#/rooms/general/thread/3';
    await waitFor(() => expect(location.hash).toBe('#/rooms/general/thread/1'));
  });
});

describe('roomCounts', () => {
  it('keeps the unread replies of threads apart from the room’s unread', () => {
    const counts = roomCounts([{ room: 'example-app', unread: 1, addressed: 0, threads: 2 }], []);
    expect(counts.get('example-app')).toEqual({ unread: 1, addressed: 0, threads: 2 });
  });
});
