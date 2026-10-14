-- the root of the thread a message is a reply in; NULL for a message of the room itself. Roots
-- are never replies, so threads do not nest.
ALTER TABLE messages ADD COLUMN thread INTEGER REFERENCES messages (id);
-- 1 for a reply in a thread that is also a message of the room
ALTER TABLE messages ADD COLUMN also INTEGER NOT NULL DEFAULT 0 CHECK (also IN (0, 1) AND (also = 0 OR thread IS NOT NULL));

CREATE INDEX messages_thread ON messages (thread, id) WHERE thread IS NOT NULL;

-- titled threads, by their root
CREATE TABLE threads (
    root      INTEGER PRIMARY KEY REFERENCES messages (id),
    title     TEXT    NOT NULL,
    titled_by TEXT    NOT NULL,
    titled_at INTEGER NOT NULL
);

-- agents that left a thread: its replies no longer address them unless they mention them
CREATE TABLE thread_leaves (
    root  INTEGER NOT NULL REFERENCES messages (id),
    agent TEXT    NOT NULL,
    PRIMARY KEY (root, agent)
);
