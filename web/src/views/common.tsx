// Pieces every view uses: an agent's avatar and pigment, a link that opens its drawer, and a
// message body with its mentions and references.
import { type CSSProperties, createContext, type ReactNode, useContext } from 'react';
import { type Liveness, parts, pigment } from '../board';
import { Icon } from '../icons';

/** The pigment custom property for an agent, for `style`. */
export function pg(name: string): CSSProperties {
  return { '--pg': `var(--pg-${pigment(name)})` } as CSSProperties;
}

/** Opens an agent's drawer; the app provides it. */
export const OpenAgent = createContext<(name: string) => void>(() => {});

export function Avatar({ name, live, size }: { name: string; live?: Liveness; size?: 'sm' | 'lg' }) {
  return (
    <span className={size ? `av ${size}` : 'av'} style={pg(name)} data-state={live}>
      <Icon name="agent" />
      {live && <span className="sr">{live}</span>}
    </span>
  );
}

/** An agent's name in its pigment, opening its drawer. */
export function AgentLink({ name, className, children }: { name: string; className?: string; children?: ReactNode }) {
  const open = useContext(OpenAgent);
  return (
    <button type="button" className={className ?? 'pgname'} style={pg(name)} onClick={() => open(name)}>
      {children ?? name}
    </button>
  );
}

/** A message body: mentions of known agents in their pigment (the operator's marked), and #references. */
export function MessageBody({ body, known, operator }: { body: string; known: Set<string>; operator: string }) {
  return (
    <>
      {parts(body).map((p) => {
        if (p.kind === 'text') return p.text;
        if (p.kind === 'ref')
          return (
            <span key={p.at} className="ref">
              {p.text}
            </span>
          );
        const name = p.text.slice(1).toLowerCase();
        if (!known.has(name))
          return (
            <span key={p.at} className="mn other">
              {p.text}
            </span>
          );
        return (
          <span key={p.at} className={name === operator ? 'mn mine' : 'mn'} style={pg(name)}>
            {p.text}
          </span>
        );
      })}
    </>
  );
}
