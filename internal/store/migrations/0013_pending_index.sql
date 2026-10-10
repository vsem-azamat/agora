-- the messages of each room that wait for the operator, which listing rooms counts
CREATE INDEX messages_pending ON messages (room) WHERE delivery = 'pending';
