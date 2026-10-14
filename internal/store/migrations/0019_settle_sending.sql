-- a message left sending in a room whose bridge was removed waited forever: no bridge answers for
-- it any more. It is marked not sent, as removing a bridge now does.

UPDATE messages SET delivery = 'failed', delivery_error = 'the bridge was removed before it answered; it may have gone out'
WHERE delivery = 'sending' AND room NOT IN (SELECT name FROM bridges);
