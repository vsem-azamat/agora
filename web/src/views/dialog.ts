// Modal dialogs (the drawer and the palette): focus moves in when one opens and back when it
// closes, Tab stays inside, and Escape closes only the topmost one.
import { type RefObject, useEffect, useRef } from 'react';

const open: object[] = [];

const focusable = 'a[href], button:not([disabled]), input, textarea, [tabindex]:not([tabindex="-1"])';

export function useDialog(
  root: RefObject<HTMLElement | null>,
  first: RefObject<HTMLElement | null>,
  onClose: () => void,
) {
  const close = useRef(onClose);
  close.current = onClose;
  useEffect(() => {
    const me = {};
    open.push(me);
    const before = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    first.current?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (open.at(-1) !== me) return;
      if (e.key === 'Escape') {
        e.stopPropagation();
        close.current();
      } else if (e.key === 'Tab' && root.current) {
        const all = [...root.current.querySelectorAll<HTMLElement>(focusable)];
        const [head, tail] = [all[0], all.at(-1)];
        if (!head || !tail) return;
        const inside = root.current.contains(document.activeElement);
        if (e.shiftKey && (document.activeElement === head || !inside)) {
          e.preventDefault();
          tail.focus();
        } else if (!e.shiftKey && (document.activeElement === tail || !inside)) {
          e.preventDefault();
          head.focus();
        }
      }
    };
    // capture: the topmost dialog acts before anything under it
    document.addEventListener('keydown', onKey, true);
    return () => {
      document.removeEventListener('keydown', onKey, true);
      open.splice(open.indexOf(me), 1);
      before?.focus();
    };
  }, [root, first]);
}
