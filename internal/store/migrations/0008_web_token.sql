-- the token the web app must present; at most one row
CREATE TABLE web_token (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    token      TEXT    NOT NULL,
    created_at INTEGER NOT NULL
);
