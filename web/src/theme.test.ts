import { afterEach, describe, expect, it } from 'vitest';
import { applyTheme, resolveTheme } from './theme';

describe('theme', () => {
  it('is parchment without a stored choice', () => {
    expect(resolveTheme(null)).toBe('parchment');
  });
  it('keeps a stored choice', () => {
    expect(resolveTheme('ink')).toBe('ink');
    expect(resolveTheme('parchment')).toBe('parchment');
  });
  it('falls back to parchment for an unknown stored value', () => {
    expect(resolveTheme('sepia')).toBe('parchment');
  });
  describe('applyTheme', () => {
    afterEach(() => {
      document.head.innerHTML = '';
      document.documentElement.removeAttribute('style');
    });
    it('sets the browser theme color from the theme background', () => {
      document.head.innerHTML = '<meta name="theme-color" content="#ece3d2">';
      document.documentElement.style.setProperty('--bg', '#2a211b');
      applyTheme('ink');
      expect(document.documentElement.dataset.theme).toBe('ink');
      expect(document.querySelector('meta[name="theme-color"]')?.getAttribute('content')).toBe('#2a211b');
    });
  });
});
