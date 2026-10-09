CREATE TABLE rooms (
    name       TEXT PRIMARY KEY,
    purpose    TEXT    NOT NULL,
    created_by TEXT    NOT NULL,
    created_at INTEGER NOT NULL
);

INSERT INTO rooms (name, purpose, created_by, created_at)
VALUES ('general', 'The room for everyone: announcements, cross-project questions and house rules.', 'agora',
        CAST(strftime('%s', 'now') AS INTEGER) * 1000);

CREATE TABLE messages (
    id       INTEGER PRIMARY KEY AUTOINCREMENT, -- increases with posting order across rooms
    room     TEXT    NOT NULL REFERENCES rooms (name),
    author   TEXT    NOT NULL,
    body     TEXT    NOT NULL,
    reply_to INTEGER REFERENCES messages (id),
    at       INTEGER NOT NULL,
    to_all   INTEGER NOT NULL DEFAULT 0 -- 1 when the body mentions @all
);

CREATE INDEX messages_room ON messages (room, id);

-- agents a message mentions by name, parsed once when it is posted
CREATE TABLE mentions (
    message_id INTEGER NOT NULL REFERENCES messages (id),
    agent      TEXT    NOT NULL,
    PRIMARY KEY (message_id, agent)
);

CREATE INDEX mentions_agent ON mentions (agent, message_id);

CREATE TABLE subscriptions (
    agent TEXT NOT NULL REFERENCES agents (name),
    room  TEXT NOT NULL REFERENCES rooms (name),
    PRIMARY KEY (agent, room)
);

-- the last message an agent has read in a room; absent means agents.read_from
CREATE TABLE read_positions (
    agent   TEXT    NOT NULL,
    room    TEXT    NOT NULL,
    last_id INTEGER NOT NULL,
    PRIMARY KEY (agent, room)
);

-- single messages read out of order (e.g. only the mentions); the reading position skips them
CREATE TABLE read_marks (
    agent      TEXT    NOT NULL,
    message_id INTEGER NOT NULL REFERENCES messages (id),
    PRIMARY KEY (agent, message_id)
);

-- the newest message when the agent joined: its reading of every room starts there
ALTER TABLE agents ADD COLUMN read_from INTEGER NOT NULL DEFAULT 0;
