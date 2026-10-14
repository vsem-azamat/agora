-- threads of a bridged room mapped to their key outside, such as a forum topic; the first
-- message that came in with a key is the thread's root
CREATE TABLE thread_keys (
    room TEXT    NOT NULL REFERENCES rooms (name),
    key  TEXT    NOT NULL,
    root INTEGER NOT NULL REFERENCES messages (id),
    PRIMARY KEY (room, key)
);

-- a thread has at most one key, so a message goes out with one
CREATE UNIQUE INDEX thread_keys_root ON thread_keys (root);
