// Five themes, Parchment the default. The system's color scheme is not consulted. Each theme's
// tokens live in styles.css under `:root[data-theme=...]`; the colors here draw its swatch and
// set the browser's theme color.
import { readPref } from './prefs';

export const THEMES = [
  { id: 'parchment', label: 'Parchment', note: 'papyrus by daylight', bg: '#ece3d2', accent: '#6e4f37', fg: '#2a211b' },
  { id: 'marble', label: 'Marble', note: 'cool Pentelic white', bg: '#eeeeea', accent: '#3c5677', fg: '#23262b' },
  { id: 'ivory', label: 'Ivory', note: 'bright ivory and amber', bg: '#fcf8ef', accent: '#a8581a', fg: '#2a211b' },
  { id: 'ink', label: 'Ink', note: 'sepia for the evening', bg: '#2a211b', accent: '#dcc6a0', fg: '#ece3d2' },
  { id: 'patina', label: 'Patina', note: 'green bronze at night', bg: '#1e2826', accent: '#c9a46a', fg: '#e4e0d2' },
] as const;

export type Theme = (typeof THEMES)[number]['id'];

export const THEME_KEY = 'agora.theme';

export function resolveTheme(stored: string | null): Theme {
  return THEMES.find((t) => t.id === stored)?.id ?? 'parchment';
}

export function storedTheme(): string | null {
  return readPref(THEME_KEY);
}

/** Shows a theme on the page and makes it the browser's theme color; storing it is up to the caller. */
export function applyTheme(t: Theme) {
  document.documentElement.dataset.theme = t;
  const bg = THEMES.find((x) => x.id === t)?.bg;
  if (bg) document.querySelector('meta[name="theme-color"]')?.setAttribute('content', bg);
}
