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
