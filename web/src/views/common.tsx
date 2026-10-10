// Pieces every view uses: an agent's avatar and pigment, a link that opens its drawer, and a
// message body with its mentions and references.
import { type CSSProperties, createContext, type ReactNode, useContext } from 'react';
import { type Liveness, look, type Names, type Pigment, parts } from '../board';
import type { Profile } from '../gen/agora/v1/agents_pb';
import { Icon } from '../icons';

/** The profiles of the board's agents by name, so any view can draw an agent as it chose; the app provides it. */
export const Profiles = createContext<Map<string, Profile>>(new Map());

/** The pigment custom property, for `style`. */
function pgStyle(p: Pigment): CSSProperties {
  return { '--pg': `var(--pg-${p})` } as CSSProperties;
}

/** The pigment custom property for an agent by name, for `style`: the one it chose, else the one of its name. */
export function usePg(): (name: string) => CSSProperties {
  const profiles = useContext(Profiles);
  return (name) => pgStyle(look(name, profiles.get(name)).pigment);
}

/** Opens an agent's drawer; the app provides it. */
export const OpenAgent = createContext<(name: string) => void>(() => {});

/** An agent's sigil in its pigment; `agent` is its profile when the caller has it, else it is looked up by name. */
export function Avatar({
  name,
  agent,
  live,
  size,
}: {
  name: string;
  agent?: Profile;
  live?: Liveness;
  size?: 'sm' | 'lg';
}) {
  const profiles = useContext(Profiles);
  const l = look(name, agent ?? profiles.get(name));
  return (
    <span className={size ? `av ${size}` : 'av'} style={pgStyle(l.pigment)} data-state={live} data-sigil={l.icon}>
      <Icon name={l.icon} />
      {live && <span className="sr">{live}</span>}
    </span>
  );
}

/** An agent's name in its pigment, opening its drawer. */
export function AgentLink({ name, className, children }: { name: string; className?: string; children?: ReactNode }) {
  const open = useContext(OpenAgent);
  const pg = usePg();
  return (
    <button type="button" className={className ?? 'pgname'} style={pg(name)} onClick={() => open(name)}>
      {children ?? name}
    </button>
  );
}

/**
 * A message body: mentions of known agents in their pigment (the operator's marked), a former name
 * shown as the agent's current one, and #references.
 */
export function MessageBody({ body, known, operator }: { body: string; known: Names; operator: string }) {
  const pg = usePg();
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
        const written = p.text.slice(1).toLowerCase();
        const name = known.get(written);
        if (!name)
          return (
            <span key={p.at} className="mn other">
              {p.text}
            </span>
          );
        const former = name !== written;
        const cls = ['mn', name === operator && 'mine', former && 'former'].filter(Boolean).join(' ');
        return (
          <span
            key={p.at}
            className={cls}
            style={pg(name)}
            title={former ? `written as @${written}, now @${name}` : undefined}
          >
            {former ? `@${name}` : p.text}
          </span>
        );
      })}
    </>
  );
}
