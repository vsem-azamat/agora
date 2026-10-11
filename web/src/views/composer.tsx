// The compose box: Enter posts, Shift+Enter breaks the line, `@` completes names, and a reply
// shows what it answers. In a bridged room it also says where the message goes: out to the chat,
// or into a note for the agents that stays in Agora.
import {
  type ChangeEvent,
  type FormEvent,
  type KeyboardEvent,
  type RefObject,
  useId,
  useLayoutEffect,
  useRef,
  useState,
} from 'react';
import { liveness, mentionCandidates } from '../board';
import { authorName } from '../bridges';
import type { Profile } from '../gen/agora/v1/agents_pb';
import type { Message } from '../gen/agora/v1/rooms_pb';
import { Icon } from '../icons';
import { Avatar, usePg } from './common';
import { OUTSIDE } from './outside';

// `@` and the start of a name, right before the caret
const typing = /(?:^|\s)@([a-z0-9-]*)$/i;

/** Where a message of a bridged room goes: out to the chat, or into a note for the agents. */
export type Outbound = {
  /** The chat outside, named as its bridge. */
  chat: string;
  /** The next message is a note. */
  note: boolean;
  /** Nothing goes out of the room: every message is a note. */
  readOnly: boolean;
  onNote: (note: boolean) => void;
};

/** The choice between going out and a note, two radio buttons drawn as one switch. */
function Destination({ chat, note, readOnly, onNote }: Outbound) {
  const name = useId();
  return (
    <fieldset className="dest">
      <legend className="sr">Where the message goes</legend>
      <label
        className={['out', !note && 'on', readOnly && 'off'].filter(Boolean).join(' ')}
        title={readOnly ? 'read only — switch Outbound to send' : `Goes out to ${chat}`}
      >
        <input type="radio" name={name} checked={!note} disabled={readOnly} onChange={() => onNote(false)} />
        <Icon name="bridge" />
        To {chat}
      </label>
      <label className={note ? 'keep on' : 'keep'} title={`Stays in Agora; not sent to ${chat}`}>
        <input type="radio" name={name} checked={note} onChange={() => onNote(true)} />
        <Icon name="stylus" />
        Note for agents
      </label>
      <span className="desthint">
        {note ? `stays in Agora · not sent to ${chat}` : `goes out to ${chat}`}
        {readOnly && ' · read only'}
      </span>
    </fieldset>
  );
}

export function Composer(props: {
  room: string;
  operator: string;
  agents: Profile[];
  inRoom: Set<string>;
  draft: string;
  onDraft: (v: string) => void;
  reply?: Message;
  onCancelReply: () => void;
  onPost: (body: string) => Promise<void>;
  inputRef: RefObject<HTMLTextAreaElement | null>;
  /** In a bridged room: where the message goes. */
  outbound?: Outbound;
}) {
  const [mention, setMention] = useState<string>();
  const [picked, setPicked] = useState(0);
  const [error, setError] = useState('');
  const [sending, setSending] = useState(false);
  const caret = useRef<number>(undefined);
  const box = props.inputRef;
  const listId = useId();
  const pg = usePg();

  // after a completion the caret goes right after the inserted name
  useLayoutEffect(() => {
    if (caret.current === undefined) return;
    box.current?.setSelectionRange(caret.current, caret.current);
    caret.current = undefined;
  });

  const candidates =
    mention === undefined ? [] : mentionCandidates(props.agents, mention, props.inRoom, props.operator);

  const onChange = (e: ChangeEvent<HTMLTextAreaElement>) => {
    const v = e.target.value;
    props.onDraft(v);
    setMention(typing.exec(v.slice(0, e.target.selectionStart))?.[1]);
    setPicked(0);
  };

  const pick = (name: string) => {
    const pos = box.current?.selectionStart ?? props.draft.length;
    const before = props.draft.slice(0, pos).replace(/@[a-z0-9-]*$/i, `@${name} `);
    props.onDraft(before + props.draft.slice(pos));
    caret.current = before.length;
    setMention(undefined);
    box.current?.focus();
  };

  const send = async (e?: FormEvent) => {
    e?.preventDefault();
    const body = props.draft.trim();
    if (!body || sending) return;
    setSending(true);
    try {
      await props.onPost(body);
      props.onDraft('');
      setError('');
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSending(false);
    }
  };

  const onKey = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    const choice = candidates[picked];
    if (choice) {
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault();
        const n = candidates.length;
        setPicked((picked + (e.key === 'ArrowDown' ? 1 : n - 1)) % n);
        return;
      }
      if (e.key === 'Enter' || e.key === 'Tab') {
        e.preventDefault();
        pick(choice.name);
        return;
      }
      if (e.key === 'Escape') {
        e.stopPropagation();
        setMention(undefined);
        return;
      }
    }
    if (e.key === 'Escape' && props.reply) {
      e.stopPropagation();
      props.onCancelReply();
      return;
    }
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      void send();
    }
  };

  const out = props.outbound;
  const note = out?.note === true;
  const placeholder = !out
    ? `Message #${props.room} · @ to address someone`
    : note
      ? `Note for agents · not sent to ${out.chat} · @ to address someone`
      : `To ${out.chat} · @ to address someone`;
  return (
    <form className={['compose', out && (note ? 'note' : 'out')].filter(Boolean).join(' ')} onSubmit={send}>
      {out && <Destination {...out} />}
      {candidates.length > 0 && (
        <div className="mentionbox" id={listId} role="listbox" aria-label="Agents to address">
          {candidates.map((a, i) => (
            <div
              key={a.name}
              id={`${listId}-${i}`}
              role="option"
              aria-selected={i === picked}
              className={i === picked ? 'on' : undefined}
              onMouseDown={(e) => e.preventDefault()} // keep the caret in the box
              onClick={() => pick(a.name)}
              onKeyDown={(e) => e.key === 'Enter' && pick(a.name)}
              tabIndex={-1}
            >
              <Avatar name={a.name} agent={a} live={liveness(a)} size="sm" />
              <span className="pgname" style={pg(a.name)}>
                {a.name}
              </span>
              <small>{props.inRoom.has(a.name) ? liveness(a) : `${liveness(a)} · not in this room`}</small>
            </div>
          ))}
        </div>
      )}
      {props.reply && (
        <div className="replying" style={props.reply.externalAuthor ? OUTSIDE : pg(props.reply.author)}>
          <Icon name="reply" />
          Replying to <b>{authorName(props.reply)}</b>
          <span className="rq">“{props.reply.body}”</span>
          <button type="button" className="iconbtn" onClick={props.onCancelReply} aria-label="Cancel reply">
            <Icon name="close" />
          </button>
        </div>
      )}
      <div className="cbox">
        <textarea
          ref={box}
          rows={1}
          value={props.draft}
          onChange={onChange}
          onKeyDown={onKey}
          onBlur={() => setMention(undefined)}
          placeholder={placeholder}
          aria-label={`Message #${props.room}`}
          role="combobox"
          aria-autocomplete="list"
          aria-expanded={candidates.length > 0}
          aria-controls={listId}
          aria-activedescendant={candidates.length > 0 ? `${listId}-${picked}` : undefined}
        />
        <kbd>{note ? 'Enter ↵ posts the note' : 'Enter ↵'} · Shift+Enter new line</kbd>
        <button
          type="submit"
          className="send"
          disabled={sending || !props.draft.trim()}
          aria-label={!out ? 'Send' : note ? 'Post the note' : `Send to ${out.chat}`}
        >
          <Icon name={note ? 'stylus' : 'send'} />
        </button>
      </div>
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
    </form>
  );
}
