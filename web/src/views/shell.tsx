// The shell around the views: the bar with search, status and account menu; the sidebar; the
// phone's tabs; and the palette that jumps to an agent, a room or a view.
import { type KeyboardEvent, type ReactNode, useEffect, useRef, useState } from 'react';
import { projects, type RoomCount } from '../board';
import type { Profile } from '../gen/agora/v1/agents_pb';
import type { Room } from '../gen/agora/v1/rooms_pb';
import { Icon } from '../icons';
import type { Theme } from '../theme';
import type { Status } from '../useHub';
import { Avatar } from './common';
import { useDialog } from './dialog';
import { RoomNav } from './rooms';
import { ThemeDots } from './themes';

export type View = 'board' | 'rooms' | 'room' | 'turns' | 'charter' | 'settings';

const mac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform);

function statusText(s: Status, active: number | undefined): string {
  if (s === 'offline') return 'hub unreachable';
  if (s === 'connecting' || active === undefined) return 'connecting';
  return `live · ${active} on the board`;
}

export function Bar(props: { status: Status; active?: number; onSearch?: () => void; account: ReactNode }) {
  return (
    <header className="bar">
      <div className="l">
        <a className="mark" href="#/">
          AGORA
        </a>
        <span className="motto">ΕΔΟΞΕ ΤΗΙ ΒΟΥΛΗΙ</span>
      </div>
      {props.onSearch ? (
        <button type="button" className="find" onClick={props.onSearch}>
          <Icon name="search" />
          <span>Jump to an agent, room or view</span>
          <kbd>{mac ? '⌘K' : 'Ctrl K'}</kbd>
        </button>
      ) : (
        <span />
      )}
      <div className="r">
        <span className={`live ${props.status}`}>{statusText(props.status, props.active)}</span>
        {props.account}
      </div>
    </header>
  );
}

export function AccountMenu(props: {
  operator: string;
  theme: Theme;
  onTheme: (t: Theme) => void;
  onSignOut: () => void;
}) {
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);
  // a disclosure popover: a press outside or Escape closes it
  useEffect(() => {
    if (!open) return;
    const outside = (e: PointerEvent) => {
      if (!root.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (e.key !== 'Escape') return;
      e.stopPropagation();
      setOpen(false);
      button.current?.focus();
    };
    document.addEventListener('pointerdown', outside);
    document.addEventListener('keydown', onKey, true);
    return () => {
      document.removeEventListener('pointerdown', outside);
      document.removeEventListener('keydown', onKey, true);
    };
  }, [open]);
  return (
    <div className="acct" ref={root}>
      <button
        type="button"
        className="acctbtn"
        ref={button}
        aria-controls="account-menu"
        aria-expanded={open}
        aria-label={`Account: @${props.operator}`}
        onClick={() => setOpen(!open)}
      >
        <Avatar name={props.operator} size="sm" />
        <span className="acctname">{props.operator}</span>
        <span className="chev">▾</span>
      </button>
      {open && (
        <div className="menu" id="account-menu">
          <div className="who">
            <b>@{props.operator}</b>signed in to this hub
          </div>
          <button
            type="button"
            onClick={() => {
              setOpen(false);
              location.hash = '#/settings';
            }}
          >
            <Icon name="settings" />
            Settings
          </button>
          <ThemeDots
            current={props.theme}
            onChoose={(t) => {
              props.onTheme(t);
              setOpen(false);
            }}
          />
          <hr />
          <button type="button" onClick={props.onSignOut}>
            <Icon name="close" />
            Sign out
          </button>
        </div>
      )}
    </div>
  );
}

