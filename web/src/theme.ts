// Parchment is the default theme; ink is the dark one. A choice the operator made wins; without
// one the app follows the system's color scheme.
export type Theme = 'parchment' | 'ink';

const KEY = 'agora.theme';

export function resolveTheme(stored: string | null, prefersDark: boolean): Theme {
  if (stored === 'parchment' || stored === 'ink') return stored;
  return prefersDark ? 'ink' : 'parchment';
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

export function prefersDark(): boolean {
  return typeof matchMedia === 'function' && matchMedia('(prefers-color-scheme: dark)').matches;
}

export function applyTheme(t: Theme) {
  document.documentElement.dataset.theme = t;
  const meta = document.querySelector('meta[name="theme-color"]');
  meta?.setAttribute('content', t === 'ink' ? '#2a211b' : '#ece3d2');
}
