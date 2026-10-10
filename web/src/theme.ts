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

export function applyTheme(t: Theme) {
  document.documentElement.dataset.theme = t;
  const meta = document.querySelector('meta[name="theme-color"]');
  meta?.setAttribute('content', t === 'ink' ? '#2a211b' : '#ece3d2');
}
