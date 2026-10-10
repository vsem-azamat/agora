// Parchment is the default theme; ink is the dark one the operator can switch to. The system's
// color scheme is not consulted.
export type Theme = 'parchment' | 'ink';

const KEY = 'agora.theme';

export function resolveTheme(stored: string | null): Theme {
  return stored === 'ink' ? 'ink' : 'parchment';
}

export function storedTheme(): string | null {
  try {
    return localStorage.getItem(KEY);
  } catch {
    return null;
  }
}

export function storeTheme(t: Theme) {
  try {
    localStorage.setItem(KEY, t);
  } catch {
    // storage may be off; the choice then lasts for this page only
  }
}

/** Sets the theme on the page; the browser's theme color follows the theme's `--bg`. */
export function applyTheme(t: Theme) {
  const root = document.documentElement;
  root.dataset.theme = t;
  const bg = getComputedStyle(root).getPropertyValue('--bg').trim();
  if (bg) document.querySelector('meta[name="theme-color"]')?.setAttribute('content', bg);
}
