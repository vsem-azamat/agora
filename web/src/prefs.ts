// Choices kept in this browser: theme, text size and notifications. Only an explicit choice is
// stored; without one each falls back to its default.
import { useState } from 'react';

export function readPref(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

export function writePref(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch {
    // storage may be off; the choice then lasts for this page only
  }
}

/**
 * A stored choice: its current value, resolved from storage, and a setter that stores it and
 * passes it to `apply`. Applying the stored value when the page loads is main.tsx's job.
 */
export function useChoice<T extends string>(
  key: string,
  resolve: (stored: string | null) => T,
  apply?: (value: T) => void,
): [T, (value: T) => void] {
  const [value, setValue] = useState(() => resolve(readPref(key)));
  const choose = (v: T) => {
    writePref(key, v);
    apply?.(v);
    setValue(v);
  };
  return [value, choose];
}

// --- text size ---------------------------------------------------------------------------

export const SIZE_KEY = 'agora.size';

export const SIZES = [
  ['s', 'Small'],
  ['m', 'Medium'],
  ['l', 'Large'],
] as const;

export type TextSize = (typeof SIZES)[number][0];

export function resolveSize(stored: string | null): TextSize {
  return SIZES.find(([s]) => s === stored)?.[0] ?? 'm';
}

/** Sets the text size on the page; styles.css sizes the text from `data-size`. */
export function applySize(s: TextSize) {
  document.documentElement.dataset.size = s;
}

// --- notifications -----------------------------------------------------------------------

export const NOTIFY_KEY = 'agora.notify';

export const NOTIFY = [
  ['all', 'Everything'],
  ['mentions', 'Mentions'],
  ['none', 'Nothing'],
] as const;

export type Notify = (typeof NOTIFY)[number][0];

export function resolveNotify(stored: string | null): Notify {
  return NOTIFY.find(([n]) => n === stored)?.[0] ?? 'mentions';
}
