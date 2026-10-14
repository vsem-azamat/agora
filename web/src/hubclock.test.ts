import { afterEach, describe, expect, it, vi } from 'vitest';
import { hubNow, setClockOffset, setHubClock, watchWallClock } from './hubclock';

afterEach(() => {
  setClockOffset(0);
  vi.useRealTimers();
});

describe('the hub clock', () => {
  it('takes the offset from the middle of the call, and keeps it for an answer without a time', () => {
    vi.useFakeTimers({ now: new Date('2026-10-09T12:00:20Z') });
    setHubClock(new Date('2026-10-09T12:00:00Z'), Date.now() - 100, Date.now() + 100);
    expect(hubNow()).toBe(new Date('2026-10-09T12:00:00Z').getTime());
    setHubClock(undefined, 0, 0);
    expect(hubNow()).toBe(new Date('2026-10-09T12:00:00Z').getTime());
  });

  it('notices the browser clock being set, not time passing', () => {
    vi.useFakeTimers({ now: new Date('2026-10-09T12:00:00Z') });
    const onJump = vi.fn();
    const stop = watchWallClock(onJump);
    vi.advanceTimersByTime(5000);
    expect(onJump).not.toHaveBeenCalled();
    vi.setSystemTime(Date.now() + 30_000); // the time service corrects the clock
    vi.advanceTimersByTime(1000);
    expect(onJump).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(5000);
    expect(onJump).toHaveBeenCalledTimes(1);
    stop();
  });
});
