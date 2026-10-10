import { readFileSync } from 'node:fs';
import { create } from '@bufbuild/protobuf';
import { timestampFromDate } from '@bufbuild/protobuf/wkt';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { App, parseRoute } from './App';
import { boardAgents } from './board';
import { agent, lock, NOW, proposal, queue } from './fixtures';
import { GetCharterResponseSchema } from './gen/agora/v1/governance_pb';
import { MessageSchema, RoomSchema } from './gen/agora/v1/rooms_pb';
import { Board, Charter, RoomList, RoomView, Turns } from './views';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
  history.replaceState(null, '', '/');
});

const names = (container: HTMLElement) => [...container.querySelectorAll('.agent .n')].map((n) => n.textContent);

describe('Board', () => {
  const agents = [
    agent('builder', 'idle', { foundPrs: [57], prs: [58], ci: { 57: 'green', 58: 'red' } }),
    agent('reviewer', 'busy'),
    agent('docs-writer', 'offline', { project: 'website' }),
    agent('operator', 'offline'),
  ];

  it('lists agents busy first with helmets, CI icons and filter counts', () => {
    const { container } = render(<Board agents={boardAgents(agents, 'operator')} now={NOW} />);
    expect(names(container)).toEqual(['reviewer', 'builder', 'docs-writer']);
    expect(container.querySelector('.head .sum')?.textContent).toBe('1 busy · 1 idle · 1 offline');
    expect(container.querySelector('[data-agent="reviewer"] .ic.busy')).not.toBeNull();
    expect(container.querySelector('[data-agent="docs-writer"] .ic.offline')).not.toBeNull();
    const chips = [...container.querySelectorAll('.filters .chip')].map((c) => c.textContent);
    expect(chips).toEqual(['all 3', 'busy 1', 'idle 1', 'with PR 1']);
    const row = container.querySelector('[data-agent="builder"]') as HTMLElement;
    expect(within(row).getByText(/#57/).querySelector('.ci.green')).not.toBeNull();
    expect(within(row).getByText(/#58/).querySelector('.ci.red')).not.toBeNull();
  });

  it('narrows to agents with a pull request and to a project', () => {
    const { container, rerender } = render(<Board agents={boardAgents(agents, 'operator')} now={NOW} />);
    fireEvent.click(screen.getByText('with PR 1'));
    expect(names(container)).toEqual(['builder']);
    rerender(<Board agents={boardAgents(agents, 'operator')} now={NOW} project="website" />);
    fireEvent.click(screen.getByText('all 1'));
    expect(names(container)).toEqual(['docs-writer']);
  });
});

describe('Rooms', () => {
  const rooms = [
    create(RoomSchema, { name: 'example-app', messages: 12 }),
    create(RoomSchema, { name: 'general', messages: 3 }),
  ];

  it('shows mention counts as @n and marks followed rooms', () => {
    const unread = new Map([['example-app', { unread: 7, addressed: 2 }]]);
    const { container } = render(
      <RoomList rooms={rooms} unread={unread} followed={new Set(['general', 'example-app'])} />,
    );
    expect(container.querySelector('em.mention')?.textContent).toBe('@2');
    expect(container.querySelectorAll('.item.followed')).toHaveLength(2);
  });

  it('shows the board with an owl, sets mentions and own messages apart, posts and follows', async () => {
    const at = timestampFromDate(NOW);
    const messages = [
      create(MessageSchema, { id: 1n, room: 'example-app', author: 'agora', body: '@builder CI is green on #57.', at }),
      create(MessageSchema, { id: 2n, room: 'example-app', author: 'reviewer', body: 'thanks @builder', at }),
      create(MessageSchema, { id: 3n, room: 'example-app', author: 'operator', body: 'merging now', at }),
    ];
    const onPost = vi.fn(async () => {});
    const onFollow = vi.fn();
    const { container } = render(
      <RoomView
        room={rooms[0]}
        name="example-app"
        messages={messages}
        operator="operator"
        followed={false}
        now={NOW}
        onPost={onPost}
        onFollow={onFollow}
      />,
    );
    const sys = container.querySelector('.msg.sys') as HTMLElement;
    expect(sys.querySelector('.who svg title')?.textContent).toBe('the board');
    expect([...sys.querySelectorAll('.tx b')].map((b) => b.textContent)).toEqual(['@builder', '#57']);
    expect([...container.querySelectorAll('.msg.own .tx')].map((t) => t.textContent)).toEqual(['merging now']);
    const box = screen.getByLabelText('Message #example-app');
    fireEvent.change(box, { target: { value: '@builder please rebase' } });
    fireEvent.keyDown(box, { key: 'Enter' });
    await waitFor(() => expect(onPost).toHaveBeenCalledWith('@builder please rebase'));
    fireEvent.click(screen.getByText('follow'));
    expect(onFollow).toHaveBeenCalledWith(true);
  });
});

describe('General', () => {
  it('is always followed: its view offers no follow button', () => {
    const general = create(RoomSchema, { name: 'general' });
    render(
      <RoomView
        room={general}
        name="general"
        messages={[]}
        operator="operator"
        followed
        now={NOW}
        onPost={async () => {}}
        onFollow={() => {}}
      />,
    );
    expect(screen.queryByText('follow')).toBeNull();
    expect(screen.queryByText('following')).toBeNull();
  });
});

describe('Turns', () => {
  it('shows queues and locks', () => {
    const rs = [queue('heavy-tests', 2, [['builder', 17]], ['reviewer', 'docs-writer']), lock('example-app/merge')];
    const { container } = render(<Turns resources={rs} now={NOW} />);
    const slots = [...container.querySelectorAll('.slot')].map((s) => s.textContent);
    expect(slots).toEqual(['@builderholds · 17m left', '@reviewernext', '@docs-writer#2']);
    expect(container.querySelector('.lock')?.textContent).toBe('example-app/mergefree');
  });
});

describe('Charter', () => {
  it('shows pebbles for votes and the charter text', () => {
    const p = proposal(3, 'Merge only under the lock', 'open', [
      ['builder', 'yes'],
      ['reviewer', 'yes'],
      ['docs-writer', 'yes'],
      ['tester', 'no'],
    ]);
    const charter = create(GetCharterResponseSchema, { body: '# House rules\n\nBe kind.' });
    const { container } = render(<Charter charter={charter} proposals={[p]} now={NOW} />);
    expect(container.querySelectorAll('.pebbles i.yes')).toHaveLength(3);
    expect(container.querySelectorAll('.pebbles i.no')).toHaveLength(1);
    expect(screen.getByText('House rules').tagName).toBe('H3');
  });
});

describe('App', () => {
  it('routes by the fragment', () => {
    expect(parseRoute('#/')).toEqual({ view: 'board' });
    expect(parseRoute('#/rooms/example-app')).toEqual({ view: 'room', room: 'example-app' });
    expect(parseRoute('#/turns')).toEqual({ view: 'turns' });
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

describe('Board messages', () => {
  it('are marked with ›', () => {
    const css = readFileSync('src/styles.css', 'utf8');
    expect(css).toMatch(/\.msg\.sys \.tx::before \{\s*content: "› ";/);
  });
});

describe('Own messages', () => {
  it('are set apart by a rule', () => {
    const css = readFileSync('src/styles.css', 'utf8');
    expect(css).toMatch(/\.msg\.own \{\s*border-left: 2px solid var\(--line\);/);
  });
});

describe('Phone layout', () => {
  it('shows tabs instead of the sidebars at 520 pixels and below', () => {
    const css = readFileSync('src/styles.css', 'utf8'); // vitest runs in web/
    const phone = css.slice(css.indexOf('@container (max-width: 520px)'));
    expect(phone).toMatch(/\.side,\s*\.rail\s*\{\s*display: none;/);
    expect(phone).toMatch(/\.tabs\s*\{\s*display: flex;/);
  });
});
