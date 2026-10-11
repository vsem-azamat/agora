-- foreign keys: off
-- the delivery states take the names they are shown under: a scheduled message waits for its
-- delay, a cancelled one never goes out. SQLite cannot change a CHECK, so messages is built
-- again with foreign keys off, as SQLite prescribes; the rows of mentions and read_marks point at
-- the same ids, and the foreign keys are checked before the migration commits.

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
    delivery        TEXT CHECK (delivery IN ('scheduled', 'sending', 'sent', 'cancelled', 'failed')),
    delivery_error  TEXT,
    quiet           INTEGER NOT NULL DEFAULT 0,
    note            INTEGER NOT NULL DEFAULT 0 CHECK (note IN (0, 1) AND (note = 0 OR delivery IS NULL)),
    scheduled_at    INTEGER
);

INSERT INTO messages_new (id, room, author, body, reply_to, at, to_all, ext_id, ext_author_id, ext_author_name,
                          delivery, delivery_error, quiet, note, scheduled_at)
SELECT id, room, author, body, reply_to, at, to_all, ext_id, ext_author_id, ext_author_name,
       CASE delivery WHEN 'pending' THEN 'scheduled' WHEN 'declined' THEN 'cancelled' ELSE delivery END,
       delivery_error, quiet, note, scheduled_at
FROM messages;

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
-- the messages of each room that are scheduled, which listing rooms counts
CREATE INDEX messages_scheduled ON messages (room) WHERE delivery = 'scheduled';
