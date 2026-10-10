// Settings: kept in this browser, and deliberately few.
import type { ReactNode } from 'react';
import { Icon } from '../icons';
import { NOTIFY, type Notify, SIZES, type TextSize } from '../prefs';
import type { Theme } from '../theme';
import { pg } from './common';
import { ThemeCards } from './themes';

function Row({ label, desc, children }: { label: ReactNode; desc: ReactNode; children: ReactNode }) {
  return (
    <div className="setting">
      <span className="lbl">{label}</span>
      <span className="ctl">{children}</span>
      <span className="desc">{desc}</span>
    </div>
  );
}

function Segments<T extends string>(props: {
  label: string;
  options: readonly (readonly [T, string])[];
  value: T;
  onChange: (v: T) => void;
}) {
  return (
    <fieldset className="seg">
      <legend className="sr">{props.label}</legend>
      {props.options.map(([v, text]) => (
        <button key={v} type="button" aria-pressed={v === props.value} onClick={() => props.onChange(v)}>
          {text}
        </button>
      ))}
    </fieldset>
  );
}

function notifyNote(): string {
  if (typeof Notification === 'undefined') return 'This browser cannot show notifications.';
  if (Notification.permission === 'denied') return 'The browser blocks notifications for this page.';
  return 'While the tab is in the background.';
}

export function Settings(props: {
  operator: string;
  theme: Theme;
  onTheme: (t: Theme) => void;
  size: TextSize;
  onSize: (s: TextSize) => void;
  notify: Notify;
  onNotify: (n: Notify) => void;
  onSignOut: () => void;
}) {
  return (
    <section className="view settings" aria-label="Settings">
      <div className="head">
        <h2>
          <Icon name="settings" />
          Settings
        </h2>
        <span className="sum">kept in this browser</span>
      </div>
      <div className="sgroup">
        <h3>APPEARANCE</h3>
        <div className="setting wide">
          <span className="lbl">Theme</span>
          <ThemeCards current={props.theme} onChoose={props.onTheme} />
        </div>
        <Row label="Text size" desc="Large suits the phone.">
          <Segments label="Text size" options={SIZES} value={props.size} onChange={props.onSize} />
        </Row>
      </div>
      <div className="sgroup">
        <h3>NOTIFICATIONS</h3>
        <Row label="Notify me" desc={notifyNote()}>
          <Segments label="Notify me" options={NOTIFY} value={props.notify} onChange={props.onNotify} />
        </Row>
      </div>
      <div className="sgroup">
        <h3>ACCESS</h3>
        <Row
          label={
            <>
              Signed in as{' '}
              <span className="pgname" style={pg(props.operator)}>
                @{props.operator}
              </span>
            </>
          }
          desc={
            <>
              To rotate the token, run <code>agora web token --rotate</code> on the hub's machine; every browser then
              signs in again.
            </>
          }
        >
          <button type="button" className="btn quiet" onClick={props.onSignOut}>
            Sign out
          </button>
        </Row>
      </div>
    </section>
  );
}
