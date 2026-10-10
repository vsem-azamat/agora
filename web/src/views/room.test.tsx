import { create } from '@bufbuild/protobuf';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { ComponentProps } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { agent, message, NOW } from '../fixtures';
import type { Message } from '../gen/agora/v1/rooms_pb';
import { RoomSchema } from '../gen/agora/v1/rooms_pb';
import type { Saved } from '../tape';
import { RoomView } from './room';
import { RoomNav } from './rooms';

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

function room(messages: Message[] | undefined, more: Partial<Props> = {}) {
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
    memory: new Map<string, Saved>(),
    onArrived: vi.fn(),
    ...more,
  };
  return { props, ...render(<RoomView {...props} />) };
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

  it('follows with a mode and stops following, except the general room', () => {
    const { props } = room(ms, { followed: false });
    const follow = screen.getByRole('combobox', { name: 'Follow' }) as HTMLSelectElement;
    expect(follow.value).toBe('none');
    fireEvent.change(follow, { target: { value: 'wake' } });
    expect(props.onFollow).toHaveBeenCalledWith(true, 'wake');
    cleanup();
    const general = room([], { name: 'general', mode: 'mentions' });
    const select = screen.getByRole('combobox', { name: 'Follow' }) as HTMLSelectElement;
    expect(select.value).toBe('mentions');
    expect([...select.options].map((o) => o.text)).toEqual([
      'Every message',
      'Mentions only',
      'Every message notifies',
    ]);
    fireEvent.change(select, { target: { value: 'all' } });
    expect(general.props.onFollow).toHaveBeenCalledWith(true, 'all');
  });

  it('stops following', () => {
    const { props } = room(ms, { mode: 'all' });
    fireEvent.change(screen.getByRole('combobox', { name: 'Follow' }), { target: { value: 'none' } });
    expect(props.onFollow).toHaveBeenCalledWith(false);
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
