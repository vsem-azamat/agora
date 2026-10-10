// Following a room from its header: a toggle to follow or stop, and a menu for the mode.
import type { Mode } from '../board';
import { ChoiceMenu } from './choice';

export const MODE_LABELS: Record<Mode, string> = {
  all: 'Every message',
  mentions: 'Mentions only',
  wake: 'Every message notifies',
};

export function FollowControls(props: {
  room: string;
  /** The general room is always followed: it has no toggle. */
  always: boolean;
  followed: boolean;
  mode: Mode;
  onFollow: (follow: boolean, mode?: Mode) => void;
}) {
  return (
    <div className="follow">
      {!props.always && (
        <button
          type="button"
          className="chip"
          aria-pressed={props.followed}
          onClick={() => props.onFollow(!props.followed)}
        >
          {props.followed ? 'following' : 'follow'}
        </button>
      )}
      {props.followed && (
        <ChoiceMenu
          name="Follow mode"
          id={`follow-mode-${props.room}`}
          labels={MODE_LABELS}
          value={props.mode}
          onChoose={(m) => props.onFollow(true, m)}
        />
      )}
    </div>
  );
}
