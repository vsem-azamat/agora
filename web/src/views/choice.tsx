// A chip that opens a menu of choices, one of them current: the follow mode and a bridge's
// outbound policy. The value changes only when the operator picks a choice, never while moving
// through the menu.
import { type KeyboardEvent, useEffect, useRef, useState } from 'react';

export function ChoiceMenu<T extends string>(props: {
  /** What is chosen, such as `Follow mode`; it names the menu and labels the chip. */
  name: string;
  /** The chip also shows the name in front of the choice, where there is room. */
  shown?: boolean;
  /** Unique on the page: the menu's id is built from it. */
  id: string;
  labels: Record<T, string>;
  value: T;
  onChoose: (v: T) => void;
}) {
  const choices = Object.keys(props.labels) as T[];
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);
  const items = useRef<(HTMLButtonElement | null)[]>([]);
  const at = choices.indexOf(props.value);

  // a press outside closes the menu; opening it focuses the current choice
  useEffect(() => {
    if (!open) return;
    items.current[at]?.focus();
    const outside = (e: PointerEvent) => {
      if (!root.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener('pointerdown', outside);
    return () => document.removeEventListener('pointerdown', outside);
  }, [open, at]);

  const close = () => {
    setOpen(false);
    button.current?.focus();
  };
  const onKey = (e: KeyboardEvent) => {
    const now = items.current.indexOf(document.activeElement as HTMLButtonElement);
    const step = { ArrowDown: 1, ArrowUp: -1 }[e.key];
    if (step !== undefined) {
      e.preventDefault();
      items.current[(now + step + choices.length) % choices.length]?.focus();
    } else if (e.key === 'Home' || e.key === 'End') {
      e.preventDefault();
      items.current[e.key === 'Home' ? 0 : choices.length - 1]?.focus();
    } else if (e.key === 'Escape' || e.key === 'Tab') {
      e.stopPropagation();
      if (e.key === 'Escape') e.preventDefault();
      close();
    }
  };
  const choose = (v: T) => {
    close();
    if (v !== props.value) props.onChoose(v);
  };

  return (
    <div className="choice" ref={root}>
      <button
        type="button"
        className="chip"
        ref={button}
        aria-label={`${props.name}: ${props.labels[props.value]}`}
        aria-haspopup="menu"
        aria-controls={props.id}
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        onKeyDown={(e) => {
          if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
            e.preventDefault();
            setOpen(true);
          }
        }}
      >
        {props.shown && <span className="pre">{props.name}</span>}
        {props.labels[props.value]}
        <span className="chev">▾</span>
      </button>
      {open && (
        <div className="menu" id={props.id} role="menu" aria-label={props.name} tabIndex={-1} onKeyDown={onKey}>
          {choices.map((v, i) => (
            <button
              key={v}
              type="button"
              role="menuitemradio"
              aria-checked={v === props.value}
              tabIndex={-1}
              ref={(el) => {
                items.current[i] = el;
              }}
              onClick={() => choose(v)}
            >
              <span className="check" aria-hidden="true">
                {v === props.value ? '●' : ''}
              </span>
              {props.labels[v]}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
