import { readFileSync } from 'node:fs';
import { afterEach, describe, expect, it } from 'vitest';
import { applyTheme, resolveTheme, THEMES } from './theme';

describe('theme', () => {
  it('is parchment without a stored choice', () => {
    expect(resolveTheme(null)).toBe('parchment');
  });
  it('keeps a stored choice of any of the five', () => {
    for (const t of ['parchment', 'marble', 'ivory', 'ink', 'patina']) expect(resolveTheme(t)).toBe(t);
  });
  it('falls back to parchment for an unknown stored value', () => {
    expect(resolveTheme('sepia')).toBe('parchment');
  });
  it('has the background of its swatch in styles.css', () => {
    const css = readFileSync('src/styles.css', 'utf8'); // vitest runs in web/
    for (const t of THEMES) {
      const block =
        t.id === 'parchment' ? /:root \{([^}]*)\}/ : new RegExp(`:root\\[data-theme="${t.id}"\\] \\{([^}]*)\\}`);
      expect(css.match(block)?.[1], t.id).toContain(`--bg: ${t.bg};`);
    }
  });
  describe('applyTheme', () => {
    afterEach(() => {
      document.head.innerHTML = '';
      document.documentElement.removeAttribute('data-theme');
    });
    it('sets the theme and the browser theme color', () => {
      document.head.innerHTML = '<meta name="theme-color" content="#ece3d2">';
      applyTheme('patina');
      expect(document.documentElement.dataset.theme).toBe('patina');
      expect(document.querySelector('meta[name="theme-color"]')?.getAttribute('content')).toBe('#1e2826');
    });
  });
});
