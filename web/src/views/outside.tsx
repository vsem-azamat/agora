// Someone outside Agora who wrote through a bridge: a neutral mark with the first letter of the
// name, and the name with its bridge, which opens a small card instead of an agent's drawer.
import { type CSSProperties, type ReactNode, useEffect, useId, useRef, useState } from 'react';
import { initial } from '../bridges';
import type { ExternalAuthor } from '../gen/agora/v1/rooms_pb';

/** The neutral pigment of people outside, for `style`. */
export const OUTSIDE: CSSProperties = { '--pg': 'var(--mute)' } as CSSProperties;

export function OutsiderAvatar({ who, size }: { who: ExternalAuthor; size?: 'sm' }) {
  return (
    <span className={size ? `av ext ${size}` : 'av ext'} aria-hidden="true">
      <span className="l">{initial(who.name || who.id)}</span>
    </span>
  );
}

/**
 * The name outside, by default with its bridge (`Ada · example-chat`); choosing it shows who it
 * is there. `children` replace the name on the button, as in the room's people.
 */
export function OutsiderName({
  who,
  className = 'au',
  via = true,
  children,
}: {
  who: ExternalAuthor;
  className?: string;
  via?: boolean;
  children?: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLSpanElement>(null);
  const button = useRef<HTMLButtonElement>(null);
  const id = useId();
  const name = who.name || who.id;
  // a disclosure popover: a press outside or Escape closes it
  useEffect(() => {
    if (!open) return;
    const outside = (e: PointerEvent) => {
      if (!root.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
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
    <span className="outsider" ref={root}>
      <button
        type="button"
        className={className}
        ref={button}
        aria-expanded={open}
        aria-controls={id}
        onClick={() => setOpen(!open)}
      >
        {children ?? name}
      </button>
      {via && <span className="via"> · {who.bridge}</span>}
      {open && (
        <span className="card" id={id}>
          <b>{name}</b>
          <span>outside Agora, through {who.bridge}</span>
          {who.id && <span className="xid">id there: {who.id}</span>}
        </span>
      )}
    </span>
  );
}
