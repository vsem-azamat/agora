-- a message that came in from the operator outside while a message of the operator with the same
-- text was still sending may be that message's echo: echo_of names it, and the message is left out
-- of the room (history, unread counts, room counts) until the bridge answers. The answer merges it
-- into the sent message when the identifier outside is the same, and clears echo_of otherwise.
ALTER TABLE messages ADD COLUMN echo_of INTEGER REFERENCES messages (id);

CREATE INDEX messages_echo_of ON messages (echo_of) WHERE echo_of IS NOT NULL;
