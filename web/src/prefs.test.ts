import { describe, expect, it } from 'vitest';
import { resolveNotify, resolveSize } from './prefs';

describe('prefs', () => {
  it('fall back to medium text and mentions', () => {
    expect(resolveSize(null)).toBe('m');
    expect(resolveSize('xl')).toBe('m');
    expect(resolveSize('l')).toBe('l');
    expect(resolveNotify(null)).toBe('mentions');
    expect(resolveNotify('all')).toBe('all');
    expect(resolveNotify('loud')).toBe('mentions');
  });
});
