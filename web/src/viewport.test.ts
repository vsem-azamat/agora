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
afterEach(() => {
  root.removeAttribute('data-typing');
  root.style.removeProperty('--vvh');
  document.body.innerHTML = '';
});

describe('the part of the screen the app may use', () => {
  it('follows the visual viewport as the keyboard opens and closes, scrolled back to the top', () => {
    const vv = fakeViewport(844);
    const top = vi.fn();
    const stop = watchViewport(document, vv, top);
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
    const stop = watchViewport(document, vv, top);
    top.mockClear();
    vv.scale = 2;
    vv.resize(422);
    expect(root.style.getPropertyValue('--vvh')).toBe('844px');
    expect(top).not.toHaveBeenCalled();
    stop();
  });

  it('scrolls back to the top when the visible part moves', () => {
    const vv = fakeViewport(480);
    const top = vi.fn();
    const stop = watchViewport(document, vv, top);
    top.mockClear();
    vv.listeners.get('scroll')?.();
    expect(top).toHaveBeenCalledOnce();
    stop();
  });

  it('keeps typing while the focus moves to the send button of the same form', () => {
    const stop = watchViewport(document, undefined, vi.fn());
    document.body.innerHTML = '<form><textarea></textarea><button>Send</button></form><button id="away">x</button>';
    document.querySelector('textarea')?.focus();
    (document.querySelector('form button') as HTMLElement).focus();
    expect(root.hasAttribute('data-typing')).toBe(true);
    (document.getElementById('away') as HTMLElement).focus();
    expect(root.hasAttribute('data-typing')).toBe(false);
    stop();
  });

  it('marks the page while a text field has the focus', () => {
    const stop = watchViewport(document, undefined, vi.fn());
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
