/**
 * The hub's clock, as far as the page knows it. Times the hub gives (a scheduled message's
 * `dueAt`) are counted down by the hub's time, not the browser's: a browser clock that runs
 * ahead or behind would show `going out…` too early or too late.
 */
let offset = 0;

/**
 * Takes the hub's time from an answer: the offset is the hub's time less the browser's at the
 * middle of the call. An answer without a time leaves the offset as it was.
 */
export function setHubClock(hub: Date | undefined, sent: number, received: number) {
  if (hub) offset = hub.getTime() - (sent + received) / 2;
}

/** The hub's time now, in milliseconds. */
export function hubNow(): number {
  return Date.now() + offset;
}

/** Sets the offset between the hub's clock and the browser's, in milliseconds (for tests). */
export function setClockOffset(ms: number) {
  offset = ms;
}

/** How often the wall clock is compared with the monotonic one. */
const TICK_MS = 1000;

/** How much the wall clock may differ from the monotonic one between two ticks before it counts as set. */
const JUMP_MS = 1000;

/**
 * Calls `onJump` when the browser's clock was set while the page stayed open (the time service
 * correcting it after the machine slept, or the user changing it): between two ticks the wall
 * clock moved differently from the monotonic one. The offset to the hub's clock is stale then.
 */
export function watchWallClock(onJump: () => void): () => void {
  let wall = Date.now();
  let mono = performance.now();
  const t = setInterval(() => {
    const dw = Date.now() - wall;
    const dm = performance.now() - mono;
    wall = Date.now();
    mono = performance.now();
    if (Math.abs(dw - dm) > JUMP_MS) onJump();
  }, TICK_MS);
  return () => clearInterval(t);
}
