// Following a room from its header: a toggle to follow or stop, and a menu for the mode. The
// mode changes only when the operator picks one, never while moving through the menu.
import { type KeyboardEvent, useEffect, useRef, useState } from 'react';
import type { Mode } from '../board';

export const MODE_LABELS: Record<Mode, string> = {
  all: 'Every message',
  mentions: 'Mentions only',
  wake: 'Every message notifies',
};

const MODES = Object.keys(MODE_LABELS) as Mode[];

export function FollowControls(props: {
  room: string;
  /** The general room is always followed: it has no toggle. */
  always: boolean;
  followed: boolean;
  mode: Mode;
  onFollow: (follow: boolean, mode?: Mode) => void;
}) {
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);
  const items = useRef<(HTMLButtonElement | null)[]>([]);
  const menuId = `follow-mode-${props.room}`;

  // a press outside or Escape closes the menu; opening it focuses the current mode
  useEffect(() => {
    if (!open) return;
    items.current[MODES.indexOf(props.mode)]?.focus();
    const outside = (e: PointerEvent) => {
      if (!root.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener('pointerdown', outside);
    return () => document.removeEventListener('pointerdown', outside);
  }, [open, props.mode]);

  const close = () => {
    setOpen(false);
    button.current?.focus();
  };
  const onKey = (e: KeyboardEvent) => {
    const at = items.current.indexOf(document.activeElement as HTMLButtonElement);
    const step = { ArrowDown: 1, ArrowUp: -1 }[e.key];
    if (step !== undefined) {
      e.preventDefault();
      items.current[(at + step + MODES.length) % MODES.length]?.focus();
    } else if (e.key === 'Home' || e.key === 'End') {
      e.preventDefault();
      items.current[e.key === 'Home' ? 0 : MODES.length - 1]?.focus();
    } else if (e.key === 'Escape' || e.key === 'Tab') {
      e.stopPropagation();
      if (e.key === 'Escape') e.preventDefault();
      close();
    }
  };
  const choose = (m: Mode) => {
    close();
    if (m !== props.mode) props.onFollow(true, m);
  };

  return (
    <div className="follow" ref={root}>
      {!props.always && (
        <button
          type="button"
          className="chip"
          aria-pressed={props.followed}
          onClick={() => props.onFollow(!props.followed)}
        >
          {props.followed ? 'following' : 'follow'}
        </button>
      )}
      {props.followed && (
        <button
          type="button"
          className="chip"
          ref={button}
          aria-label={`Follow mode: ${MODE_LABELS[props.mode]}`}
          aria-haspopup="menu"
          aria-controls={menuId}
          aria-expanded={open}
          onClick={() => setOpen(!open)}
          onKeyDown={(e) => {
            if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
              e.preventDefault();
              setOpen(true);
            }
          }}
        >
          {MODE_LABELS[props.mode]}
          <span className="chev">▾</span>
        </button>
      )}
      {open && (
        <div className="menu" id={menuId} role="menu" aria-label="Follow mode" tabIndex={-1} onKeyDown={onKey}>
          {MODES.map((m, i) => (
            <button
              key={m}
              type="button"
              role="menuitemradio"
              aria-checked={m === props.mode}
              tabIndex={-1}
              ref={(el) => {
                items.current[i] = el;
              }}
              onClick={() => choose(m)}
            >
              <span className="check" aria-hidden="true">
                {m === props.mode ? '●' : ''}
              </span>
              {MODE_LABELS[m]}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
