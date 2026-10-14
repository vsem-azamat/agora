import { afterEach, describe, expect, it, vi } from 'vitest';
import { SETTLE_MS, watchViewport } from './viewport';

/** A visual viewport that can be resized as the on-screen keyboard opens. */
function fakeViewport(height: number) {
  const listeners = new Map<string, () => void>();
  return {
    height,
    scale: 1,
    addEventListener: (type: string, f: () => void) => listeners.set(type, f),
    removeEventListener: (type: string, f: () => void) => {
      if (listeners.get(type) === f) listeners.delete(type);
    },
    resize(h: number) {
      this.height = h;
      listeners.get('resize')?.();
    },
    listeners,
  };
}

/** A touch event with `n` fingers still on the screen. */
function touch(type: 'touchstart' | 'touchend' | 'touchcancel', n: number) {
  const e = new Event(type);
  Object.defineProperty(e, 'touches', { value: new Array(n).fill({}) });
  document.dispatchEvent(e);
}

/** A touch event dispatched on `target`, with `n` fingers still on the screen. */
function touchAt(target: EventTarget, type: string, n: number) {
  const e = new Event(type, { bubbles: true });
  Object.defineProperty(e, 'touches', { value: new Array(n).fill({}) });
  target.dispatchEvent(e);
}

/** The page goes to the background, or comes back. */
function visibility(state: 'hidden' | 'visible') {
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue(state);
  document.dispatchEvent(new Event('visibilitychange'));
  vi.restoreAllMocks();
}

const root = document.documentElement;
// every watcher stops after its test, also a failed one, so it cannot break the next
const stops: (() => void)[] = [];
function watch(...args: Parameters<typeof watchViewport>) {
  const stop = watchViewport(...args);
  stops.push(stop);
  return stop;
}
afterEach(() => {
  for (const stop of stops.splice(0)) stop();
  root.removeAttribute('data-typing');
  root.style.removeProperty('--vvh');
  document.body.innerHTML = '';
});

