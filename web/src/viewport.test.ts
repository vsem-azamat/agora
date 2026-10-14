import { afterEach, describe, expect, it, vi } from 'vitest';
import { watchViewport } from './viewport';

/** A visual viewport that can be resized as the on-screen keyboard opens. */
function fakeViewport(height: number) {
  const listeners = new Map<string, () => void>();
  return {
    height,
    scale: 1,
    addEventListener: (type: string, f: () => void) => listeners.set(type, f),
    removeEventListener: (type: string) => listeners.delete(type),
    resize(h: number) {
      this.height = h;
      listeners.get('resize')?.();
    },
    listeners,
  };
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

    it('scrolls back to the top when the finger lifts, without waiting', () => {
      vi.useFakeTimers();
      const vv = fakeViewport(480);
      const top = vi.fn();
      watch(document, vv, top);
      top.mockClear();
      document.dispatchEvent(new Event('touchend')); // nothing moved: nothing to undo
      expect(top).not.toHaveBeenCalled();
      vv.listeners.get('scroll')?.();
      expect(top).not.toHaveBeenCalled();
      document.dispatchEvent(new Event('touchend'));
      expect(top).toHaveBeenCalledOnce();
      vi.advanceTimersByTime(1000);
      expect(top).toHaveBeenCalledOnce();
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
      expect(top).not.toHaveBeenCalled();
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
