// Pure helpers for bridged rooms: a bridge's state and policy, messages from outside and where a
// message stands on its way out. Kept free of React so they are cheap to test.
import { type Bridge, BridgePolicy, BridgeState } from './gen/agora/v1/bridges_pb';
import { DeliveryState, type Message } from './gen/agora/v1/rooms_pb';

/** Whether the hub runs a bridge: running, restarting after its process exited, or stopped. */
export type Running = 'running' | 'restarting' | 'stopped';

export function runningOf(b: Pick<Bridge, 'state'>): Running {
  if (b.state === BridgeState.RESTARTING) return 'restarting';
  if (b.state === BridgeState.RUNNING) return 'running';
  return 'stopped';
}

/** How a bridge's state reads: `running`, `restarting · exit status 1`, `stopped`. */
export function runningText(b: Pick<Bridge, 'state' | 'lastExit'>): string {
  const r = runningOf(b);
  return r === 'restarting' && b.lastExit ? `${r} · ${b.lastExit}` : r;
}

/** What goes out of a bridged room. */
export type Policy = 'approve' | 'open' | 'read';

const POLICIES: Record<Policy, BridgePolicy> = {
  approve: BridgePolicy.APPROVE,
  open: BridgePolicy.OPEN,
  read: BridgePolicy.READ,
};

export function policyOf(p: BridgePolicy): Policy {
  return p === BridgePolicy.OPEN ? 'open' : p === BridgePolicy.READ ? 'read' : 'approve';
}

export function bridgePolicy(p: Policy): BridgePolicy {
  return POLICIES[p];
}

export const POLICY_LABELS: Record<Policy, string> = {
  approve: 'Ask before sending',
  open: 'Send at once',
  read: 'Read only',
};

/** Where a message stands on its way out; undefined for one that stays here. */
export type Delivery = 'pending' | 'sending' | 'sent' | 'declined' | 'failed';

const DELIVERIES: Partial<Record<DeliveryState, Delivery>> = {
  [DeliveryState.PENDING]: 'pending',
  [DeliveryState.SENDING]: 'sending',
  [DeliveryState.SENT]: 'sent',
  [DeliveryState.DECLINED]: 'declined',
  [DeliveryState.FAILED]: 'failed',
};

export function deliveryOf(m: Pick<Message, 'deliveryState'>): Delivery | undefined {
  return DELIVERIES[m.deliveryState];
}

/** Whether a message came from outside through a bridge. */
export function fromOutside(m: Pick<Message, 'externalAuthor'>): boolean {
  return m.externalAuthor !== undefined;
}

/** Who wrote a message, as the tape names it: the agent, or the name outside. */
export function authorName(m: Pick<Message, 'author' | 'externalAuthor'>): string {
  return m.externalAuthor ? m.externalAuthor.name || m.externalAuthor.id : m.author;
}

/**
 * Who wrote a message, as one key: the agent's name, or the bridge and the id outside for someone
 * outside, whose `author` is empty and so cannot tell two people apart; the name stands in for an
 * id the bridge did not give.
 */
export function authorKey(m: Pick<Message, 'author' | 'externalAuthor'>): string {
  const x = m.externalAuthor;
  if (!x) return m.author;
  return x.id ? `${x.bridge}\nid ${x.id}` : `${x.bridge}\nname ${x.name}`;
}

/** The mark of someone outside: the first letter of the name, else of the id. */
export function initial(name: string): string {
  return ([...name.trim()][0] ?? '?').toUpperCase();
}
