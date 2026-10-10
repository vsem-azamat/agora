// Theme pickers: cards in Settings, dots in the account menu. Pointing at or focusing a theme
// shows it at once; leaving restores the current one; choosing it stores it.
import { type CSSProperties, useEffect, useRef } from 'react';
import { applyTheme, THEMES, type Theme } from '../theme';

type Picker = { current: Theme; onChoose: (t: Theme) => void };

function swatch(t: (typeof THEMES)[number]): CSSProperties {
  return { '--sw-bg': t.bg, '--sw-ac': t.accent, '--sw-fg': t.fg } as CSSProperties;
}

/** Preview handlers for a theme button; unmounting mid-preview restores the current theme. */
function usePreview(current: Theme) {
  const now = useRef(current);
  now.current = current;
  useEffect(() => () => applyTheme(now.current), []);
  return (t: Theme) => {
    const show = () => applyTheme(t);
    const restore = () => applyTheme(now.current);
    return { onPointerEnter: show, onFocus: show, onPointerLeave: restore, onBlur: restore };
  };
}

export function ThemeCards({ current, onChoose }: Picker) {
  const preview = usePreview(current);
  return (
    <div className="themes">
      {THEMES.map((t) => (
        <button
          key={t.id}
          type="button"
          className="theme"
          aria-pressed={t.id === current}
          title={t.note}
          onClick={() => onChoose(t.id)}
          {...preview(t.id)}
        >
          <span className="sw" style={swatch(t)}>
            <b>Aa</b>
            <i />
            <i />
          </span>
          {t.label}
        </button>
      ))}
    </div>
  );
}

export function ThemeDots({ current, onChoose }: Picker) {
  const preview = usePreview(current);
  return (
    <fieldset className="dots">
      <legend className="sr">Theme</legend>
      {THEMES.map((t) => (
        <button
          key={t.id}
          type="button"
          className="dot"
          style={swatch(t)}
          aria-pressed={t.id === current}
          aria-label={`${t.label} theme`}
          title={t.label}
          onClick={() => onChoose(t.id)}
          {...preview(t.id)}
        />
      ))}
    </fieldset>
  );
}
