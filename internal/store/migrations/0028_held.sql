-- foreign keys: off
-- a message that may go out but waits for the message it replies to is held, a delivery state of
-- its own, and the hub marks when it hands a message to the bridge (handed_at), so a bridge
-- removed before it took a message leaves it not sent rather than unconfirmed. SQLite cannot
-- change a CHECK, so messages is built again with foreign keys off, as 0024 did.
--
-- A message sending whose reply_to, or for a reply whose replied-to message has no identifier
-- outside the thread's root, is still sending without one is held back by the hub now: it becomes
-- held, and is handed once that message is answered. (One the hub handed before its parent went
-- from scheduled to sending is held too: it is handed again then, as a restart did.) Every other
-- message sending may have been handed: it is marked handed now.

CREATE TABLE messages_new (
    id              INTEGER PRIMARY KEY AUTOINCREMENT, -- increases with posting order across rooms
    room            TEXT    NOT NULL REFERENCES rooms (name),
    author          TEXT    NOT NULL,
    body            TEXT    NOT NULL,
    reply_to        INTEGER REFERENCES messages (id),
    at              INTEGER NOT NULL,
    to_all          INTEGER NOT NULL DEFAULT 0, -- 1 when the body mentions @all
    ext_id          TEXT,
    ext_author_id   TEXT,
    ext_author_name TEXT,
    delivery        TEXT CHECK (delivery IN ('scheduled', 'held', 'sending', 'sent', 'cancelled', 'failed', 'unconfirmed')),
    delivery_error  TEXT,
    quiet           INTEGER NOT NULL DEFAULT 0,
    note            INTEGER NOT NULL DEFAULT 0 CHECK (note IN (0, 1) AND (note = 0 OR delivery IS NULL)),
    scheduled_at    INTEGER,
    thread          INTEGER REFERENCES messages (id),
    also            INTEGER NOT NULL DEFAULT 0 CHECK (also IN (0, 1) AND (also = 0 OR thread IS NOT NULL)),
    echo_of         INTEGER REFERENCES messages (id),
    sent_out        INTEGER REFERENCES messages (id),
    handed_at       INTEGER -- when the hub last handed the message to its bridge
);

INSERT INTO messages_new (id, room, author, body, reply_to, at, to_all, ext_id, ext_author_id, ext_author_name,
                          delivery, delivery_error, quiet, note, scheduled_at, thread, also, echo_of, sent_out, handed_at)
SELECT m.id, m.room, m.author, m.body, m.reply_to, m.at, m.to_all, m.ext_id, m.ext_author_id, m.ext_author_name,
       CASE WHEN m.delivery = 'sending' AND w.id IS NOT NULL THEN 'held' ELSE m.delivery END,
       m.delivery_error, m.quiet, m.note, m.scheduled_at, m.thread, m.also, m.echo_of, m.sent_out,
       CASE WHEN m.delivery = 'sending' AND w.id IS NULL THEN CAST(strftime('%s', 'now') AS INTEGER) * 1000 END
FROM messages AS m LEFT JOIN (
    SELECT c.id FROM messages AS c
    LEFT JOIN messages AS r ON r.id = c.reply_to AND r.room = c.room
    LEFT JOIN messages AS t ON t.id = c.thread
    WHERE c.delivery = 'sending'
        AND (COALESCE(r.delivery, '') = 'sending' AND r.ext_id IS NULL
            OR r.ext_id IS NULL AND COALESCE(t.delivery, '') = 'sending' AND t.ext_id IS NULL)
) AS w ON w.id = m.id;

-- the next id continues after the highest ever given, also when the newest messages were deleted
-- (merged echoes): the copy's sequence is only its highest id. The rename moves this row along.
DELETE FROM sqlite_sequence WHERE name = 'messages_new';
INSERT INTO sqlite_sequence (name, seq) SELECT 'messages_new', seq FROM sqlite_sequence WHERE name = 'messages';

DROP TABLE messages;
ALTER TABLE messages_new RENAME TO messages;

CREATE INDEX messages_room ON messages (room, id);
CREATE INDEX messages_to_all ON messages (room, id) WHERE to_all;
CREATE UNIQUE INDEX messages_ext ON messages (room, ext_id) WHERE ext_id IS NOT NULL;
CREATE INDEX messages_delivery ON messages (room, id) WHERE delivery = 'sending';
CREATE INDEX messages_scheduled ON messages (room) WHERE delivery = 'scheduled';
CREATE INDEX messages_held ON messages (room, id) WHERE delivery = 'held';
CREATE INDEX messages_thread ON messages (thread, id) WHERE thread IS NOT NULL;
CREATE INDEX messages_echo_of ON messages (echo_of) WHERE echo_of IS NOT NULL;
