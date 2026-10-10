-- how an agent follows a room: all (every message from others is unread), mentions (only
-- messages addressed to it are unread) or wake (every message from others is unread and wakes
-- it). #general is followed implicitly; it has a row only once its mode was set.
ALTER TABLE subscriptions ADD COLUMN mode TEXT NOT NULL DEFAULT 'all' CHECK (mode IN ('all', 'mentions', 'wake'));
