-- a program the hub runs that connects a room to a chat outside Agora; the bridge has the
-- name of its room
CREATE TABLE bridges (
    name       TEXT PRIMARY KEY REFERENCES rooms (name),
    command    TEXT    NOT NULL, -- run with sh -c
    policy     TEXT    NOT NULL DEFAULT 'approve' CHECK (policy IN ('approve', 'open', 'read')),
    created_by TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    cursor     TEXT    NOT NULL DEFAULT '' -- the latest cursor the bridge reported, passed in hello
);

-- the agents a bridge was added with: a message from outside marked addressed mentions them
CREATE TABLE bridge_agents (
    bridge TEXT NOT NULL REFERENCES bridges (name) ON DELETE CASCADE,
    agent  TEXT NOT NULL REFERENCES agents (name),
    PRIMARY KEY (bridge, agent)
);

-- the message's identifier outside: of a message that came in, or the one the bridge reported
-- for a message that went out; unique per room
ALTER TABLE messages ADD COLUMN ext_id TEXT;
-- set for a message from outside, whose author is then ''; its bridge is its room
ALTER TABLE messages ADD COLUMN ext_author_id TEXT;
ALTER TABLE messages ADD COLUMN ext_author_name TEXT;
-- for a message of a bridged room that goes out: pending (waits for the operator), sending
-- (the bridge has to send it), sent, declined or failed; NULL for a message that stays here
ALTER TABLE messages ADD COLUMN delivery TEXT CHECK (delivery IN ('pending', 'sending', 'sent', 'declined', 'failed'));
-- why a failed message was not sent
ALTER TABLE messages ADD COLUMN delivery_error TEXT;

CREATE UNIQUE INDEX messages_ext ON messages (room, ext_id) WHERE ext_id IS NOT NULL;
CREATE INDEX messages_delivery ON messages (room, id) WHERE delivery = 'sending';
