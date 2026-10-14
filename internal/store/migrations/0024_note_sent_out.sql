-- for a note the operator sent out, the message that went out from it, so sending it out again
-- sends nothing
ALTER TABLE messages ADD COLUMN sent_out INTEGER REFERENCES messages (id);
