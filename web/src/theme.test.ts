import { describe, expect, it } from 'vitest';
import { resolveTheme } from './theme';

describe('theme', () => {
  it('is parchment on a first visit without a dark preference', () => {
    expect(resolveTheme(null, false)).toBe('parchment');
  });
  it('follows a dark system without a stored choice', () => {
    expect(resolveTheme(null, true)).toBe('ink');
  });
  it('keeps a stored choice over the system', () => {
    expect(resolveTheme('parchment', true)).toBe('parchment');
    expect(resolveTheme('ink', false)).toBe('ink');
  });
});
