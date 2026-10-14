import { create } from '@bufbuild/protobuf';
import { timestampFromDate } from '@bufbuild/protobuf/wkt';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { parseRoute } from '../App';
import { knownNames } from '../board';
import { agent, message, NOW } from '../fixtures';
import { RoomSchema, ThreadSchema, ThreadSummarySchema } from '../gen/agora/v1/rooms_pb';
import { RoomNav } from './rooms';
import { MAX_TITLE, participants, ThreadPanel, ThreadStrip } from './thread';

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
const known = knownNames(agents, 'operator');

function panel(more: Partial<Parameters<typeof ThreadPanel>[0]> = {}) {
  const props = {
    room: 'example-app',
    root: 40n,
    messages: [
      message(40, 'builder', 'the export is broken', 30),
      message(41, 'reviewer', 'which one?', 20, { thread: 40n }),
    ],
    operator: 'operator',
    board: 'agora',
    agents,
    known,
    onClose: vi.fn(),
    onPost: vi.fn(async () => {}),
    onTitle: vi.fn(),
    onLeave: vi.fn(),
    onSeen: vi.fn(),
    ...more,
  };
  return { ...render(<ThreadPanel {...props} />), props };
}

describe('the thread panel', () => {
  it('closes with Escape, but not from a text field, and the room does not hear it', () => {
    const { props } = panel();
    const room = vi.fn();
    document.addEventListener('keydown', room);
    const box = screen.getByRole('combobox', { name: 'Reply in the thread #example-app' });
    fireEvent.change(box, { target: { value: 'a long reply' } });
    fireEvent.keyDown(box, { key: 'Escape' });
    expect(props.onClose).not.toHaveBeenCalled();
    expect((box as HTMLTextAreaElement).value).toBe('a long reply');
    fireEvent.keyDown(screen.getByRole('button', { name: 'Leave' }), { key: 'Escape' });
    expect(props.onClose).toHaveBeenCalledOnce();
    expect(room).not.toHaveBeenCalled();
    document.removeEventListener('keydown', room);
  });

  it('on a phone, takes the focus to its back arrow, which goes back to the room', () => {
    vi.stubGlobal('matchMedia', (q: string) => ({ matches: q.includes('max-width: 760px') }));
    const { props } = panel();
    const backTo = screen.getByRole('button', { name: 'Back to the room' });
    expect(document.activeElement).toBe(backTo);
    fireEvent.click(backTo);
    expect(props.onClose).toHaveBeenCalledOnce();
    vi.unstubAllGlobals();
  });

  it('is read only in an archived room', () => {
    panel({ archived: true });
    expect(screen.queryByRole('combobox')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Leave' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Give a title' })).toBeNull();
    expect(screen.getByText(/read only/)).toBeTruthy();
    expect(screen.getByText('which one?')).toBeTruthy();
  });

  it('takes a title of up to the hub’s 100 characters, counting an emoji as one', () => {
    const { props } = panel();
    fireEvent.click(screen.getByRole('button', { name: 'Give a title' }));
    const input = screen.getByLabelText('Thread title') as HTMLInputElement;
    fireEvent.change(input, { target: { value: '🚀'.repeat(MAX_TITLE + 5) } });
    expect([...input.value]).toHaveLength(MAX_TITLE);
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(props.onTitle).toHaveBeenCalledWith('🚀'.repeat(MAX_TITLE));
  });

  it('stays where the operator scrolled up to when a reply comes, and follows at the bottom', () => {
    const { props, rerender } = panel();
    const list = document.querySelector('.tmsgs') as HTMLOListElement;
    Object.defineProperty(list, 'scrollHeight', { configurable: true, value: 1000 });
    Object.defineProperty(list, 'clientHeight', { configurable: true, value: 300 });
    list.scrollTop = 100;
    fireEvent.scroll(list);
    const more = [...props.messages, message(42, 'release', 'the CSV one', 5, { thread: 40n })];
    rerender(<ThreadPanel {...props} messages={more} />);
    expect(list.scrollTop).toBe(100);
    list.scrollTop = 700;
    fireEvent.scroll(list);
    rerender(<ThreadPanel {...props} messages={[...more, message(43, 'builder', 'ok', 1, { thread: 40n })]} />);
    expect(list.scrollTop).toBe(1000);
  });
});

