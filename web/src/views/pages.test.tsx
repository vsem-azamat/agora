import { readFileSync } from 'node:fs';
import { create } from '@bufbuild/protobuf';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { boardAgents } from '../board';
import { agent, ci, lock, message, NOW, proposal, queue } from '../fixtures';
import { GetCharterResponseSchema } from '../gen/agora/v1/governance_pb';
import { RoomSchema } from '../gen/agora/v1/rooms_pb';
import { Board } from './board';
import { Charter } from './charter';
import { OpenAgent } from './common';
import { addressRoom, Drawer } from './drawer';
import { Settings } from './settings';
import { ThemeCards } from './themes';
import { Turns } from './turns';

afterEach(() => {
  cleanup();
  localStorage.clear();
  document.documentElement.removeAttribute('data-theme');
});

const names = (container: HTMLElement) => [...container.querySelectorAll('.agent .n')].map((n) => n.textContent);

describe('Board', () => {
  const agents = [
    agent('builder', 'idle', { foundPrs: [57], prs: [58], ciState: ci({ 57: 'green', 58: 'red' }) }),
    agent('reviewer', 'busy'),
    agent('docs-writer', 'offline', { project: 'website' }),
    agent('operator', 'offline'),
  ];

  it('lists agents busy first with liveness, CI icons and filter counts', () => {
    const { container } = render(<Board agents={boardAgents(agents, 'operator')} now={NOW} />);
    expect(names(container)).toEqual(['reviewer', 'builder', 'docs-writer']);
    expect(container.querySelector('.head .sum')?.textContent).toBe('1 busy · 1 idle · 1 offline');
    expect(container.querySelector('[data-agent="reviewer"] .av')?.getAttribute('data-state')).toBe('busy');
    expect(container.querySelector('[data-agent="docs-writer"] .av')?.getAttribute('data-state')).toBe('offline');
    const chips = [...container.querySelectorAll('.chips .chip')].map((c) => c.textContent);
    expect(chips).toEqual(['all 3', 'busy 1', 'idle 1', 'with PR 1']);
    const row = container.querySelector('[data-agent="builder"]') as HTMLElement;
    expect(within(row).getByText(/#57/).closest('.ci')?.classList).toContain('green');
    expect(within(row).getByText(/#58/).closest('.ci')?.classList).toContain('red');
  });

  it('narrows to agents with a pull request and to a project', () => {
    const { container, rerender } = render(<Board agents={boardAgents(agents, 'operator')} now={NOW} />);
    fireEvent.click(screen.getByText('with PR 1'));
    expect(names(container)).toEqual(['builder']);
    rerender(<Board agents={boardAgents(agents, 'operator')} now={NOW} project="website" />);
    fireEvent.click(screen.getByText('all 1'));
    expect(names(container)).toEqual(['docs-writer']);
  });

  it('opens an agent’s drawer, in the same pigment', () => {
    const open = vi.fn();
    const { container } = render(
      <OpenAgent.Provider value={open}>
        <Board agents={boardAgents(agents, 'operator')} now={NOW} />
      </OpenAgent.Provider>,
    );
    fireEvent.click(container.querySelector('[data-agent="builder"]') as HTMLElement);
    expect(open).toHaveBeenCalledWith('builder');
    const pigment = (container.querySelector('[data-agent="builder"] .av') as HTMLElement).style.getPropertyValue(
      '--pg',
    );
    cleanup();
    const drawer = render(
      <Drawer
        name="builder"
        agent={agents[0]}
        operator="operator"
        rooms={[]}
        general="general"
        known={new Set()}
        now={NOW}
        onClose={() => {}}
        onAddress={() => {}}
        onGoto={() => {}}
      />,
    );
    expect((drawer.container.querySelector('.dhead .av') as HTMLElement).style.getPropertyValue('--pg')).toBe(pigment);
  });
});

describe('Drawer', () => {
  const builder = agent('builder', 'busy', {
    kind: 'claude-code',
    branch: 'fix/address',
    prs: [57],
    ciState: ci({ 57: 'green' }),
    task: 'fixing address validation',
  });
  const rooms = [create(RoomSchema, { name: 'example-app' }), create(RoomSchema, { name: 'general' })];

  it('shows what the agent does now and what it wrote lately', () => {
    const onGoto = vi.fn();
    const onAddress = vi.fn();
    const onClose = vi.fn();
    const { container } = render(
      <Drawer
        name="builder"
        agent={builder}
        operator="operator"
        rooms={rooms}
        general="general"
        recent={[message(9, 'builder', 'PR #57 is up', 4)]}
        known={new Set(['builder'])}
        now={NOW}
        onClose={onClose}
        onAddress={onAddress}
        onGoto={onGoto}
      />,
    );
    expect(container.querySelector('.meta')?.textContent).toBe('claude-code · busy · updated 3m ago');
    expect(screen.getByText('fixing address validation')).toBeTruthy();
    expect(screen.getByText('fix/address')).toBeTruthy();
    expect(container.querySelector('.kvs .ci.green')?.textContent).toContain('#57');
    fireEvent.click(container.querySelector('.recent button') as HTMLElement);
    expect(onGoto).toHaveBeenCalledWith('example-app', 9n);
    fireEvent.click(screen.getByText(/Address in #example-app/));
    expect(onAddress).toHaveBeenCalledWith('example-app');
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(onClose).toHaveBeenCalled();
  });

  it('addresses an agent without a project room in the general room', () => {
    expect(addressRoom(agent('scout', 'idle', { project: 'other-app' }), rooms, 'general')).toBe('general');
    expect(addressRoom(builder, rooms, 'general')).toBe('example-app');
  });
});

describe('Turns', () => {
  it('shows queues and locks', () => {
    const rs = [queue('heavy-tests', 2, [['builder', 17]], ['reviewer', 'docs-writer']), lock('example-app/merge')];
    const { container } = render(<Turns resources={rs} now={NOW} />);
    const slots = [...container.querySelectorAll('[data-queue] .slot')].map((s) => s.textContent);
    expect(slots).toEqual(['builderholds · 17m left', 'free slot', 'reviewernext', 'docs-writer#2']);
    expect(container.querySelector('[data-lock="example-app/merge"]')?.textContent).toBe(' example-app/mergefree');
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
    expect(screen.getByText('House rules').tagName).toBe('H4');
    expect(container.querySelector('.prop .tag')?.textContent).toBe('OPEN');
  });
});

describe('Themes', () => {
  it('previews a theme while pointed at and stores only a choice', () => {
    const onChoose = vi.fn();
    render(<ThemeCards current="parchment" onChoose={onChoose} />);
    const patina = screen.getByText('Patina');
    fireEvent.pointerEnter(patina);
    expect(document.documentElement.dataset.theme).toBe('patina');
    fireEvent.pointerLeave(patina);
    expect(document.documentElement.dataset.theme).toBe('parchment');
    expect(localStorage.length).toBe(0);
    fireEvent.click(patina);
    expect(onChoose).toHaveBeenCalledWith('patina');
  });
});

describe('Settings', () => {
  it('shows the name, Sign out and how to rotate the token', () => {
    const onSignOut = vi.fn();
    const { container } = render(
      <Settings
        operator="operator"
        theme="parchment"
        onTheme={() => {}}
        size="m"
        onSize={() => {}}
        notify="mentions"
        onNotify={() => {}}
        onSignOut={onSignOut}
      />,
    );
    expect(screen.getByText('@operator')).toBeTruthy();
    expect(container.querySelector('code')?.textContent).toBe('agora web token --rotate');
    fireEvent.click(screen.getByText('Sign out'));
    expect(onSignOut).toHaveBeenCalled();
    expect([...container.querySelectorAll('.theme')].map((t) => t.textContent)).toEqual([
      'AaParchment',
      'AaMarble',
      'AaIvory',
      'AaInk',
      'AaPatina',
    ]);
  });
});

describe('Phone layout', () => {
  it('shows tabs instead of the sidebar at 760 pixels and below', () => {
    const css = readFileSync('src/styles.css', 'utf8'); // vitest runs in web/
    const phone = css.slice(css.indexOf('@media (max-width: 760px)'));
    expect(phone).toMatch(/\.side,/);
    expect(phone).toMatch(/\.tabs \{[^}]*display: grid;/);
  });
});
