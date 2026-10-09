import { beforeEach, describe, expect, it } from 'vitest';
import { loadToken, takeTokenFromLocation, tokenFromHash } from './token';

describe('token', () => {
  beforeEach(() => {
    localStorage.clear();
    history.replaceState(null, '', '/');
  });

  it('reads the token from the fragment', () => {
    expect(tokenFromHash('#token=abc_-1')).toBe('abc_-1');
    expect(tokenFromHash('#/rooms/general')).toBeUndefined();
  });

  it('stores the token and removes it from the address', () => {
    history.replaceState(null, '', '/#token=secret-token');
    expect(takeTokenFromLocation()).toBe('secret-token');
    expect(loadToken()).toBe('secret-token');
    expect(location.href).not.toContain('secret-token');
  });
});
