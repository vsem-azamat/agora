-- pictures that stand for agents and people outside, drawn again by the hub; stored once under
-- the SHA-256 of data
CREATE TABLE avatars (
    hash       TEXT PRIMARY KEY, -- lowercase hex
    mime       TEXT    NOT NULL CHECK (mime IN ('image/png', 'image/jpeg', 'image/svg+xml')),
    data       BLOB    NOT NULL, -- at most 256 pixels a side; an SVG as written anew
    small      BLOB,             -- the same at 128 pixels a side, for a picture larger than that
    w          INTEGER NOT NULL, -- pixels of data; 0 for an SVG
    h          INTEGER NOT NULL,
    created_at INTEGER NOT NULL
);

-- the agent's avatar; NULL when it has none
ALTER TABLE agents ADD COLUMN avatar TEXT REFERENCES avatars (hash);

-- the avatars a bridge sent for people outside, by the room (the bridge's name) and the author's
-- id there, as stored in messages.ext_author_id; kept when the bridge is removed, like its messages
CREATE TABLE outside_avatars (
    room       TEXT    NOT NULL REFERENCES rooms (name),
    author     TEXT    NOT NULL,
    rev        TEXT    NOT NULL,          -- the bridge's name for the picture
    avatar     TEXT REFERENCES avatars (hash), -- NULL: the person has none
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (room, author)
);

CREATE INDEX outside_avatars_avatar ON outside_avatars (avatar);
CREATE INDEX agents_avatar ON agents (avatar);