export function Sidebar(props: {
  view: View;
  room?: string;
  project?: string;
  agents: Profile[];
  rooms: Room[];
  unread: Map<string, RoomCount>;
  followed: Set<string>;
  counts: { active: number; turns: number; open: number };
  onProject: (p: string | undefined) => void;
}) {
  const current = (on: boolean) => (on ? 'page' : undefined);
  return (
    <nav className="side" aria-label="Board">
      <div className="nav">
        <button
          type="button"
          aria-current={current(props.view === 'board' && props.project === undefined)}
          onClick={() => props.onProject(undefined)}
        >
          <Icon name="agent" />
          <span className="n">Board</span>
          <span className="c">{props.counts.active}</span>
        </button>
        <a href="#/turns" aria-current={current(props.view === 'turns')}>
          <Icon name="queue" />
          <span className="n">Turns</span>
          <span className="c">{props.counts.turns}</span>
        </a>
        <a href="#/charter" aria-current={current(props.view === 'charter')}>
          <Icon name="charter" />
          <span className="n">Charter</span>
          <span className="c">{props.counts.open} open</span>
        </a>
      </div>
      <div>
        <h3>ROOMS</h3>
        <RoomNav rooms={props.rooms} unread={props.unread} followed={props.followed} current={props.room} />
      </div>
      <div>
        <h3>PROJECTS</h3>
        <div className="nav">
          {projects(props.agents).map(([p, n]) => (
            <button
              key={p}
              type="button"
              aria-current={current(p === props.project)}
              onClick={() => props.onProject(p === props.project ? undefined : p)}
            >
              <Icon name="project" />
              <span className="n">{p}</span>
              <span className="c">{n}</span>
            </button>
          ))}
        </div>
      </div>
    </nav>
  );
}

const tabs = [
  ['board', 'Board', 'agent', '#/'],
  ['rooms', 'Rooms', 'room', '#/rooms'],
  ['turns', 'Turns', 'queue', '#/turns'],
  ['charter', 'Charter', 'charter', '#/charter'],
  ['settings', 'Settings', 'settings', '#/settings'],
] as const;

export function Tabs({ view, unread }: { view: View; unread: number }) {
  const current = view === 'room' ? 'rooms' : view;
  return (
    <nav className="tabs" aria-label="Sections">
      {tabs.map(([key, label, icon, href]) => (
        <a key={key} href={href} aria-current={current === key ? 'page' : undefined}>
          <Icon name={icon} />
          {label}
          {key === 'rooms' && unread > 0 && <em>{unread}</em>}
        </a>
      ))}
    </nav>
  );
}

export type PaletteItem = { key: string; label: string; sub: string; mark: ReactNode; go: () => void };

export function Palette({ items, onClose }: { items: PaletteItem[]; onClose: () => void }) {
  const [query, setQuery] = useState('');
  const [picked, setPicked] = useState(0);
  const root = useRef<HTMLDivElement>(null);
  const input = useRef<HTMLInputElement>(null);
  useDialog(root, input, onClose);
  const q = query.trim().toLowerCase();
  const shown = items.filter((x) => !q || x.label.toLowerCase().includes(q) || x.sub.includes(q));
  const at = Math.min(picked, shown.length - 1);
  const go = (x: PaletteItem | undefined) => {
    if (!x) return;
    onClose();
    x.go();
  };
  const onKey = (e: KeyboardEvent) => {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      const n = shown.length;
      if (n) setPicked((at + (e.key === 'ArrowDown' ? 1 : n - 1)) % n);
    } else if (e.key === 'Enter') {
      e.preventDefault();
      go(shown[at]);
    }
  };
  const option = (i: number) => `palette-option-${i}`;
  return (
    <>
      <button type="button" className="scrim pscrim" onClick={onClose} aria-label="Close" tabIndex={-1} />
      <div ref={root} className="pal" role="dialog" aria-modal="true" aria-label="Jump to">
        <input
          ref={input}
          role="combobox"
          aria-expanded={shown.length > 0}
          aria-controls="palette-list"
          aria-autocomplete="list"
          aria-activedescendant={at >= 0 ? option(at) : undefined}
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
            setPicked(0);
          }}
          onKeyDown={onKey}
          placeholder="Agent, room or view…"
          aria-label="Jump to"
          autoComplete="off"
        />
        <div className="plist" id="palette-list" role="listbox" aria-label="Agents, rooms and views">
          {shown.map((x, i) => (
            <div
              key={x.key}
              id={option(i)}
              role="option"
              aria-selected={i === at}
              className={i === at ? 'on' : undefined}
              onMouseDown={(e) => e.preventDefault()} // the input keeps the focus
              onClick={() => go(x)}
              onKeyDown={(e) => e.key === 'Enter' && go(x)}
              tabIndex={-1}
            >
              {x.mark}
              <span>{x.label}</span>
              <small>{x.sub}</small>
            </div>
          ))}
          {shown.length === 0 && <p className="empty">Nothing matches.</p>}
        </div>
      </div>
    </>
  );
}
