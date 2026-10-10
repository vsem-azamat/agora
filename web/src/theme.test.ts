import { describe, expect, it } from 'vitest';
import { resolveTheme } from './theme';

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
});
