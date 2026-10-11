// The part of the screen the app may use. On a phone the on-screen keyboard covers the lower part
// of the screen, and some browsers (Safari) do not shrink the page for it: the app takes the
// height of the visual viewport (`--vvh`) and stays scrolled to the top, so the compose box sits
// right above the keyboard. While a text field has the focus the page is marked `data-typing`,
// which hides the phone's bottom tabs.

type Viewport = {
  height: number;
  /** The pinch zoom; zoomed in, the visible part is smaller without the keyboard. */
  scale: number;
  addEventListener: (type: 'resize' | 'scroll', f: () => void) => void;
  removeEventListener: (type: 'resize' | 'scroll', f: () => void) => void;
};

const notTyped = new Set(['button', 'checkbox', 'color', 'file', 'image', 'radio', 'range', 'reset', 'submit']);

/** Whether the keyboard comes up for the element. */
function textField(el: EventTarget | null): boolean {
  return el instanceof HTMLTextAreaElement || (el instanceof HTMLInputElement && !notTyped.has(el.type));
}

/** Keeps `--vvh` and `data-typing` on the page's root up to date; returns the function that stops it. */
export function watchViewport(doc: Document, vv: Viewport | undefined, toTop: () => void): () => void {
  const root = doc.documentElement;
  const size = () => {
    if (!vv) return;
    root.style.setProperty('--vvh', `${vv.height * vv.scale}px`);
    if (vv.scale <= 1) toTop(); // zoomed in, the operator pans the page
  };
  const focusIn = (e: FocusEvent) => root.toggleAttribute('data-typing', textField(e.target));
  const focusOut = (e: FocusEvent) => root.toggleAttribute('data-typing', textField(e.relatedTarget));
  size();
  vv?.addEventListener('resize', size);
  vv?.addEventListener('scroll', size);
  doc.addEventListener('focusin', focusIn);
  doc.addEventListener('focusout', focusOut);
  return () => {
    vv?.removeEventListener('resize', size);
    vv?.removeEventListener('scroll', size);
    doc.removeEventListener('focusin', focusIn);
    doc.removeEventListener('focusout', focusOut);
  };
}
