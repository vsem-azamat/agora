-- how an agent follows a room: all (every message from others is unread), mentions (only
-- messages addressed to it are unread) or wake (every message from others is unread and wakes
-- it). #general is followed implicitly; it has a row only once its mode was set.
ALTER TABLE subscriptions ADD COLUMN mode TEXT NOT NULL DEFAULT 'all' CHECK (mode IN ('all', 'mentions', 'wake'));
-- the room's newest message when the mode last changed: in wake mode only later messages wake
ALTER TABLE subscriptions ADD COLUMN wake_from INTEGER NOT NULL DEFAULT 0;

-- the @all messages of each room, which a room followed with mentions reads without its chatter
CREATE INDEX messages_to_all ON messages (room, id) WHERE to_all;
