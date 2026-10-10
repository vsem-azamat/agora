import { renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { alerts, askToNotify, useNotifications } from './notify';

const counts = (entries: [string, number, number][]) =>
  new Map(entries.map(([room, unread, addressed]) => [room, { unread, addressed }]));

describe('alerts', () => {
  const before = counts([['example-app', 2, 0]]);
  const after = counts([
    ['example-app', 4, 1],
    ['website', 1, 0],
  ]);
  it('tells every rise for Everything', () => {
    expect(alerts(before, after, 'all')).toEqual([
      { room: 'example-app', body: '2 new · 1 for you' },
      { room: 'website', body: '1 new' },
    ]);
  });
  it('tells only messages to the operator for Mentions', () => {
    expect(alerts(before, after, 'mentions')).toEqual([{ room: 'example-app', body: '1 for you' }]);
  });
  it('tells every rise in a room that notifies, unless Nothing', () => {
    const wake = new Set(['website']);
    expect(alerts(before, after, 'mentions', wake)).toEqual([
      { room: 'example-app', body: '1 for you' },
      { room: 'website', body: '1 new' },
    ]);
    expect(alerts(before, after, 'all', wake)).toEqual(alerts(before, after, 'all'));
    expect(alerts(before, after, 'none', wake)).toEqual([]);
  });
  it('tells nothing for Nothing or when counts fall', () => {
    expect(alerts(before, after, 'none')).toEqual([]);
    expect(alerts(after, before, 'all')).toEqual([]);
  });
});

class FakeNotification {
  static permission: NotificationPermission = 'granted';
  static requestPermission = vi.fn(async () => 'granted' as NotificationPermission);
  static shown: [string, NotificationOptions | undefined][] = [];
  onclick: (() => void) | null = null;
  constructor(title: string, opts?: NotificationOptions) {
    FakeNotification.shown.push([title, opts]);
  }
  close() {}
}

describe('useNotifications', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    FakeNotification.shown = [];
    FakeNotification.permission = 'granted';
  });
  const run = (hidden: boolean) => {
    vi.stubGlobal('Notification', FakeNotification);
    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue(hidden ? 'hidden' : 'visible');
    const { rerender } = renderHook(({ u }) => useNotifications(u, 'mentions', () => {}), {
      initialProps: { u: counts([['example-app', 0, 0]]) },
    });
    rerender({ u: counts([['example-app', 1, 1]]) });
  };
  it('notifies about a mention while the page is hidden', () => {
    run(true);
    expect(FakeNotification.shown).toEqual([['#example-app', expect.objectContaining({ body: '1 for you' })]]);
  });
  it('shows nothing while the page is visible', () => {
    run(false);
    expect(FakeNotification.shown).toEqual([]);
  });
  it('asks for permission once', () => {
    vi.stubGlobal('Notification', FakeNotification);
    FakeNotification.permission = 'default';
    askToNotify();
    expect(FakeNotification.requestPermission).toHaveBeenCalledOnce();
  });
});
