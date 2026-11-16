-- messages that showed in their room after the agent's reading position there had passed them
-- (an echo kept out until its message was answered): unread for the agent until it reads them
CREATE TABLE shown_late (
    agent      TEXT    NOT NULL,
    message_id INTEGER NOT NULL REFERENCES messages (id),
    PRIMARY KEY (agent, message_id)
);