describe('the part of the screen the app may use', () => {
  it('follows the visual viewport as the keyboard opens and closes, scrolled back to the top', () => {
    const vv = fakeViewport(844);
    const top = vi.fn();
    const stop = watch(document, vv, top);
    expect(root.style.getPropertyValue('--vvh')).toBe('844px');
    vv.resize(480);
    expect(root.style.getPropertyValue('--vvh')).toBe('480px');
    expect(top).toHaveBeenCalled();
    stop();
    expect(vv.listeners.size).toBe(0);
  });

  it('keeps the height and the place on the page while zoomed in', () => {
    const vv = fakeViewport(844);
    const top = vi.fn();
    const stop = watch(document, vv, top);
    top.mockClear();
    vv.scale = 2;
    vv.resize(422);
    expect(root.style.getPropertyValue('--vvh')).toBe('844px');
    expect(top).not.toHaveBeenCalled();
    stop();
  });

  describe('when the visible part moves', () => {
    afterEach(() => vi.useRealTimers());

    it('scrolls back to the top only once the scrolling settles, not under the finger', () => {
      vi.useFakeTimers();
      const vv = fakeViewport(480);
      const top = vi.fn();
      watch(document, vv, top);
      top.mockClear();
      for (let i = 0; i < 5; i++) {
        vv.listeners.get('scroll')?.();
        vi.advanceTimersByTime(100); // events keep coming within 150 ms of each other
      }
      expect(top).not.toHaveBeenCalled();
      vi.advanceTimersByTime(49);
      expect(top).not.toHaveBeenCalled();
      vi.advanceTimersByTime(1);
      expect(top).toHaveBeenCalledOnce();
      vi.advanceTimersByTime(1000);
      expect(top).toHaveBeenCalledOnce();
    });

    it.each(['touchend', 'touchcancel'] as const)(
      'scrolls back to the top at once on %s of the last finger',
      (type) => {
        vi.useFakeTimers();
        const vv = fakeViewport(480);
        const top = vi.fn();
        watch(document, vv, top);
        top.mockClear();
        touch(type, 0); // nothing moved: nothing to undo
        expect(top).not.toHaveBeenCalled();
        vv.listeners.get('scroll')?.();
        expect(top).not.toHaveBeenCalled();
        touch(type, 0);
        expect(top).toHaveBeenCalledOnce();
        vi.advanceTimersByTime(1000);
        expect(top).toHaveBeenCalledOnce();
        touch(type, 0); // settled: a later lift has nothing to undo
        expect(top).toHaveBeenCalledOnce();
      },
    );

    it('is not held by a finger whose element left the page before it lifted', () => {
      vi.useFakeTimers();
      const vv = fakeViewport(480);
      const top = vi.fn();
      watch(document, vv, top);
      top.mockClear();
      const el = document.createElement('div');
      document.body.append(el);
      const on = (type: string, n: number, target: EventTarget) => {
        const e = new Event(type, { bubbles: true });
        Object.defineProperty(e, 'touches', { value: new Array(n).fill({}) });
        target.dispatchEvent(e);
      };
      on('touchstart', 1, el);
      el.remove(); // a tablet drawn again under the finger; its touchend no longer reaches the document
      on('touchend', 0, el);
      vv.listeners.get('scroll')?.();
      vi.advanceTimersByTime(SETTLE_MS);
      expect(top).toHaveBeenCalledOnce();
    });

    it('is not held by a finger when the page goes to the background', () => {
      vi.useFakeTimers();
      const vv = fakeViewport(480);
      const top = vi.fn();
      watch(document, vv, top);
      top.mockClear();
      touch('touchstart', 1);
      vv.listeners.get('scroll')?.();
      visibility('hidden');
      vi.advanceTimersByTime(SETTLE_MS);
      expect(top).toHaveBeenCalledOnce();
      // back again, the finger that was down is gone: a pan with none settles
      top.mockClear();
      visibility('visible');
      vv.listeners.get('scroll')?.();
      vi.advanceTimersByTime(SETTLE_MS);
      expect(top).toHaveBeenCalledOnce();
    });

    it('is not held by the last of two fingers on an element removed between the lifts', () => {
      vi.useFakeTimers();
      const vv = fakeViewport(480);
      const top = vi.fn();
      watch(document, vv, top);
      top.mockClear();
      const el = document.createElement('div');
      document.body.append(el);
      touchAt(el, 'touchstart', 1);
      touchAt(el, 'touchstart', 2);
      touchAt(el, 'touchend', 1);
      el.remove();
      touchAt(el, 'touchend', 0);
      vv.listeners.get('scroll')?.();
      vi.advanceTimersByTime(SETTLE_MS);
      expect(top).toHaveBeenCalledOnce();
    });

    it('is not held by a touch cancelled on an element that left the page', () => {
      vi.useFakeTimers();
      const vv = fakeViewport(480);
      const top = vi.fn();
      watch(document, vv, top);
      top.mockClear();
      const el = document.createElement('div');
      document.body.append(el);
      touchAt(el, 'touchstart', 1);
      el.remove();
      touchAt(el, 'touchcancel', 0);
      vv.listeners.get('scroll')?.();
      vi.advanceTimersByTime(SETTLE_MS);
      expect(top).toHaveBeenCalledOnce();
    });

    it('settles once when the target and the document both hear the lift', () => {
      vi.useFakeTimers();
      const vv = fakeViewport(480);
      const top = vi.fn();
      watch(document, vv, top);
      top.mockClear();
      const el = document.createElement('div');
      document.body.append(el);
      touchAt(el, 'touchstart', 1);
      vv.listeners.get('scroll')?.();
      touchAt(el, 'touchend', 0);
      vi.advanceTimersByTime(1000);
      expect(top).toHaveBeenCalledOnce();
    });

    it('settles once when the page goes to the background with a scroll pending', () => {
      vi.useFakeTimers();
      const vv = fakeViewport(480);
      const top = vi.fn();
      watch(document, vv, top);
      top.mockClear();
      vv.listeners.get('scroll')?.();
      visibility('hidden');
      vi.advanceTimersByTime(1000);
      expect(top).toHaveBeenCalledOnce();
    });

    it('stops listening on the elements touched once the last finger lifts, and when it stops', () => {
      const vv = fakeViewport(480);
      const stop = watch(document, vv, vi.fn());
      const [a, b] = [document.createElement('div'), document.createElement('div')];
      document.body.append(a, b);
      const removed = (el: HTMLElement) => vi.spyOn(el, 'removeEventListener');
      const [ra, rb] = [removed(a), removed(b)];
      touchAt(a, 'touchstart', 1);
      touchAt(b, 'touchstart', 2);
      touchAt(b, 'touchend', 1);
      expect(ra).not.toHaveBeenCalled(); // a finger is still down
      touchAt(a, 'touchend', 0);
      expect(ra.mock.calls.map((c) => c[0]).sort()).toEqual(['touchcancel', 'touchend']);
      expect(rb.mock.calls.map((c) => c[0]).sort()).toEqual(['touchcancel', 'touchend']);
      ra.mockClear();
      touchAt(a, 'touchstart', 1);
      stop();
      expect(ra.mock.calls.map((c) => c[0]).sort()).toEqual(['touchcancel', 'touchend']);
    });

    it('stops listening on the elements touched when the page goes to the background', () => {
      watch(document, fakeViewport(480), vi.fn());
      const el = document.createElement('div');
      document.body.append(el);
      const removed = vi.spyOn(el, 'removeEventListener');
      touchAt(el, 'touchstart', 1);
      visibility('hidden');
      expect(removed).toHaveBeenCalledTimes(2);
    });

    describe('after the watch stopped', () => {
      it('ignores a touch that ends on an element that left the page', () => {
        vi.useFakeTimers();
        const vv = fakeViewport(480);
        const top = vi.fn();
        const stop = watch(document, vv, top);
        top.mockClear();
        const el = document.createElement('div');
        document.body.append(el);
        touchAt(el, 'touchstart', 1);
        vv.listeners.get('scroll')?.();
        stop();
        el.remove();
        touchAt(el, 'touchend', 0);
        vi.advanceTimersByTime(1000);
        expect(top).not.toHaveBeenCalled();
      });

      it('ignores the page going to the background, and a timer that was pending', () => {
        vi.useFakeTimers();
        const vv = fakeViewport(480);
        const top = vi.fn();
        const stop = watch(document, vv, top);
        top.mockClear();
        touch('touchstart', 1);
        vv.listeners.get('scroll')?.();
        stop();
        visibility('hidden');
        vi.advanceTimersByTime(1000);
        expect(top).not.toHaveBeenCalled();
      });

      it('has no timer left from a scroll and the page going to the background', () => {
        vi.useFakeTimers();
        const vv = fakeViewport(480);
        const top = vi.fn();
        const stop = watch(document, vv, top);
        top.mockClear();
        vv.listeners.get('scroll')?.();
        visibility('hidden'); // a second timer replaces the first
        stop();
        vi.advanceTimersByTime(1000);
        expect(top).not.toHaveBeenCalled();
      });
    });

    it('does not scroll while zoomed in, nor after the watch stopped', () => {
      vi.useFakeTimers();
      const vv = fakeViewport(480);
      const top = vi.fn();
      const stop = watch(document, vv, top);
      top.mockClear();
      vv.scale = 2;
      vv.listeners.get('scroll')?.();
      vi.advanceTimersByTime(1000);
      expect(top).not.toHaveBeenCalled();
      vv.scale = 1;
      vv.listeners.get('scroll')?.();
      stop();
      vi.advanceTimersByTime(1000);
      touch('touchend', 0);
      touch('touchcancel', 0);
      expect(top).not.toHaveBeenCalled();
      expect(vv.listeners.size).toBe(0);
    });
  });

  it('keeps typing while the focus moves to the send button of the same form', () => {
    const stop = watch(document, undefined, vi.fn());
    document.body.innerHTML = '<form><textarea></textarea><button>Send</button></form><button id="away">x</button>';
    document.querySelector('textarea')?.focus();
    (document.querySelector('form button') as HTMLElement).focus();
    expect(root.hasAttribute('data-typing')).toBe(true);
    (document.getElementById('away') as HTMLElement).focus();
    expect(root.hasAttribute('data-typing')).toBe(false);
    stop();
  });

  it('stops typing when the focus lands outside a form, though the field left without a word', () => {
    watch(document, undefined, vi.fn());
    document.body.innerHTML = '<button id="away">x</button>';
    root.setAttribute('data-typing', ''); // as if a removed field never reported leaving
    (document.getElementById('away') as HTMLElement).focus();
    expect(root.hasAttribute('data-typing')).toBe(false);
  });

  it('marks the page while a text field has the focus', () => {
    const stop = watch(document, undefined, vi.fn());
    document.body.innerHTML = '<textarea></textarea><input type="radio"><button>x</button>';
    const [box, radio] = [document.querySelector('textarea'), document.querySelector('input')] as HTMLElement[];
    box?.focus();
    expect(root.hasAttribute('data-typing')).toBe(true);
    radio?.focus();
    expect(root.hasAttribute('data-typing')).toBe(false);
    box?.focus();
    box?.blur();
    expect(root.hasAttribute('data-typing')).toBe(false);
    stop();
    box?.focus();
    expect(root.hasAttribute('data-typing')).toBe(false);
  });
});
