import { create } from '@bufbuild/protobuf';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import type { ComponentProps, ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { agent, bridge, goingOut, message, NOW, outside } from '../fixtures';
import type { Message } from '../gen/agora/v1/rooms_pb';
import { RoomSchema } from '../gen/agora/v1/rooms_pb';
import type { Saved } from '../tape';
import { OpenAgent } from './common';
import { RoomView } from './room';
import { RoomNav, Rooms } from './rooms';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const agents = [
  agent('builder', 'busy'),
  agent('reviewer', 'idle'),
  agent('release', 'busy'),
  agent('operator', 'offline'),
];

type Props = ComponentProps<typeof RoomView>;

function room(
  messages: Message[] | undefined,
  more: Partial<Props> = {},
  wrapper?: (p: { children: ReactNode }) => ReactNode,
) {
  const props: Props = {
    name: 'example-app',
    room: create(RoomSchema, { name: 'example-app', purpose: 'Checkout and payments', messages: 12 }),
    messages,
    reader: { operator: 'operator', board: 'agora' },
    general: 'general',
    agents,
    followed: true,
    now: NOW,
    draft: '',
    onDraft: vi.fn(),
    onPost: vi.fn(async () => {}),
    onFollow: vi.fn(),
    onSeen: vi.fn(),
    onDecide: vi.fn(async () => {}),
    onPolicy: vi.fn(),
    memory: new Map<string, Saved>(),
    onArrived: vi.fn(),
    ...more,
  };
  return { props, ...render(<RoomView {...props} />, { wrapper }) };
}

const tablet = (c: HTMLElement, id: number) => c.querySelector(`[data-mid="${id}"]`) as HTMLElement;
const header = (c: HTMLElement, id: number) =>
  tablet(c, id)
    .querySelector('.mh')
    ?.textContent?.replace(/\d\d:\d\d/, '');

/** Lays the tape out in jsdom: rows of 100 pixels in a view of 250, with a working scrollTop. */
function layout() {
  const tops = new WeakMap<Element, number>();
  vi.spyOn(HTMLElement.prototype, 'offsetTop', 'get').mockImplementation(function (this: HTMLElement) {
    return [...(this.parentElement?.children ?? [])].indexOf(this) * 100;
  });
  vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockReturnValue(100);
  vi.spyOn(Element.prototype, 'clientHeight', 'get').mockReturnValue(250);
  vi.spyOn(Element.prototype, 'scrollHeight', 'get').mockImplementation(function (this: Element) {
    return this.children.length * 100;
  });
  vi.spyOn(Element.prototype, 'scrollTop', 'get').mockImplementation(function (this: Element) {
    return tops.get(this) ?? 0;
  });
  vi.spyOn(Element.prototype, 'scrollTop', 'set').mockImplementation(function (this: Element, v: number) {
    tops.set(this, v);
  });
}

describe('messages in a room', () => {
  const ms = [
    message(1, 'builder', 'PR #57 is up', 30),
    message(2, 'builder', 'and the tests pass', 28),
    message(3, 'agora', 'CI is green on #57', 27),
    message(4, 'reviewer', '@release approved #57', 20, { replyTo: 1n }),
    message(5, 'release', '@operator ready to tag', 10),
    message(6, 'operator', 'wait for me', 9),
  ];

  it('heads each tablet with author → addressees and quotes what it replies to', () => {
    const { container } = room(ms);
    expect(header(container, 4)).toBe('reviewer→ builder, releasereply');
    expect(tablet(container, 4).querySelector('.quote')?.textContent).toBe('builderPR #57 is up');
  });

  it('tints and tags messages to the operator and sets the operator’s own apart', () => {
    const { container } = room(ms);
    expect(tablet(container, 5).classList).toContain('tome');
    expect(tablet(container, 5).querySelector('.tag')?.textContent).toBe('TO YOU');
    expect([...container.querySelectorAll('.msg.own .tx')].map((t) => t.textContent)).toEqual(['wait for me']);
  });

  it('groups one author’s messages and shows board messages as a line with an owl', () => {
    const { container } = room(ms);
    expect(tablet(container, 2).classList).toContain('cont');
    const line = tablet(container, 3);
    expect(line.classList).toContain('sys');
    expect(line.querySelector('svg title')?.textContent).toBe('the board');
    expect(line.querySelector('.mh')).toBeNull();
    expect(container.querySelector('.day')?.textContent).toBe('TODAY');
  });

  it('jumps to a quoted message and offers the way back', () => {
    const { container } = room(ms);
    fireEvent.click(tablet(container, 4).querySelector('.quote') as HTMLElement);
    expect(tablet(container, 1).classList).toContain('flash');
    fireEvent.click(screen.getByText('Back to where you were'));
    expect(screen.queryByText('Back to where you were')).toBeNull();
  });

  it('focuses a conversation and dims the rest', () => {
    const talk = [
      message(1, 'builder', '@reviewer PR is up', 9),
      message(2, 'reviewer', '@builder on it', 8),
      message(3, 'release', 'tagging later', 7),
      message(4, 'builder', '@reviewer fixed', 6),
    ];
    const { container } = room(talk);
    fireEvent.click(screen.getByRole('button', { name: /builder ⇄ reviewer/ }));
    expect(container.querySelector('.focusbar')?.textContent).toContain('builder ⇄ reviewer');
    expect(container.querySelector('.focusbar')?.textContent).toContain('3 messages between them');
    expect([...container.querySelectorAll('.msg.dim')].map((m) => m.getAttribute('data-mid'))).toEqual(['3']);
    fireEvent.click(screen.getByText('Show everyone'));
    expect(container.querySelector('.msg.dim')).toBeNull();
  });

  it('shows a mention of a former name as the agent and addresses it', () => {
    const renamed = [...agents, agent('docs-writer', 'idle', { formerly: [{ name: 'fixer' }], pigment: 'lapis' })];
    const talk = [message(1, 'builder', '@fixer please check #57', 9), message(2, 'builder', '@docs-writer fixed', 8)];
    const { container } = room(talk, { agents: renamed });
    const mention = tablet(container, 1).querySelector('.tx .mn') as HTMLElement;
    expect(mention.textContent).toBe('@docs-writer');
    expect(mention.classList).toContain('former');
    expect(mention.title).toBe('written as @fixer, now @docs-writer');
    expect(header(container, 1)).toBe('builder→ docs-writerreply');
    expect(screen.getByRole('button', { name: /builder ⇄ docs-writer/ }).textContent).toContain('2');
  });

  it('follows and stops following, except the general room', () => {
    const { props } = room(ms, { followed: false });
    expect(screen.queryByRole('button', { name: /^Follow mode/ })).toBeNull();
    fireEvent.click(screen.getByText('follow'));
    expect(props.onFollow).toHaveBeenCalledWith(true);
    cleanup();
    const following = room(ms, { mode: 'all' });
    fireEvent.click(screen.getByText('following'));
    expect(following.props.onFollow).toHaveBeenCalledWith(false);
    cleanup();
    room([], { name: 'general', mode: 'mentions' });
    expect(screen.queryByText(/^follow/)).toBeNull();
    expect(screen.getByRole('button', { name: 'Follow mode: Mentions only' })).toBeTruthy();
  });

  it('changes the mode only when one is picked', () => {
    const { props } = room(ms, { mode: 'all' });
    const button = screen.getByRole('button', { name: 'Follow mode: Every message' });
    fireEvent.click(button);
    const menu = screen.getByRole('menu', { name: 'Follow mode' });
    const items = within(menu).getAllByRole('menuitemradio');
    expect(items.map((i) => [i.textContent, i.getAttribute('aria-checked')])).toEqual([
      ['●Every message', 'true'],
      ['Mentions only', 'false'],
      ['Every message notifies', 'false'],
    ]);
    expect(document.activeElement).toBe(items[0]);
    fireEvent.keyDown(menu, { key: 'ArrowDown' });
    fireEvent.keyDown(menu, { key: 'ArrowDown' });
    expect(document.activeElement).toBe(items[2]);
    fireEvent.keyDown(menu, { key: 'Escape' });
    expect(screen.queryByRole('menu')).toBeNull();
    expect(document.activeElement).toBe(button);
    expect(props.onFollow).not.toHaveBeenCalled();
    fireEvent.click(button);
    fireEvent.click(screen.getByRole('menuitemradio', { name: 'Every message notifies' }));
    expect(props.onFollow).toHaveBeenCalledWith(true, 'wake');
  });

  it('counts back only mentions in a room followed for mentions', () => {
    const msgs = [message(1, 'builder', 'chatter', 3), message(2, 'reviewer', '@operator look', 2)];
    const { container } = room(msgs, { mode: 'mentions', unread: { unread: 1, addressed: 1 } });
    expect(container.querySelector('.newline')?.nextElementSibling?.getAttribute('data-mid')).toBe('2');
  });
});

describe('reading position', () => {
  const ms = [1, 2, 3, 4, 5, 6].map((i) => message(i, i % 2 ? 'builder' : 'reviewer', `message ${i}`, 60 - i * 7));

  it('reports unread messages in view as seen', () => {
    const { props } = room(ms, { unread: { unread: 2, addressed: 0 } }); // jsdom lays nothing out: all is in view
    expect(props.onSeen).toHaveBeenCalledWith(6n);
  });

  it('lands on the NEW line above the first unread message', () => {
    layout();
    const { container } = room(ms, { unread: { unread: 2, addressed: 0 } });
    const line = container.querySelector('.newline') as HTMLElement;
    expect(line.nextElementSibling?.getAttribute('data-mid')).toBe('5');
    expect((container.querySelector('.msgs') as HTMLElement).scrollTop).toBe(line.offsetTop - 40);
  });

  it('returns to where the room was left', () => {
    layout();
    const memory = new Map<string, Saved>([['example-app', { top: 120, bottom: false }]]);
    const { container } = room(ms, { memory });
    expect((container.querySelector('.msgs') as HTMLElement).scrollTop).toBe(120);
  });

  it('keeps its place and counts new messages while scrolled up', async () => {
    layout();
    const { container, props, rerender } = room(ms);
    const tape = container.querySelector('.msgs') as HTMLElement;
    expect(tape.scrollTop).toBe(700); // the bottom
    tape.scrollTop = 0;
    fireEvent.scroll(tape);
    const more = [...ms, message(7, 'builder', 'one more', 2), message(8, 'release', '@operator tag it', 1)];
    rerender(<RoomView {...props} messages={more} />);
    expect(tape.scrollTop).toBe(0);
    await waitFor(() => expect(container.querySelector('.pill.latest')?.textContent).toBe('2 new · 1 for you'));
  });
});

describe('a hidden page', () => {
  const ms = [1, 2, 3, 4, 5, 6].map((i) => message(i, i % 2 ? 'builder' : 'reviewer', `message ${i}`, 60 - i * 7));

  it('reads nothing that came while hidden, and puts the NEW line above it on return', async () => {
    layout();
    const { container, props, rerender } = room(ms);
    const tape = container.querySelector('.msgs') as HTMLElement;
    expect(tape.scrollTop).toBe(700); // at the bottom, following
    vi.mocked(props.onSeen).mockClear();
    const state = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
    const more = [...ms, message(7, 'builder', 'one more', 2), message(8, 'release', '@operator tag it', 1)];
    rerender(<RoomView {...props} messages={more} />);
    expect(tape.scrollTop).toBe(700); // no follow while hidden
    expect(props.onSeen).not.toHaveBeenCalled();
    await waitFor(() => expect(container.querySelector('.pill.latest')?.textContent).toBe('2 new · 1 for you'));
    state.mockReturnValue('visible');
    act(() => {
      document.dispatchEvent(new Event('visibilitychange'));
    });
    await waitFor(() =>
      expect(container.querySelector('.newline')?.nextElementSibling?.getAttribute('data-mid')).toBe('7'),
    );
  });
});

describe('composing', () => {
  it('completes a name after @ instead of posting', () => {
    const onDraft = vi.fn();
    const { props, rerender } = room([], { onDraft });
    const box = screen.getByLabelText('Message #example-app') as HTMLTextAreaElement;
    fireEvent.change(box, { target: { value: '@rev', selectionStart: 4 } });
    rerender(<RoomView {...props} draft="@rev" />);
    expect(screen.getByRole('option', { name: /reviewer/, selected: true })).toBeTruthy();
    expect(box.getAttribute('aria-expanded')).toBe('true');
    fireEvent.keyDown(box, { key: 'Enter' });
    expect(onDraft).toHaveBeenLastCalledWith('@reviewer ');
    expect(props.onPost).not.toHaveBeenCalled();
  });

  it('posts with Enter', async () => {
    const { props } = room([], { draft: '@builder please rebase' });
    fireEvent.keyDown(screen.getByLabelText('Message #example-app'), { key: 'Enter' });
    await waitFor(() => expect(props.onPost).toHaveBeenCalledWith('@builder please rebase', undefined));
  });

  it('replies to a message, and Escape cancels the reply', async () => {
    const ms = [message(1, 'builder', 'PR #57 is up', 5)];
    const { props } = room(ms, { draft: 'done' });
    fireEvent.click(screen.getByRole('button', { name: 'Reply' }));
    expect(screen.getByText(/Replying to/).textContent).toContain('builder');
    const box = screen.getByLabelText('Message #example-app');
    fireEvent.keyDown(box, { key: 'Escape' });
    expect(screen.queryByText(/Replying to/)).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Reply' }));
    fireEvent.keyDown(box, { key: 'Enter' });
    await waitFor(() => expect(props.onPost).toHaveBeenCalledWith('done', 1n));
    await waitFor(() => expect(screen.queryByText(/Replying to/)).toBeNull());
  });
});

describe('room list', () => {
  it('shows mention counts as @n and marks followed rooms', () => {
    const rooms = [create(RoomSchema, { name: 'example-app' }), create(RoomSchema, { name: 'website' })];
    const unread = new Map([['example-app', { unread: 7, addressed: 2 }]]);
    const { container } = render(<RoomNav rooms={rooms} unread={unread} followed={new Set(['example-app'])} />);
    expect(container.querySelector('em.mention')?.textContent).toBe('@2');
    expect([...container.querySelectorAll('a.followed')].map((a) => a.textContent)).toEqual(['#example-app@2']);
  });
});

describe('bridged rooms', () => {
  const rooms = [
    create(RoomSchema, { name: 'example-app', purpose: 'Checkout and payments' }),
    create(RoomSchema, { name: 'example-chat', purpose: 'A chat outside', pending: 2 }),
  ];
  const chat = (b = bridge('example-chat', 'running')) => new Map([['example-chat', b]]);
  const link = (c: HTMLElement, name: string) => c.querySelector(`a[href="#/rooms/${name}"]`) as HTMLElement;
  const bridgedRoom = (messages: Message[] | undefined, more: Partial<Props> = {}) =>
    room(messages, {
      name: 'example-chat',
      room: create(RoomSchema, { name: 'example-chat', purpose: 'A chat outside' }),
      bridge: bridge('example-chat', 'running'),
      ...more,
    });

  it('marks a bridged room with a bridge and others with the stoa', () => {
    const { container } = render(<RoomNav rooms={rooms} unread={new Map()} followed={new Set()} bridges={chat()} />);
    expect(link(container, 'example-chat').querySelector('svg.br title')?.textContent).toBe('bridge running');
    expect(link(container, 'example-app').querySelector('svg.br')).toBeNull();
  });

  it('draws a failing bridge so it shows at a glance', () => {
    const failing = chat(bridge('example-chat', 'restarting', 'approve', 'exit status 1'));
    const { container } = render(<RoomNav rooms={rooms} unread={new Map()} followed={new Set()} bridges={failing} />);
    const icon = link(container, 'example-chat').querySelector('svg.br') as SVGElement;
    expect(icon.classList).toContain('restarting');
    expect(icon.querySelector('title')?.textContent).toBe('bridge restarting · exit status 1');
    cleanup();
    const view = render(<Rooms rooms={rooms} unread={new Map()} followed={new Set()} bridges={failing} />);
    expect(link(view.container, 'example-chat').querySelector('.sum.down')?.textContent).toBe(
      'bridge restarting · exit status 1',
    );
    cleanup();
    const { container: head } = bridgedRoom([], { bridge: bridge('example-chat', 'restarting', 'approve', 'exit') });
    expect(head.querySelector('.roomhead .brstate')?.textContent).toBe('bridge restarting');
    expect(head.querySelector('.roomhead h2 svg.br.restarting')).not.toBeNull();
  });

  it('shows how many messages wait for the operator next to the unread count', () => {
    const unread = new Map([['example-chat', { unread: 3, addressed: 0, pending: 2 }]]);
    const { container } = render(<RoomNav rooms={rooms} unread={unread} followed={new Set()} bridges={chat()} />);
    const a = link(container, 'example-chat');
    expect(a.querySelector('em.pending')?.textContent).toBe('2 waiting for you to send');
    expect(a.querySelector('em:not(.pending)')?.textContent).toBe('3');
    expect(link(container, 'example-app').querySelector('em')).toBeNull();
  });

  it('changes the outbound policy only when one is picked', () => {
    const { props } = bridgedRoom([]);
    const button = screen.getByRole('button', { name: 'Outbound: Ask before sending' });
    fireEvent.keyDown(button, { key: 'ArrowDown' });
    const menu = screen.getByRole('menu', { name: 'Outbound' });
    const items = within(menu).getAllByRole('menuitemradio');
    expect(items.map((i) => [i.textContent, i.getAttribute('aria-checked')])).toEqual([
      ['●Ask before sending', 'true'],
      ['Send at once', 'false'],
      ['Read only', 'false'],
    ]);
    fireEvent.keyDown(menu, { key: 'ArrowDown' });
    expect(document.activeElement).toBe(items[1]);
    fireEvent.keyDown(menu, { key: 'Escape' });
    expect(document.activeElement).toBe(button);
    expect(props.onPolicy).not.toHaveBeenCalled();
    fireEvent.click(button);
    fireEvent.click(screen.getByRole('menuitemradio', { name: 'Send at once' }));
    expect(props.onPolicy).toHaveBeenCalledWith('open');
  });

  it('has no policy control in a room without a bridge', () => {
    room([]);
    expect(screen.queryByRole('button', { name: /^Outbound/ })).toBeNull();
  });
});

describe('messages from outside', () => {
  const ms = [
    outside(1, 'Ada', '42', 'can you look?', 9),
    outside(2, 'Ada', '42', 'it is the login page', 8.5),
    outside(3, 'Bob', '77', 'same here', 7.5),
    message(4, 'secretary', 'looking', 6, { room: 'example-chat', replyTo: 3n }),
  ];
  const open = vi.fn();
  const chatRoom = () =>
    room(
      ms,
      {
        name: 'example-chat',
        bridge: bridge('example-chat', 'running'),
        agents: [...agents, agent('secretary', 'idle')],
      },
      ({ children }) => <OpenAgent.Provider value={open}>{children}</OpenAgent.Provider>,
    );

  it('names the author outside with the bridge and a neutral mark', () => {
    const { container } = chatRoom();
    expect(header(container, 1)).toBe('Ada · example-chatreply');
    const mark = tablet(container, 1).querySelector('.av') as HTMLElement;
    expect(mark.classList).toContain('ext');
    expect(mark.textContent).toBe('A');
    expect(tablet(container, 1).querySelector('[data-sigil]')).toBeNull();
  });

  it('shows who it is outside instead of an agent drawer', () => {
    const { container } = chatRoom();
    const name = within(tablet(container, 1)).getByRole('button', { name: 'Ada' });
    fireEvent.click(name);
    expect(name.getAttribute('aria-expanded')).toBe('true');
    const card = tablet(container, 1).querySelector('.card')?.textContent;
    expect(card).toContain('Ada');
    expect(card).toContain('example-chat');
    expect(card).toContain('id there: 42');
    expect(open).not.toHaveBeenCalled();
    fireEvent.keyDown(name, { key: 'Escape' });
    expect(tablet(container, 1).querySelector('.card')).toBeNull();
  });

  it('gives each person outside a header of their own', () => {
    const { container } = chatRoom();
    expect(tablet(container, 2).classList).toContain('cont');
    expect(tablet(container, 3).classList).not.toContain('cont');
    expect(header(container, 3)).toBe('Bob · example-chatreply');
  });

  it('leaves people outside and the board out of conversations', () => {
    const notice = message(5, 'agora', '@secretary your message 4 was not sent: chat not found.', 5, {
      room: 'example-chat',
    });
    const { container } = room([...ms, notice], {
      name: 'example-chat',
      agents: [...agents, agent('secretary', 'idle')],
    });
    expect(header(container, 4)).toBe('secretaryreply'); // a reply to Bob addresses no one
    expect(tablet(container, 4).querySelector('.quote .qa')?.textContent).toBe('Bob');
    expect(container.querySelector('.pairs')).toBeNull();
  });

  it('lists people outside apart from the agents in the room', () => {
    const { container } = chatRoom();
    const groups = [...container.querySelectorAll('.rail .people')];
    const names = (g: Element | undefined) => [...(g?.querySelectorAll('.pgname') ?? [])].map((p) => p.textContent);
    expect(names(groups[0])).toEqual(['secretary', 'operator']);
    expect(container.querySelector('.outsiders h3')?.textContent).toBe('OUTSIDE · 2');
    expect(names(groups[1])).toEqual(['Ada', 'Bob']);
    const ada = within(groups[1] as HTMLElement).getByRole('button', { name: 'Ada' });
    expect(ada.querySelector('.av.ext')?.textContent).toBe('A');
    fireEvent.click(ada);
    expect(groups[1]?.querySelector('.card')?.textContent).toContain('id there: 42');
    expect(open).not.toHaveBeenCalled();
  });
});

describe('messages going out', () => {
  const chatRoom = (messages: Message[], more: Partial<Props> = {}) =>
    room(messages, { name: 'example-chat', bridge: bridge('example-chat', 'running'), ...more });

  it('shows a pending message as waiting for the operator, with Send and Don’t send', () => {
    const { container } = chatRoom([goingOut(1, 'secretary', 'looked, all fine', 5, 'pending')]);
    const t = tablet(container, 1);
    expect(t.classList).toContain('waiting');
    const decide = within(t).getByRole('group', { name: 'Send the message of secretary to example-chat?' });
    expect(decide.textContent).toContain('Waiting for you');
    expect(within(decide).getByRole('button', { name: 'Send' })).toBeTruthy();
    expect(within(decide).getByRole('button', { name: 'Don’t send' })).toBeTruthy();
  });

  it('sends a pending message, then shows a check', async () => {
    let done = () => {};
    const onDecide = vi.fn(() => new Promise<void>((r) => (done = r)));
    const pending = goingOut(1, 'secretary', 'looked, all fine', 5, 'pending');
    const { container, props, rerender } = chatRoom([pending], { onDecide });
    const send = within(tablet(container, 1)).getByRole('button', { name: 'Send' }) as HTMLButtonElement;
    fireEvent.click(send);
    expect(onDecide).toHaveBeenCalledWith(pending, true);
    expect(send.disabled).toBe(true);
    await act(async () => done());
    expect(send.disabled).toBe(false);
    rerender(<RoomView {...props} messages={[goingOut(1, 'secretary', 'looked, all fine', 5, 'sending')]} />);
    expect(tablet(container, 1).querySelector('.dl')?.textContent).toBe('sending…');
    rerender(<RoomView {...props} messages={[goingOut(1, 'secretary', 'looked, all fine', 5, 'sent')]} />);
    const t = tablet(container, 1);
    expect(t.querySelector('.dl.sent svg')).not.toBeNull();
    expect(t.querySelector('.decide')).toBeNull();
    expect(t.classList).not.toContain('waiting');
  });

  it('declines a pending message, then shows it muted as not sent', () => {
    const pending = goingOut(1, 'secretary', 'something rude', 5, 'pending');
    const { container, props, rerender } = chatRoom([pending]);
    fireEvent.click(within(tablet(container, 1)).getByRole('button', { name: 'Don’t send' }));
    expect(props.onDecide).toHaveBeenCalledWith(pending, false);
    rerender(<RoomView {...props} messages={[goingOut(1, 'secretary', 'something rude', 5, 'declined')]} />);
    const t = tablet(container, 1);
    expect(t.classList).toContain('declined');
    expect(t.querySelector('.dl')?.textContent).toBe('not sent');
    expect(t.querySelector('.decide')).toBeNull();
  });

  it('gives every message going out a header of its own', () => {
    const { container } = chatRoom([
      goingOut(1, 'secretary', 'first', 5, 'sent'),
      goingOut(2, 'secretary', 'second', 4, 'sending'),
    ]);
    expect(tablet(container, 2).classList).not.toContain('cont');
    expect(tablet(container, 2).querySelector('.dl')?.textContent).toBe('sending…');
  });

  it('keeps Send from a read-only room, but not Don’t send', () => {
    const pending = goingOut(1, 'secretary', 'looked, all fine', 5, 'pending');
    const { container, props } = chatRoom([pending], { bridge: bridge('example-chat', 'running', 'read') });
    const t = tablet(container, 1);
    expect((within(t).getByRole('button', { name: 'Send' }) as HTMLButtonElement).disabled).toBe(true);
    expect(t.querySelector('.hint')?.textContent).toBe('read only — switch Outbound to send');
    fireEvent.click(within(t).getByRole('button', { name: 'Don’t send' }));
    expect(props.onDecide).toHaveBeenCalledWith(pending, false);
  });

  it('says why the bridge could not send a message', () => {
    const { container } = chatRoom([goingOut(1, 'secretary', 'hello', 5, 'failed', 'chat not found')]);
    expect(tablet(container, 1).querySelector('.dl')?.textContent).toBe('not sent');
    expect(tablet(container, 1).querySelector('.dlerr')?.textContent).toBe('Not sent: chat not found');
  });

  it('counts a new pending message as for the operator while scrolled up', async () => {
    layout();
    const ms = [1, 2, 3, 4, 5, 6].map((i) =>
      message(i, 'builder', `message ${i}`, 60 - i * 7, { room: 'example-chat' }),
    );
    const { container, props, rerender } = chatRoom(ms);
    const tape = container.querySelector('.msgs') as HTMLElement;
    tape.scrollTop = 0;
    fireEvent.scroll(tape);
    rerender(<RoomView {...props} messages={[...ms, goingOut(7, 'secretary', 'draft', 1, 'pending')]} />);
    await waitFor(() => expect(container.querySelector('.pill.latest')?.textContent).toBe('1 new · 1 for you'));
  });
});