describe('threads in a room', () => {
  it('shows a strip with the replies, who wrote them, the last time and the unread, and opens the thread', () => {
    const last = new Date(NOW.getTime() - 10 * 60_000);
    const root = message(40, 'builder', 'the export is broken', 30, {
      threadSummary: create(ThreadSummarySchema, {
        replies: 3,
        repliers: ['reviewer', 'release'],
        lastAt: timestampFromDate(last),
        unread: 2,
      }),
    });
    const onOpen = vi.fn();
    render(<ThreadStrip m={root} onOpen={onOpen} />);
    // a screen reader hears the whole strip: who replied, when and what is new, not only the count
    const strip = screen.getByRole('button', {
      name: /^Open the thread: 3 replies from reviewer, release, the last at \d\d:\d\d, 2 new$/,
    });
    expect(strip.textContent).toContain('3 replies');
    expect(strip.textContent).toContain('reviewer, release');
    expect(strip.textContent).toContain('2 new');
    expect(strip.textContent).toContain(
      `${String(last.getHours()).padStart(2, '0')}:${String(last.getMinutes()).padStart(2, '0')}`,
    );
    fireEvent.click(strip);
    expect(onOpen).toHaveBeenCalledWith(40n);
  });

  it('shows no strip for a message without replies', () => {
    const { container } = render(<ThreadStrip m={message(40, 'builder', 'x', 1)} onOpen={vi.fn()} />);
    expect(container.textContent).toBe('');
  });

  it('says whom a reply wakes: the participants other than the operator', () => {
    const msgs = [
      message(40, 'builder', 'the export is broken', 30),
      message(41, 'operator', '@release can you look?', 20, { thread: 40n }),
      message(42, 'reviewer', 'on it', 10, { thread: 40n }),
    ];
    expect(participants(msgs, known, 'agora', 'operator')).toEqual(['builder', 'release', 'reviewer']);
  });

  it('posts a reply in the thread, also to the room when asked, and titles it', async () => {
    const onPost = vi.fn(async () => {});
    const onTitle = vi.fn();
    const onSeen = vi.fn();
    render(
      <ThreadPanel
        room="example-app"
        root={40n}
        messages={[
          message(40, 'builder', 'the export is broken', 30),
          message(41, 'reviewer', 'which one?', 20, { thread: 40n }),
        ]}
        operator="operator"
        board="agora"
        agents={agents}
        known={known}
        onClose={vi.fn()}
        onPost={onPost}
        onTitle={onTitle}
        onLeave={vi.fn()}
        onSeen={onSeen}
      />,
    );
    expect(onSeen).toHaveBeenCalledWith(41n);
    expect(screen.getByText(/A reply wakes/).textContent).toContain('builder, reviewer');
    fireEvent.click(screen.getByLabelText('Also to the room'));
    const box = screen.getByRole('combobox', { name: 'Reply in the thread #example-app' });
    fireEvent.change(box, { target: { value: 'the CSV one' } });
    fireEvent.keyDown(box, { key: 'Enter' });
    await waitFor(() => expect(onPost).toHaveBeenCalledWith('the CSV one', true));
    fireEvent.click(screen.getByRole('button', { name: 'Give a title' }));
    fireEvent.change(screen.getByLabelText('Thread title'), { target: { value: 'Release 2.4' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(onTitle).toHaveBeenCalledWith('Release 2.4');
  });

  it('lists a titled thread under its room, with its unread count and its own address', () => {
    render(
      <RoomNav
        rooms={[create(RoomSchema, { name: 'example-app' })]}
        unread={new Map()}
        followed={new Set()}
        threads={[create(ThreadSchema, { root: 40n, room: 'example-app', title: 'Release 2.4', unread: 2 })]}
      />,
    );
    const link = screen.getByRole('link', { name: /Release 2.4/ });
    expect(link.getAttribute('href')).toBe('#/rooms/example-app/thread/40');
    expect(link.textContent).toContain('2');
    const links = screen.getAllByRole('link');
    expect(links.map((l) => l.getAttribute('href'))).toEqual(['#/rooms/example-app', '#/rooms/example-app/thread/40']);
  });

  it('marks the open thread as the current place in the room list', () => {
    render(
      <RoomNav
        rooms={[create(RoomSchema, { name: 'example-app' })]}
        unread={new Map()}
        followed={new Set()}
        current="example-app"
        currentThread={40n}
        threads={[create(ThreadSchema, { root: 40n, room: 'example-app', title: 'Release 2.4' })]}
      />,
    );
    expect(screen.getByRole('link', { name: /Release 2.4/ }).getAttribute('aria-current')).toBe('page');
    expect(screen.getByRole('link', { name: /#example-app/ }).getAttribute('aria-current')).toBe('true');
  });

  it('opens a room with its thread from the address', () => {
    expect(parseRoute('#/rooms/example-app/thread/40')).toEqual({ view: 'room', room: 'example-app', thread: 40n });
    expect(parseRoute('#/rooms/example-app')).toEqual({ view: 'room', room: 'example-app' });
  });
});
