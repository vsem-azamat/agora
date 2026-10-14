import { create } from '@bufbuild/protobuf';
import { timestampFromDate } from '@bufbuild/protobuf/wkt';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { type ComponentProps, type ReactNode, useState } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { agent, bridge, goingOut, message, NOW, note, outside } from '../fixtures';
import type { Message } from '../gen/agora/v1/rooms_pb';
import { RoomSchema, ThreadSummarySchema } from '../gen/agora/v1/rooms_pb';
import { setClockOffset } from '../hubclock';
import type { Saved } from '../tape';
import { OpenAgent } from './common';
import { nextUnreadThread, RoomView } from './room';
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
    note: false,
    onNote: vi.fn(),
    onPost: vi.fn(async () => {}),
    onFollow: vi.fn(),
    onSeen: vi.fn(),
    onDecide: vi.fn(async () => true),
    onPolicy: vi.fn(),
    onSendOut: vi.fn(async () => true),
    onArchive: vi.fn(),
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

/** A ResizeObserver that remembers what it observes; `resize` calls back for the tape only. */
function observeResizes() {
  const observed = new Map<Element, () => void>();
  vi.stubGlobal(
    'ResizeObserver',
    class {
      constructor(private f: () => void) {}
      observe(el: Element) {
        observed.set(el, this.f);
      }
      disconnect() {
        observed.clear();
      }
    },
  );
  return (tape: Element) => act(() => observed.get(tape)?.());
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

  it('closes the mode menu when the room changes', () => {
    const { props, rerender } = room(ms, { mode: 'all' });
    fireEvent.click(screen.getByRole('button', { name: 'Follow mode: Every message' }));
    expect(screen.getByRole('menu', { name: 'Follow mode' })).toBeTruthy();
    rerender(<RoomView {...props} name="other-app" room={create(RoomSchema, { name: 'other-app', messages: 3 })} />);
    expect(screen.queryByRole('menu')).toBeNull();
    expect(screen.getByRole('button', { name: 'Follow mode: Every message' }).getAttribute('aria-expanded')).toBe(
      'false',
    );
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

  it('stays at the newest message when the room gets shorter', () => {
    layout();
    const resize = observeResizes();
    try {
      const { container } = room(ms);
      const tape = container.querySelector('.msgs') as HTMLElement;
      const bottom = () => tape.scrollHeight - tape.scrollTop - tape.clientHeight;
      tape.scrollTop = tape.scrollHeight - tape.clientHeight; // as a browser leaves it at the bottom
      fireEvent.scroll(tape);
      // the keyboard comes up: the tape is shorter, its scroll position is not
      vi.spyOn(Element.prototype, 'clientHeight', 'get').mockReturnValue(100);
      expect(bottom()).toBe(150);
      resize(tape);
      expect(bottom()).toBeLessThanOrEqual(0);
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it('stays where it was scrolled to when the room gets shorter', () => {
    layout();
    const resize = observeResizes();
    try {
      const { container } = room(ms);
      const tape = container.querySelector('.msgs') as HTMLElement;
      tape.scrollTop = 0;
      fireEvent.scroll(tape);
      vi.spyOn(Element.prototype, 'clientHeight', 'get').mockReturnValue(100);
      resize(tape);
      expect(tape.scrollTop).toBe(0);
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it('keeps following after a jump to a message that is already at the bottom', () => {
    layout();
    const ms2 = [...ms, message(9, 'reviewer', 'about the last one', 1, { replyTo: 6n })];
    const { container, props, rerender } = room(ms2);
    const tape = container.querySelector('.msgs') as HTMLElement;
    tape.scrollTop = tape.scrollHeight - tape.clientHeight;
    fireEvent.scroll(tape);
    fireEvent.click(tablet(container, 9).querySelector('.quote') as HTMLElement);
    rerender(<RoomView {...props} messages={[...ms2, message(10, 'builder', 'next', 0)]} />);
    expect(tape.scrollHeight - tape.scrollTop - tape.clientHeight).toBeLessThanOrEqual(0);
  });

  it('keeps following after arriving at the newest message', () => {
    layout();
    // a browser clamps the scroll position to what can be scrolled
    const tops = new WeakMap<Element, number>();
    vi.spyOn(Element.prototype, 'scrollTop', 'get').mockImplementation(function (this: Element) {
      return tops.get(this) ?? 0;
    });
    vi.spyOn(Element.prototype, 'scrollTop', 'set').mockImplementation(function (this: Element, v: number) {
      tops.set(this, Math.max(0, Math.min(v, this.scrollHeight - this.clientHeight)));
    });
    const { container, props, rerender } = room(ms, { arrival: { room: 'example-app', jump: 6n } });
    const tape = container.querySelector('.msgs') as HTMLElement;
    rerender(<RoomView {...props} arrival={undefined} messages={[...ms, message(10, 'builder', 'next', 0)]} />);
    expect(tape.scrollHeight - tape.scrollTop - tape.clientHeight).toBeLessThanOrEqual(0);
  });

  it('does not pin when the room gets taller', () => {
    layout();
    const resize = observeResizes();
    try {
      const { container } = room(ms);
      const tape = container.querySelector('.msgs') as HTMLElement;
      tape.scrollTop = tape.scrollHeight - tape.clientHeight - 40; // following, just above the bottom
      fireEvent.scroll(tape);
      const at = tape.scrollTop;
      vi.spyOn(Element.prototype, 'clientHeight', 'get').mockReturnValue(300);
      resize(tape);
      expect(tape.scrollTop).toBe(at);
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it('lets a jump to a quoted message finish when the room changes size', () => {
    layout();
    const resize = observeResizes();
    try {
      const { container } = room([...ms, message(9, 'reviewer', 'about the first one', 1, { replyTo: 1n })]);
      const tape = container.querySelector('.msgs') as HTMLElement;
      tape.scrollTop = tape.scrollHeight - tape.clientHeight;
      fireEvent.scroll(tape);
      fireEvent.click(tablet(container, 9).querySelector('.quote') as HTMLElement);
      const at = tape.scrollTop;
      vi.spyOn(Element.prototype, 'clientHeight', 'get').mockReturnValue(100);
      resize(tape);
      expect(tape.scrollTop).toBe(at);
    } finally {
      vi.unstubAllGlobals();
    }
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

  it('keeps following after the return when nothing is below the view', async () => {
    layout();
    const one = [message(1, 'builder', 'hello', 9)];
    const { container, props, rerender } = room(one);
    const tape = container.querySelector('.msgs') as HTMLElement;
    const state = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
    const two = [...one, message(2, 'builder', 'more', 5)];
    rerender(<RoomView {...props} messages={two} />);
    state.mockReturnValue('visible');
    act(() => {
      document.dispatchEvent(new Event('visibilitychange'));
    });
    const many = [...two, ...[3, 4, 5, 6, 7, 8].map((i) => message(i, 'builder', `message ${i}`, 4))];
    rerender(<RoomView {...props} messages={many} />);
    await waitFor(() => expect(tape.scrollHeight - tape.scrollTop - tape.clientHeight).toBeLessThanOrEqual(0));
  });

  it('keeps the NEW line in view when the room gets shorter after the return', async () => {
    layout();
    const resize = observeResizes();
    try {
      const { container, props, rerender } = room(ms);
      const tape = container.querySelector('.msgs') as HTMLElement;
      tape.scrollTop = tape.scrollHeight - tape.clientHeight;
      fireEvent.scroll(tape);
      const state = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
      rerender(<RoomView {...props} messages={[...ms, message(7, 'builder', 'one more', 2)]} />);
      state.mockReturnValue('visible');
      act(() => {
        document.dispatchEvent(new Event('visibilitychange'));
      });
      await waitFor(() => expect(container.querySelector('.newline')).not.toBeNull());
      const at = tape.scrollTop;
      vi.spyOn(Element.prototype, 'clientHeight', 'get').mockReturnValue(100);
      resize(tape);
      expect(tape.scrollTop).toBe(at);
    } finally {
      vi.unstubAllGlobals();
    }
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

  it('keeps the focus, and the phone’s keyboard, in the box when Send is pressed', () => {
    room([], { draft: 'done' });
    // a press whose default is prevented does not move the focus
    expect(fireEvent.mouseDown(screen.getByRole('button', { name: 'Send' }))).toBe(false);
  });

  it('posts with Enter', async () => {
    const { props } = room([], { draft: '@builder please rebase' });
    fireEvent.keyDown(screen.getByLabelText('Message #example-app'), { key: 'Enter' });
    await waitFor(() => expect(props.onPost).toHaveBeenCalledWith('@builder please rebase', undefined, false));
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
    await waitFor(() => expect(props.onPost).toHaveBeenCalledWith('done', 1n, false));
    await waitFor(() => expect(screen.queryByText(/Replying to/)).toBeNull());
  });
});

describe('a long message', () => {
  // a browser without CSS field-sizing, such as Safari: the box is sized from its content
  function withoutFieldSizing(scroll: number) {
    vi.stubGlobal('CSS', { supports: () => false });
    const was = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollHeight');
    Object.defineProperty(HTMLTextAreaElement.prototype, 'scrollHeight', { configurable: true, get: () => scroll });
    return () => {
      vi.unstubAllGlobals();
      delete (HTMLTextAreaElement.prototype as { scrollHeight?: number }).scrollHeight;
      if (was) Object.defineProperty(HTMLElement.prototype, 'scrollHeight', was);
    };
  }

  it('grows with its text where the browser cannot size the box', () => {
    const restore = withoutFieldSizing(120);
    try {
      const { props, rerender } = room([]);
      const box = screen.getByLabelText('Message #example-app') as HTMLTextAreaElement;
      expect(box.style.height).toBe('');
      rerender(<RoomView {...props} draft={'a long line that wraps '.repeat(10)} />);
      expect(box.style.height).toBe('120px');
      // emptied, the box goes back to one line
      rerender(<RoomView {...props} draft="" />);
      expect(box.style.height).toBe('');
    } finally {
      restore();
    }
  });

  it('leaves the box to the browser where it sizes the box itself', () => {
    vi.stubGlobal('CSS', { supports: () => true });
    try {
      const { props, rerender } = room([]);
      rerender(<RoomView {...props} draft={'a long line that wraps '.repeat(10)} />);
      expect((screen.getByLabelText('Message #example-app') as HTMLTextAreaElement).style.height).toBe('');
    } finally {
      vi.unstubAllGlobals();
    }
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
    create(RoomSchema, { name: 'example-chat', purpose: 'A chat outside', scheduled: 2 }),
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
    const failing = chat(bridge('example-chat', 'restarting', 'delay', 'exit status 1'));
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
    const { container: head } = bridgedRoom([], { bridge: bridge('example-chat', 'restarting', 'delay', 'exit') });
    expect(head.querySelector('.roomhead .brstate')?.textContent).toBe('bridge restarting');
    expect(head.querySelector('.roomhead h2 svg.br.restarting')).not.toBeNull();
  });

  it('warns in the header when the bridge speaks another protocol version', () => {
    const mark = (version: number) => {
      const { container } = bridgedRoom([], { bridge: bridge('example-chat', 'running', 'delay', '', version) });
      const found = container.querySelector('.roomhead .brwarn');
      cleanup();
      return found?.textContent ?? null;
    };
    expect(mark(2)).toBe('protocol v2, hub v3');
    expect(mark(3)).toBeNull(); // the hub's own version
    expect(mark(0)).toBeNull(); // not reported
  });

  it('shows how many messages wait for the operator next to the unread count', () => {
    const unread = new Map([['example-chat', { unread: 3, addressed: 0, scheduled: 2 }]]);
    const { container } = render(<RoomNav rooms={rooms} unread={unread} followed={new Set()} bridges={chat()} />);
    const a = link(container, 'example-chat');
    expect(a.querySelector('em.scheduled')?.textContent).toBe('2 scheduled to go out');
    expect(a.querySelector('em:not(.scheduled)')?.textContent).toBe('3');
    expect(link(container, 'example-app').querySelector('em')).toBeNull();
  });

  it('changes the outbound policy only when one is picked', () => {
    const { props } = bridgedRoom([]);
    const button = screen.getByRole('button', { name: 'Outbound: Send after a delay' });
    expect(button.textContent).toBe('OutboundSend after a delay▾'); // the name shows on the chip too
    fireEvent.keyDown(button, { key: 'ArrowDown' });
    const menu = screen.getByRole('menu', { name: 'Outbound' });
    const items = within(menu).getAllByRole('menuitemradio');
    expect(items.map((i) => [i.textContent, i.getAttribute('aria-checked')])).toEqual([
      ['●Send after a delay', 'true'],
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

  it('opens a menu on the side of its chip where it fits', () => {
    // on a phone the chip is at the left edge: a menu aligned to the chip's right end would start off the screen
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
      const left = this.classList.contains('menu') && !this.classList.contains('start') ? -49 : 16;
      return { left, right: left + 200, top: 0, bottom: 100, width: 200, height: 100, x: left, y: 0 } as DOMRect;
    });
    bridgedRoom([]);
    fireEvent.click(screen.getByRole('button', { name: 'Outbound: Send after a delay' }));
    expect(screen.getByRole('menu', { name: 'Outbound' }).classList).toContain('start');
  });

  it('keeps a menu at its chip’s right end where it fits', () => {
    bridgedRoom([]);
    fireEvent.click(screen.getByRole('button', { name: 'Outbound: Send after a delay' }));
    expect(screen.getByRole('menu', { name: 'Outbound' }).classList).not.toContain('start');
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

  it('tells people outside without an id apart by their names', () => {
    const anonymous = [
      outside(1, 'Ada', '', 'hello', 3),
      outside(2, 'Bob', '', 'hi', 2.5),
      outside(3, 'Bob', '', 'again', 2),
    ];
    const { container } = room(anonymous, { name: 'example-chat', bridge: bridge('example-chat', 'running') });
    expect(tablet(container, 2).classList).not.toContain('cont');
    expect(tablet(container, 3).classList).toContain('cont');
    expect([...container.querySelectorAll('.outsiders .pgname')].map((p) => p.textContent)).toEqual(['Ada', 'Bob']);
  });

  it('shows what the operator wrote outside as the operator’s', () => {
    const mine = message(9, 'operator', 'writing from the phone', 1, { room: 'example-chat' });
    const { container } = room([...ms, mine], { name: 'example-chat', bridge: bridge('example-chat', 'running') });
    const t = tablet(container, 9);
    expect(t.classList).toContain('own');
    expect(t.querySelector('.av.ext')).toBeNull();
    expect(header(container, 9)).toBe('operatorreply');
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

  it('counts down a scheduled message, with Send now and Cancel', () => {
    vi.useFakeTimers({ now: NOW });
    const due = timestampFromDate(new Date(NOW.getTime() + 42_000));
    const { container } = chatRoom([goingOut(1, 'secretary', 'looked, all fine', 5, 'scheduled', '', due)]);
    const t = tablet(container, 1);
    expect(t.classList).toContain('waiting');
    const decide = within(t).getByRole('group', { name: 'Send the message of secretary to example-chat?' });
    expect(decide.querySelector('.why')?.textContent).toBe('goes out in 0:42');
    act(() => vi.advanceTimersByTime(1000));
    expect(decide.querySelector('.why')?.textContent).toBe('goes out in 0:41');
    act(() => vi.advanceTimersByTime(41_000));
    expect(decide.querySelector('.why')?.textContent).toBe('going out…');
    vi.useRealTimers();
    expect(within(decide).getByRole('button', { name: 'Send now' })).toBeTruthy();
    expect(within(decide).getByRole('button', { name: 'Cancel' })).toBeTruthy();
  });

  it('counts down by the hub clock when the browser clock is 20 seconds ahead', () => {
    vi.useFakeTimers({ now: NOW });
    setClockOffset(-20_000); // the hub reads 20 seconds less than this browser
    try {
      const due = timestampFromDate(new Date(NOW.getTime() + 22_000)); // 42 seconds ahead by the hub's clock
      const { container } = chatRoom([goingOut(1, 'secretary', 'looked, all fine', 5, 'scheduled', '', due)]);
      const decide = within(tablet(container, 1)).getByRole('group', {
        name: 'Send the message of secretary to example-chat?',
      });
      expect(decide.querySelector('.why')?.textContent).toBe('goes out in 0:42');
      act(() => vi.advanceTimersByTime(41_000)); // the ticks keep to the hub's clock too
      expect(decide.querySelector('.why')?.textContent).toBe('goes out in 0:01');
      act(() => vi.advanceTimersByTime(1000));
      expect(decide.querySelector('.why')?.textContent).toBe('going out…');
    } finally {
      setClockOffset(0);
      vi.useRealTimers();
    }
  });

  it('sends a scheduled message at once, then shows a check', async () => {
    let done = (_: boolean) => {};
    const onDecide = vi.fn(() => new Promise<boolean>((r) => (done = r)));
    const scheduled = goingOut(1, 'secretary', 'looked, all fine', 5, 'scheduled');
    const { container, props, rerender } = chatRoom([scheduled], { onDecide });
    const send = within(tablet(container, 1)).getByRole('button', { name: 'Send now' }) as HTMLButtonElement;
    fireEvent.click(send);
    expect(onDecide).toHaveBeenCalledWith(scheduled, true);
    expect(send.disabled).toBe(true);
    await act(async () => done(true));
    // taken: both stay off until the message stops waiting, so a second click cannot be refused
    expect(send.disabled).toBe(true);
    expect((within(tablet(container, 1)).getByRole('button', { name: 'Cancel' }) as HTMLButtonElement).disabled).toBe(
      true,
    );
    rerender(<RoomView {...props} messages={[goingOut(1, 'secretary', 'looked, all fine', 5, 'sending')]} />);
    expect(tablet(container, 1).querySelector('.dl')?.textContent).toBe('sending…');
    rerender(<RoomView {...props} messages={[goingOut(1, 'secretary', 'looked, all fine', 5, 'sent')]} />);
    const t = tablet(container, 1);
    expect(t.querySelector('.dl.sent svg')).not.toBeNull();
    expect(t.querySelector('.decide')).toBeNull();
    expect(t.classList).not.toContain('waiting');
  });

  it('turns the buttons on again when the hub refused the decision', async () => {
    const onDecide = vi.fn(async () => false);
    const { container } = chatRoom([goingOut(1, 'secretary', 'draft', 5, 'scheduled')], { onDecide });
    const send = within(tablet(container, 1)).getByRole('button', { name: 'Send now' }) as HTMLButtonElement;
    fireEvent.click(send);
    await waitFor(() => expect(send.disabled).toBe(false));
  });

  it('cancels a scheduled message, then shows it muted as not sent', () => {
    const scheduled = goingOut(1, 'secretary', 'something rude', 5, 'scheduled');
    const { container, props, rerender } = chatRoom([scheduled]);
    fireEvent.click(within(tablet(container, 1)).getByRole('button', { name: 'Cancel' }));
    expect(props.onDecide).toHaveBeenCalledWith(scheduled, false);
    rerender(<RoomView {...props} messages={[goingOut(1, 'secretary', 'something rude', 5, 'cancelled')]} />);
    const t = tablet(container, 1);
    expect(t.classList).toContain('cancelled');
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

  it('holds a scheduled message while read only, with Cancel but not Send now', () => {
    const scheduled = goingOut(1, 'secretary', 'looked, all fine', 5, 'scheduled');
    const { container, props } = chatRoom([scheduled], { bridge: bridge('example-chat', 'running', 'read') });
    const t = tablet(container, 1);
    expect(t.querySelector('.why')?.textContent).toBe('held while read only');
    expect((within(t).getByRole('button', { name: 'Send now' }) as HTMLButtonElement).disabled).toBe(true);
    expect(t.querySelector('.hint')?.textContent).toBe('read only — switch Outbound to send');
    fireEvent.click(within(t).getByRole('button', { name: 'Cancel' }));
    expect(props.onDecide).toHaveBeenCalledWith(scheduled, false);
  });

  it('says that nobody knows whether a message went out when its bridge was removed', () => {
    const { container } = chatRoom([
      goingOut(1, 'secretary', 'hello', 5, 'unconfirmed', 'the bridge was removed before it answered'),
    ]);
    const mark = tablet(container, 1).querySelector('.dl');
    expect(mark?.textContent).toBe('unconfirmed');
    expect(mark?.classList.contains('unconfirmed')).toBe(true);
    expect(mark?.getAttribute('title')).toBe('check the chat before sending it again');
    expect(tablet(container, 1).querySelector('.dlerr')?.textContent).toBe(
      'Unconfirmed: the bridge was removed before it answered · check the chat before sending it again',
    );
  });

  it('holds a scheduled message of a room without a bridge any more', () => {
    const scheduled = goingOut(1, 'secretary', 'looked, all fine', 5, 'scheduled');
    const { container, props } = chatRoom([scheduled], { bridge: undefined });
    const t = tablet(container, 1);
    expect(t.querySelector('.why')?.textContent).toBe('held: no bridge');
    expect((within(t).getByRole('button', { name: 'Send now' }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(within(t).getByRole('button', { name: 'Cancel' }));
    expect(props.onDecide).toHaveBeenCalledWith(scheduled, false);
  });

  it('says why the bridge could not send a message', () => {
    const { container } = chatRoom([goingOut(1, 'secretary', 'hello', 5, 'failed', 'chat not found')]);
    expect(tablet(container, 1).querySelector('.dl')?.textContent).toBe('not sent');
    expect(tablet(container, 1).querySelector('.dlerr')?.textContent).toBe('Not sent: chat not found');
  });

  it('counts a new scheduled message as for the operator while scrolled up', async () => {
    layout();
    const ms = [1, 2, 3, 4, 5, 6].map((i) =>
      message(i, 'builder', `message ${i}`, 60 - i * 7, { room: 'example-chat' }),
    );
    const { container, props, rerender } = chatRoom(ms);
    const tape = container.querySelector('.msgs') as HTMLElement;
    tape.scrollTop = 0;
    fireEvent.scroll(tape);
    rerender(<RoomView {...props} messages={[...ms, goingOut(7, 'secretary', 'draft', 1, 'scheduled')]} />);
    await waitFor(() => expect(container.querySelector('.pill.latest')?.textContent).toBe('1 new · 1 for you'));
  });
});

describe('notes', () => {
  /** The room with the choice kept the way the app keeps it with the draft. */
  function Kept(p: Props) {
    const [note, setNote] = useState(p.note);
    return (
      <RoomView
        {...p}
        note={note}
        onNote={(n) => {
          p.onNote(n);
          setNote(n);
        }}
      />
    );
  }
  const chatRoom = (messages: Message[], more: Partial<Props> = {}) => {
    const r = room(messages, { name: 'example-chat', bridge: bridge('example-chat', 'running'), ...more });
    r.rerender(<Kept {...r.props} />);
    return r;
  };
  const form = (c: HTMLElement) => c.querySelector('form.compose') as HTMLElement;
  const box = () => screen.getByLabelText('Message #example-chat') as HTMLTextAreaElement;

  it('sends a note out while the room sends anything out', async () => {
    let done = (_: boolean) => {};
    const onSendOut = vi.fn(() => new Promise<boolean>((r) => (done = r)));
    const draft = note(1, 'secretary', 'Draft: the fix ships tonight', 5);
    const { container } = chatRoom([draft], { onSendOut });
    const out = within(tablet(container, 1)).getByRole('button', { name: 'Send out' }) as HTMLButtonElement;
    // the phone's inline one, next to reply
    expect(within(tablet(container, 1)).getByRole('button', { name: 'send out' }).className).toBe('mreply');
    fireEvent.click(out);
    expect(onSendOut).toHaveBeenCalledWith(draft);
    expect(out.disabled).toBe(true);
    await act(async () => done(false));
    expect(out.disabled).toBe(false);
    cleanup();
    const read = chatRoom([draft], { onSendOut, bridge: bridge('example-chat', 'running', 'read') });
    expect(within(tablet(read.container, 1)).queryByRole('button', { name: 'Send out' })).toBeNull();
  });

  it('marks a note that was sent out in its header, as a link to the message, and offers no Send out', () => {
    const sent = note(1, 'secretary', 'Draft: the fix ships tonight', 5);
    sent.sentOut = 2n;
    const out = message(2, 'secretary', 'Draft: the fix ships tonight', 4);
    const onSendOut = vi.fn(async () => true);
    const { container } = chatRoom([sent, out], { onSendOut });
    const tab = tablet(container, 1);
    expect(within(tab).queryByRole('button', { name: /^send out$/i })).toBeNull();
    expect(tab.querySelector('.mh .nt')?.textContent).toBe('note · sent out → #2');
    fireEvent.click(within(tab).getByRole('button', { name: 'sent out → #2' }));
    expect(onSendOut).not.toHaveBeenCalled();
    expect(container.querySelector('[data-mid="2"]')?.classList.contains('flash')).toBe(true);
    cleanup();
    // also where nothing can be sent out any more
    const read = chatRoom([sent, out], { onSendOut, bridge: bridge('example-chat', 'running', 'read') });
    expect(within(tablet(read.container, 1)).getByRole('button', { name: 'sent out → #2' })).toBeTruthy();
  });

  it('offers no choice in a room without a bridge', () => {
    const { container } = room([]);
    expect(screen.queryByRole('group', { name: 'Where the message goes' })).toBeNull();
    expect(form(container).className).toBe('compose');
  });

  it('sends to the chat unless the operator chooses a note', async () => {
    const { container, props } = chatRoom([], { draft: 'The release is on Friday.' });
    const to = screen.getByRole('radio', { name: 'To example-chat' }) as HTMLInputElement;
    expect(to.checked).toBe(true);
    expect(form(container).classList).toContain('out');
    expect(box().placeholder).toBe('To example-chat · @ to address someone');
    expect(screen.getByRole('button', { name: 'Send to example-chat' })).toBeTruthy();
    fireEvent.keyDown(box(), { key: 'Enter' });
    await waitFor(() => expect(props.onPost).toHaveBeenCalledWith('The release is on Friday.', undefined, false));
  });

  it('posts a note for the agents, drawn as a note before it is sent', async () => {
    const { container, props } = chatRoom([], { draft: '@secretary draft a reply to Ada about the release date' });
    fireEvent.click(screen.getByRole('radio', { name: 'Note for agents' }));
    expect(form(container).classList).toContain('note');
    expect(form(container).classList).not.toContain('out');
    expect(box().placeholder).toBe('Note for agents · not sent to example-chat · @ to address someone');
    expect(form(container).querySelector('.desthint')?.textContent).toBe('stays in Agora · not sent to example-chat');
    fireEvent.click(screen.getByRole('button', { name: 'Post the note' }));
    await waitFor(() =>
      expect(props.onPost).toHaveBeenCalledWith(
        '@secretary draft a reply to Ada about the release date',
        undefined,
        true,
      ),
    );
    // the choice stays for the next message
    expect((screen.getByRole('radio', { name: 'Note for agents' }) as HTMLInputElement).checked).toBe(true);
  });

  it('writes only notes in a read-only room', async () => {
    const { container, props } = chatRoom([], {
      draft: 'for the agents',
      bridge: bridge('example-chat', 'running', 'read'),
    });
    expect((screen.getByRole('radio', { name: 'To example-chat' }) as HTMLInputElement).disabled).toBe(true);
    expect((screen.getByRole('radio', { name: 'Note for agents' }) as HTMLInputElement).checked).toBe(true);
    expect(form(container).classList).toContain('note');
    fireEvent.keyDown(box(), { key: 'Enter' });
    await waitFor(() => expect(props.onPost).toHaveBeenCalledWith('for the agents', undefined, true));
  });

  it('keeps the note when a read-only room opens again', async () => {
    const { props, rerender } = chatRoom([], {
      draft: 'for the agents',
      bridge: bridge('example-chat', 'running', 'read'),
    });
    expect(props.onNote).toHaveBeenCalledWith(true);
    rerender(<Kept {...props} bridge={bridge('example-chat', 'running', 'open')} />);
    expect((screen.getByRole('radio', { name: 'Note for agents' }) as HTMLInputElement).checked).toBe(true);
    fireEvent.keyDown(box(), { key: 'Enter' });
    await waitFor(() => expect(props.onPost).toHaveBeenCalledWith('for the agents', undefined, true));
  });

  it('chooses a note to reply to a note', () => {
    const { container } = chatRoom([note(2, 'secretary', 'here is a draft, unsure about the date', 4)]);
    expect((screen.getByRole('radio', { name: 'To example-chat' }) as HTMLInputElement).checked).toBe(true);
    fireEvent.click(within(tablet(container, 2)).getByRole('button', { name: 'Reply' }));
    expect((screen.getByRole('radio', { name: 'Note for agents' }) as HTMLInputElement).checked).toBe(true);
  });

  it('posts a note as a note while the room’s bridge is not known yet', async () => {
    const { props } = room([], { name: 'example-chat', draft: 'keep it short', note: true });
    fireEvent.keyDown(screen.getByLabelText('Message #example-chat'), { key: 'Enter' });
    await waitFor(() => expect(props.onPost).toHaveBeenCalledWith('keep it short', undefined, true));
  });

  it('draws a note apart, without a delivery state or Send', () => {
    const odd = note(2, 'secretary', 'here is a draft, unsure about the date', 4);
    odd.deliveryState = goingOut(0, '', '', 0, 'scheduled').deliveryState; // never stored, never shown
    const { container } = chatRoom([goingOut(1, 'secretary', 'looked, all fine', 5, 'sent'), odd]);
    const t = tablet(container, 2);
    expect(t.classList).toContain('note');
    expect(t.querySelector('.nt')?.textContent).toBe('note · stays in Agora');
    expect(t.querySelector('.nt')?.getAttribute('title')).toBe('Not sent to example-chat');
    expect(t.querySelector('.dl')).toBeNull();
    expect(t.querySelector('.decide')).toBeNull();
    expect(t.classList).not.toContain('waiting');
  });

  it('never shares a header between a note and a message that is not one', () => {
    const { container } = chatRoom([
      message(1, 'operator', 'Thanks, Ada!', 5, { room: 'example-chat' }),
      note(2, 'operator', 'secretary: keep it short', 4),
      note(3, 'operator', 'and friendly', 3),
    ]);
    expect(tablet(container, 2).classList).not.toContain('cont');
    expect(tablet(container, 3).classList).toContain('cont');
  });
});

describe('archived rooms', () => {
  const archivedAt = timestampFromDate(new Date(NOW.getTime() - 2 * 3600_000));
  const old = create(RoomSchema, { name: 'old-app', purpose: 'Done', messages: 12, archivedBy: 'builder', archivedAt });
  const rooms = [create(RoomSchema, { name: 'example-app', purpose: 'Checkout and payments', messages: 3 })];

  it('archives from the header only once confirmed in the page', () => {
    const confirm = vi.spyOn(window, 'confirm');
    const { props } = room([message(1, 'builder', 'hi', 5)]);
    fireEvent.click(screen.getByRole('button', { name: 'Archive room' }));
    expect(document.activeElement?.textContent).toBe('Cancel');
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(props.onArchive).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Archive room' }));
    const ask = screen.getByRole('group', { name: 'Archive #example-app?' });
    fireEvent.click(within(ask).getByRole('button', { name: 'Archive' }));
    expect(props.onArchive).toHaveBeenCalledWith(true);
    expect(confirm).not.toHaveBeenCalled();
  });

  it('cancels on Escape without leaving a reply', () => {
    const { props } = room([message(1, 'builder', 'hi', 5)]);
    fireEvent.click(screen.getByRole('button', { name: 'Reply' }));
    fireEvent.click(screen.getByRole('button', { name: 'Archive room' }));
    fireEvent.keyDown(screen.getByRole('button', { name: 'Cancel' }), { key: 'Escape' });
    expect(screen.queryByRole('group', { name: 'Archive #example-app?' })).toBeNull();
    expect(document.querySelector('.replying')).not.toBeNull();
    expect(props.onArchive).not.toHaveBeenCalled();
  });

  it('offers no archiving in the general room or a bridged one', () => {
    room([], { name: 'general', room: create(RoomSchema, { name: 'general' }) });
    expect(screen.queryByRole('button', { name: 'Archive room' })).toBeNull();
    cleanup();
    room([], { name: 'example-chat', bridge: bridge('example-chat', 'running') });
    expect(screen.queryByRole('button', { name: 'Archive room' })).toBeNull();
  });

  it('shows an archived room read only, with who archived it and Unarchive', () => {
    const { props, container } = room([message(1, 'builder', 'hi', 5)], { name: 'old-app', room: old });
    expect(container.querySelector('.roomhead.archived .badge')?.textContent).toBe('archived');
    expect(container.querySelector('.archivedpanel p')?.textContent).toBe(
      'ARCHIVED — by builder on 9 Oct. New messages are not accepted, nobody is woken.',
    );
    expect(screen.queryByRole('textbox')).toBeNull();
    expect(screen.queryByRole('button', { name: /reply/i })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Archive room' })).toBeNull();
    expect(screen.queryByRole('button', { name: /^follow/ })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Unarchive' }));
    expect(props.onArchive).toHaveBeenCalledWith(false);
  });

  it('moves the focus to Unarchive once the room it archived is archived', () => {
    const { props, rerender } = room([message(1, 'builder', 'hi', 5)], {
      name: 'old-app',
      room: { ...old, archivedBy: '' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Archive room' }));
    fireEvent.click(screen.getByRole('button', { name: 'Archive' }));
    rerender(<RoomView {...props} room={old} />);
    expect(document.activeElement?.textContent).toBe('Unarchive');
  });

  it('ends the sidebar with a row to the archived rooms', () => {
    const { container } = render(<RoomNav rooms={rooms} archived={1} unread={new Map()} followed={new Set()} />);
    const row = container.querySelector('a.archivedrow') as HTMLAnchorElement;
    expect(row.getAttribute('href')).toBe('#/archive');
    expect(row.textContent).toBe('1 archived→');
    cleanup();
    const none = render(<RoomNav rooms={rooms} archived={0} unread={new Map()} followed={new Set()} />);
    expect(none.container.querySelector('a.archivedrow')).toBeNull();
  });

  it('switches the Rooms view between active and archived rooms', () => {
    const onUnarchive = vi.fn();
    const props = { rooms, archived: [old], onUnarchive, unread: new Map(), followed: new Set<string>(), now: NOW };
    const { container } = render(<Rooms {...props} tab="active" />);
    const tabs = within(screen.getByRole('navigation', { name: 'Rooms' }));
    expect(tabs.getByRole('link', { name: 'Active · 1' }).getAttribute('aria-current')).toBe('page');
    expect(tabs.getByRole('link', { name: 'Archived · 1' }).getAttribute('href')).toBe('#/archive');
    expect(container.querySelector('a[href="#/rooms/example-app"]')).not.toBeNull();
    expect(container.querySelector('a[href="#/rooms/old-app"]')).toBeNull();
    cleanup();
    const view = render(<Rooms {...props} tab="archived" />);
    expect(screen.getByRole('link', { name: 'Archived · 1' }).getAttribute('aria-current')).toBe('page');
    expect(view.container.querySelector('a[href="#/rooms/example-app"]')).toBeNull();
    const row = view.container.querySelector('.arow') as HTMLElement;
    expect(row.querySelector('a[href="#/rooms/old-app"] .sum')?.textContent).toBe(
      'archived by builder · 9 Oct · 12 msgs',
    );
    fireEvent.click(within(row).getByRole('button', { name: 'Unarchive #old-app' }));
    expect(onUnarchive).toHaveBeenCalledWith('old-app');
  });
});

describe('threads in the room', () => {
  const ms = [
    message(40, 'builder', 'the export is broken', 30),
    message(42, 'reviewer', 'fixed in #57', 10, { thread: 40n, also: true, threadTitle: 'Release 2.4' }),
  ];

  it('opens the thread of a message, and of a reply also posted to the room', () => {
    const onThread = vi.fn();
    const { container } = room(ms, { onThread });
    fireEvent.click(within(tablet(container, 40)).getByRole('button', { name: 'Thread' }));
    expect(onThread).toHaveBeenLastCalledWith(40n);
    fireEvent.click(within(tablet(container, 42)).getByRole('button', { name: 'in thread «Release 2.4»' }));
    expect(onThread).toHaveBeenLastCalledWith(40n);
  });

  it('shows the open thread in place of the conversations', () => {
    room(ms, {
      onThread: vi.fn(),
      thread: {
        root: 40n,
        messages: ms.slice(0, 1),
        onPost: vi.fn(),
        onTitle: vi.fn(),
        onLeave: vi.fn(),
        onSeen: vi.fn(),
      },
    });
    expect(screen.getByRole('complementary', { name: 'Thread' })).toBeTruthy();
    expect(screen.queryByRole('complementary', { name: 'Who is here' })).toBeNull();
  });

  const open = (root: bigint, messages = ms.slice(0, 1)) => ({
    root,
    messages,
    onPost: vi.fn(),
    onTitle: vi.fn(),
    onLeave: vi.fn(),
    onSeen: vi.fn(),
  });

  it('starts another thread empty: its draft and title box are its own', () => {
    const { props, rerender } = room(ms, { onThread: vi.fn(), thread: open(40n) });
    const box = () => screen.getByRole('combobox', { name: 'Reply in the thread #example-app' }) as HTMLTextAreaElement;
    fireEvent.change(box(), { target: { value: 'for the first thread' } });
    rerender(<RoomView {...props} thread={open(42n, [message(42, 'reviewer', 'another', 5)])} />);
    expect(box().value).toBe('');
  });

  it('counts nothing in the room as seen while a thread covers it on a phone, and catches up when it closes', () => {
    vi.stubGlobal('matchMedia', (q: string) => ({ matches: q.includes('max-width: 760px') }));
    const { props, rerender } = room(ms.slice(0, 1), { onThread: vi.fn(), thread: open(40n) });
    expect(props.onSeen).not.toHaveBeenCalled();
    rerender(<RoomView {...props} messages={ms} />);
    expect(props.onSeen).not.toHaveBeenCalled();
    expect(document.querySelector('section.room')?.hasAttribute('inert')).toBe(true);
    rerender(<RoomView {...props} messages={ms} thread={undefined} />);
    expect(props.onSeen).toHaveBeenCalledWith(42n);
    expect(document.querySelector('section.room')?.hasAttribute('inert')).toBe(false);
    vi.unstubAllGlobals();
  });

  it('gives the focus back to what opened the thread when it closes', () => {
    const onThread = vi.fn();
    const { props, container, rerender } = room(ms, { onThread });
    const action = within(tablet(container, 40)).getByRole('button', { name: 'Thread' });
    action.focus();
    fireEvent.click(action);
    rerender(<RoomView {...props} thread={open(40n)} />);
    (screen.getByRole('button', { name: 'Close the thread' }) as HTMLElement).focus();
    rerender(<RoomView {...props} thread={undefined} />);
    expect(document.activeElement).toBe(within(tablet(container, 40)).getByRole('button', { name: 'Thread' }));
  });

  it('offers no new thread in an archived room, but opens the ones it has, read only', () => {
    const archived = create(RoomSchema, { name: 'example-app', archivedBy: 'builder' });
    const withReplies = [
      message(40, 'builder', 'the export is broken', 30, {
        threadSummary: create(ThreadSummarySchema, { replies: 1, repliers: ['reviewer'] }),
      }),
    ];
    const onThread = vi.fn();
    const { container } = room(withReplies, { room: archived, onThread, thread: open(40n) });
    expect(within(tablet(container, 40)).queryByRole('button', { name: 'Thread' })).toBeNull();
    fireEvent.click(within(tablet(container, 40)).getByRole('button', { name: /Open the thread/ }));
    expect(onThread).toHaveBeenCalledWith(40n);
    expect(screen.queryByRole('combobox', { name: 'Reply in the thread #example-app' })).toBeNull();
  });

  it('offers the unread replies of its threads, which the room leaves out, and opens the next thread', () => {
    const onThread = vi.fn();
    room(ms, { onThread, threadUnread: 3, unreadThreads: [{ root: 30n, unread: 3 }] });
    fireEvent.click(screen.getByRole('button', { name: /3 unread in threads/ }));
    expect(onThread).toHaveBeenCalledWith(30n);
  });

  it('finds the next thread with unread replies among the listed ones and the loaded roots', () => {
    const withUnread = message(40, 'builder', 'x', 9, {
      threadSummary: create(ThreadSummarySchema, { replies: 2, unread: 1 }),
    });
    expect(nextUnreadThread([{ root: 50n, unread: 2 }], [withUnread], undefined)).toBe(40n);
    expect(nextUnreadThread([{ root: 50n, unread: 2 }], [withUnread], 40n)).toBe(50n);
    expect(nextUnreadThread([{ root: 50n, unread: 0 }], [], undefined)).toBeUndefined();
  });

  it('keeps Cancel on a scheduled reply in the thread of a room whose bridge was removed', () => {
    const waiting = [
      message(40, 'builder', 'root', 30),
      { ...goingOut(41, 'secretary', 'waiting', 20, 'scheduled'), room: 'example-app', thread: 40n },
    ];
    room(ms, { onThread: vi.fn(), thread: open(40n, waiting) });
    const panel = screen.getByRole('complementary', { name: 'Thread' });
    expect(within(panel).getByRole('button', { name: 'Cancel' })).toBeTruthy();
  });
});
