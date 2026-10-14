// The part of the screen the app may use. On a phone the on-screen keyboard covers the lower part
// of the screen, and some browsers (Safari) do not shrink the page for it: the app takes the
// height of the visual viewport (`--vvh`) and stays scrolled to the top, so the compose box sits
// right above the keyboard. While a text field has the focus the page is marked `data-typing`,
// which hides the phone's bottom tabs. The scroll back to the top comes at once when the viewport
// resizes, but after a scroll only once it settles, so a drag that pans the page is not snapped
// back under the finger.

type Viewport = {
  height: number;
  /** The pinch zoom; zoomed in, the visible part is smaller without the keyboard. */
  scale: number;
  addEventListener: (type: 'resize' | 'scroll', f: () => void) => void;
  removeEventListener: (type: 'resize' | 'scroll', f: () => void) => void;
};

/** How long without a scroll event the visual viewport counts as settled. */
export const SETTLE_MS = 150;

const notTyped = new Set(['button', 'checkbox', 'color', 'file', 'image', 'radio', 'range', 'reset', 'submit']);

/** Whether the keyboard comes up for the element. */
function textField(el: EventTarget | null): boolean {
  return el instanceof HTMLTextAreaElement || (el instanceof HTMLInputElement && !notTyped.has(el.type));
}

/** Keeps `--vvh` and `data-typing` on the page's root up to date; returns the function that stops it. */
export function watchViewport(doc: Document, vv: Viewport | undefined, toTop: () => void): () => void {
  const root = doc.documentElement;
  let settling: ReturnType<typeof setTimeout> | undefined;
  let moved = false; // the visual viewport scrolled and has not settled yet
  let touching = false; // a finger is on the screen
  const settle = () => {
    clearTimeout(settling);
    settling = undefined;
    moved = false;
    if (vv && vv.scale <= 1) toTop(); // zoomed in, the operator pans the page
  };
  const size = () => {
    if (!vv) return;
    root.style.setProperty('--vvh', `${vv.height * vv.scale}px`);
    settle();
  };
  // the page moved: back to the top when it stops moving, not on every event and never while a
  // finger is down, even one held still
  const scrolled = () => {
    moved = true;
    clearTimeout(settling);
    settling = touching ? undefined : setTimeout(settle, SETTLE_MS);
  };
  // the last finger lifting ends the pan: no need to wait for more events
  const lifted = (e: TouchEvent) => {
    touching = e.touches.length > 0;
    if (touching) return;
    forget();
    if (moved) settle();
  };
  // touchend and touchcancel go to the element the touch began on and reach the document only
  // while it is in the page: the elements touches began on are listened to as well, until the last
  // finger lifts, in case one is removed under a finger
  const targets = new Set<EventTarget>();
  const forget = () => {
    for (const t of targets) {
      t.removeEventListener('touchend', lifted as EventListener);
      t.removeEventListener('touchcancel', lifted as EventListener);
    }
    targets.clear();
  };
  const touched = (e: TouchEvent) => {
    touching = e.touches.length > 0;
    if (e.target && e.target !== doc && !targets.has(e.target)) {
      targets.add(e.target);
      e.target.addEventListener('touchend', lifted as EventListener);
      e.target.addEventListener('touchcancel', lifted as EventListener);
    }
  };
  // a page sent to the background gets no touchend: no finger is down any more
  const visibility = () => {
    if (doc.visibilityState !== 'hidden') return;
    touching = false;
    forget();
    if (moved) {
      clearTimeout(settling);
      settling = setTimeout(settle, SETTLE_MS);
    }
  };
  // typing starts in a text field; where it ends is decided when the focus leaves one, and again
  // when the focus lands outside a form, in case a removed field's focusout never came
  const focusIn = (e: FocusEvent) => {
    if (textField(e.target)) root.setAttribute('data-typing', '');
    else if (!(e.target instanceof Element && e.target.closest('form'))) root.removeAttribute('data-typing');
  };
  // moving to another control of the same form, such as Send, is still typing: the box must not
  // jump under the finger between the press and the click
  const focusOut = (e: FocusEvent) => {
    const to = e.relatedTarget;
    const form = e.target instanceof Element ? e.target.closest('form') : null;
    root.toggleAttribute('data-typing', textField(to) || (to instanceof Node && !!form?.contains(to)));
  };
  size();
  vv?.addEventListener('resize', size);
  vv?.addEventListener('scroll', scrolled);
  doc.addEventListener('touchstart', touched);
  doc.addEventListener('touchend', lifted);
  doc.addEventListener('touchcancel', lifted);
  doc.addEventListener('visibilitychange', visibility);
  doc.addEventListener('focusin', focusIn);
  doc.addEventListener('focusout', focusOut);
  return () => {
    vv?.removeEventListener('resize', size);
    vv?.removeEventListener('scroll', scrolled);
    forget();
    clearTimeout(settling);
    settling = undefined;
    doc.removeEventListener('visibilitychange', visibility);
    doc.removeEventListener('touchstart', touched);
    doc.removeEventListener('touchend', lifted);
    doc.removeEventListener('touchcancel', lifted);
    doc.removeEventListener('focusin', focusIn);
    doc.removeEventListener('focusout', focusOut);
  };
}
