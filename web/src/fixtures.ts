// Example board data for tests, with neutral names.
import { create, type MessageInitShape } from '@bufbuild/protobuf';
import { timestampFromDate } from '@bufbuild/protobuf/wkt';
import { CiState, type Profile, ProfileSchema } from './gen/agora/v1/agents_pb';
import {
  type Proposal,
  ProposalSchema,
  ProposalState,
  ProposalVoteSchema,
  VoteChoice,
} from './gen/agora/v1/governance_pb';
import { EntrySchema, EntryState, type Resource, ResourceSchema } from './gen/agora/v1/resources_pb';
import { SessionState } from './gen/agora/v1/sessions_pb';

const sessions: Record<string, SessionState> = {
  busy: SessionState.BUSY,
  idle: SessionState.IDLE,
  offline: SessionState.OFFLINE,
};
const states: Record<string, ProposalState> = {
  open: ProposalState.OPEN,
  accepted: ProposalState.ACCEPTED,
  rejected: ProposalState.REJECTED,
  withdrawn: ProposalState.WITHDRAWN,
};
const choices: Record<string, VoteChoice> = { yes: VoteChoice.YES, no: VoteChoice.NO, abstain: VoteChoice.ABSTAIN };
const ciStates: Record<string, CiState> = { green: CiState.GREEN, red: CiState.RED, conflict: CiState.CONFLICT };

/** CI states by pull request, written as words. */
export function ci(states: Record<number, string>): Record<number, CiState> {
  return Object.fromEntries(Object.entries(states).map(([n, s]) => [n, ciStates[s] ?? CiState.UNSPECIFIED]));
}

export const NOW = new Date('2026-10-09T22:50:00Z');
const ago = (min: number) => timestampFromDate(new Date(NOW.getTime() - min * 60_000));
const inMin = (min: number) => timestampFromDate(new Date(NOW.getTime() + min * 60_000));

export function agent(name: string, state: string, more: MessageInitShape<typeof ProfileSchema> = {}): Profile {
  return create(ProfileSchema, {
    name,
    session: sessions[state] ?? SessionState.UNSPECIFIED,
    project: 'example-app',
    task: `${name} task`,
    updatedAt: ago(3),
    active: true,
    ...more,
  });
}

export function lock(key: string, holder?: string, waiting: string[] = []): Resource {
  return create(ResourceSchema, {
    key,
    slots: 1,
    entries: [
      ...(holder ? [create(EntrySchema, { key, agent: holder, state: EntryState.HELD, expiresAt: inMin(12) })] : []),
      ...waiting.map((a, i) => create(EntrySchema, { key, agent: a, state: EntryState.WAITING, position: i + 1 })),
    ],
  });
}

export function queue(key: string, slots: number, holders: [string, number][], waiting: string[]): Resource {
  return create(ResourceSchema, {
    key,
    slots,
    entries: [
      ...holders.map(([a, left]) =>
        create(EntrySchema, { key, agent: a, state: EntryState.HELD, expiresAt: inMin(left) }),
      ),
      ...waiting.map((a, i) => create(EntrySchema, { key, agent: a, state: EntryState.WAITING, position: i + 1 })),
    ],
  });
}

export function proposal(id: number, title: string, state: string, votes: [string, string][]): Proposal {
  return create(ProposalSchema, {
    id: BigInt(id),
    title,
    proposalState: states[state] ?? ProposalState.UNSPECIFIED,
    author: 'builder',
    votes: votes.map(([agent, choice]) =>
      create(ProposalVoteSchema, { agent, voteChoice: choices[choice] ?? VoteChoice.UNSPECIFIED }),
    ),
  });
}
