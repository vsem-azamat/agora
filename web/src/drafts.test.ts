import { afterEach, describe, expect, it } from 'vitest';
import { DRAFTS_KEY, loadDrafts, saveDrafts } from './drafts';

afterEach(() => sessionStorage.clear());

describe('drafts', () => {
  it('keep only what holds text', () => {
    saveDrafts({ general: { text: 'almost done', note: false }, chat: { text: '', note: true } });
    expect(loadDrafts()).toEqual({ general: { text: 'almost done', note: false } });
    saveDrafts({ general: { text: '', note: false } });
    expect(sessionStorage.getItem(DRAFTS_KEY)).toBeNull();
  });

  it('read anything unreadable as none', () => {
    for (const bad of ['{', 'null', '7', '"x"', '[1]']) {
      sessionStorage.setItem(DRAFTS_KEY, bad);
      expect(loadDrafts()).toEqual({});
    }
    sessionStorage.setItem(DRAFTS_KEY, JSON.stringify({ a: null, b: { text: 3 }, c: { text: 'hi', note: 'yes' } }));
    expect(loadDrafts()).toEqual({ c: { text: 'hi', note: false } });
  });
});
