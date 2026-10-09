// Package rooms keeps rooms, the messages posted in them, who follows which room, and what
// each agent has read.
package rooms

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// General is the room for everyone; every agent follows it.
	General = "general"
	// Board is the author name of messages the board posts itself.
	Board = "agora"
	// MaxBody is the longest message, in characters.
	MaxBody = 8000
	// DefaultHistory is how many messages a room's history shows by default.
	DefaultHistory = 20
)

var (
	// ErrInvalid marks an invalid room name, message or reference.
	ErrInvalid = errors.New("invalid request")
	// ErrNotFound means the room or agent does not exist.
	ErrNotFound = errors.New("not found")
	// ErrExists means the room name is taken.
	ErrExists = errors.New("room exists")
)

var (
	roomRE = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)
	// A mention: @name not preceded by a letter, digit or address character, in any script. The
	// name is matched greedily on the lowercased body, so the character after it is never part
	// of a name.
	mentionRE = regexp.MustCompile(`(?:^|[^\p{L}\p{N}._@-])@([a-z][a-z0-9-]*)`)
)

// Room is a room with its activity.
type Room struct {
	Name      string
	Purpose   string
	CreatedBy string
	CreatedAt time.Time
	Messages  int
	LastAt    time.Time // zero when the room has no messages
}

// Message is a posted message.
type Message struct {
	ID        int64
	Room      string
	Author    string
	Body      string
	ReplyTo   int64 // 0 when the message replies to nothing
	At        time.Time
	Addressed bool // set in unread lists: the message is addressed to the reader
}

// Rooms keeps rooms and messages in the hub database.
type Rooms struct {
	db  *sql.DB
	now func() time.Time
}

// New returns Rooms over db; now is the clock (time.Now when nil).
func New(db *sql.DB, now func() time.Time) *Rooms {
	if now == nil {
		now = time.Now
	}
	return &Rooms{db: db, now: now}
}

// Mentions returns the names a message body mentions, without duplicates, and whether it
// mentions @all.
func Mentions(body string) (names []string, all bool) {
	seen := map[string]bool{}
	for _, m := range mentionRE.FindAllStringSubmatch(strings.ToLower(body), -1) {
		name := strings.TrimRight(m[1], "-") // `@builder-` at the end of a phrase
		if name == "all" {
			all = true
			continue
		}
		if len(name) < 2 || len(name) > 32 || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names, all
}

// Create makes a room and subscribes its creator, all at once.
func (r *Rooms) Create(ctx context.Context, name, purpose, creator string) error {
	if !roomRE.MatchString(name) {
		return fmt.Errorf("%w: room name %q: 2-32 lowercase letters, digits and dashes, starting with a letter", ErrInvalid, name)
	}
	purpose = strings.TrimSpace(purpose)
	if purpose == "" {
		return fmt.Errorf("%w: a room needs a purpose", ErrInvalid)
	}
	return r.tx(ctx, func(tx *sql.Tx) error {
		if err := requireAgent(ctx, tx, creator); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM rooms WHERE name = ?`, name).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("%w: #%s already exists", ErrExists, name)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO rooms (name, purpose, created_by, created_at) VALUES (?, ?, ?, ?)`,
			name, purpose, creator, r.now().UnixMilli()); err != nil {
			return err
		}
		return subscribe(ctx, tx, creator, name)
	})
}

// List returns every room in name order.
func (r *Rooms) List(ctx context.Context) ([]Room, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT r.name, r.purpose, r.created_by, r.created_at,
		(SELECT COUNT(*) FROM messages WHERE room = r.name), COALESCE((SELECT MAX(at) FROM messages WHERE room = r.name), 0)
		FROM rooms AS r ORDER BY r.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Room
	for rows.Next() {
		var x Room
		var created, last int64
		if err := rows.Scan(&x.Name, &x.Purpose, &x.CreatedBy, &created, &x.Messages, &last); err != nil {
			return nil, err
		}
		x.CreatedAt = time.UnixMilli(created)
		if last > 0 {
			x.LastAt = time.UnixMilli(last)
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// Subscribe makes agent follow (add) or stop following rooms; #general stays followed. It
// returns the rooms the agent follows afterwards.
func (r *Rooms) Subscribe(ctx context.Context, agent string, rooms []string, add bool) ([]string, error) {
	var out []string
	err := r.tx(ctx, func(tx *sql.Tx) error {
		if err := requireAgent(ctx, tx, agent); err != nil {
			return err
		}
		for _, room := range rooms {
			room = strings.TrimPrefix(room, "#")
			if err := requireRoom(ctx, tx, room); err != nil {
				return err
			}
			if room == General {
				continue
			}
			var err error
			if add {
				err = subscribe(ctx, tx, agent, room)
			} else {
				_, err = tx.ExecContext(ctx, `DELETE FROM subscriptions WHERE agent = ? AND room = ?`, agent, room)
			}
			if err != nil {
				return err
			}
		}
		var err error
		out, err = followed(ctx, tx, agent)
		return err
	})
	return out, err
}

// Followed returns the rooms agent follows, #general first.
func (r *Rooms) Followed(ctx context.Context, agent string) ([]string, error) {
	var out []string
	err := r.tx(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = followed(ctx, tx, agent)
		return err
	})
	return out, err
}

// FollowedTx is Followed inside the caller's transaction.
func (r *Rooms) FollowedTx(ctx context.Context, tx *sql.Tx, agent string) ([]string, error) {
	return followed(ctx, tx, agent)
}

// Post stores a message from author (a joined agent, or the board itself) and returns its
// identifier.
func (r *Rooms) Post(ctx context.Context, author, room, body string, replyTo int64) (int64, error) {
	room = strings.TrimPrefix(room, "#")
	body = strings.TrimSpace(body)
	if body == "" || utf8.RuneCountInString(body) > MaxBody {
		return 0, fmt.Errorf("%w: a message is 1 to %d characters", ErrInvalid, MaxBody)
	}
	var id int64
	err := r.tx(ctx, func(tx *sql.Tx) error {
		var err error
		id, err = r.PostTx(ctx, tx, author, room, body, replyTo)
		return err
	})
	return id, err
}

// PostTx is Post inside the caller's transaction, so other packages can post atomically with
// their own changes.
func (r *Rooms) PostTx(ctx context.Context, tx *sql.Tx, author, room, body string, replyTo int64) (int64, error) {
	room = strings.TrimPrefix(room, "#")
	body = strings.TrimSpace(body)
	if body == "" || utf8.RuneCountInString(body) > MaxBody {
		return 0, fmt.Errorf("%w: a message is 1 to %d characters", ErrInvalid, MaxBody)
	}
	names, all := Mentions(body)
	var id int64
	err := func() error {
		if author != Board {
			if err := requireAgent(ctx, tx, author); err != nil {
				return err
			}
		}
		if err := requireRoom(ctx, tx, room); err != nil {
			return err
		}
		if replyTo != 0 {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE id = ?`, replyTo).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				return fmt.Errorf("%w: no message %d to reply to", ErrInvalid, replyTo)
			}
		}
		var reply any
		if replyTo != 0 {
			reply = replyTo
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO messages (room, author, body, reply_to, at, to_all) VALUES (?, ?, ?, ?, ?, ?)`,
			room, author, body, reply, r.now().UnixMilli(), all)
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		for _, n := range names {
			if _, err := tx.ExecContext(ctx, `INSERT INTO mentions (message_id, agent) VALUES (?, ?)`, id, n); err != nil {
				return err
			}
		}
		return nil
	}()
	return id, err
}

// History returns the last messages of room, oldest first; last <= 0 means DefaultHistory.
func (r *Rooms) History(ctx context.Context, room string, last int) ([]Message, error) {
	room = strings.TrimPrefix(room, "#")
	if last <= 0 {
		last = DefaultHistory
	}
	var out []Message
	err := r.tx(ctx, func(tx *sql.Tx) error {
		if err := requireRoom(ctx, tx, room); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id, room, author, body, COALESCE(reply_to, 0), at, 0 FROM
			(SELECT * FROM messages WHERE room = ? ORDER BY id DESC LIMIT ?) ORDER BY id`, room, last)
		if err != nil {
			return err
		}
		out, err = scanMessages(rows)
		return err
	})
	return out, err
}

// Unread returns up to limit of agent's unread messages, oldest first, and how many there are
// in all; with mentionsOnly, only messages addressed to it. limit <= 0 means no limit.
func (r *Rooms) Unread(ctx context.Context, agent string, mentionsOnly bool, limit int) ([]Message, int, error) {
	var out []Message
	var total int
	err := r.tx(ctx, func(tx *sql.Tx) error {
		var err error
		out, total, err = unread(ctx, tx, agent, mentionsOnly, limit)
		return err
	})
	return out, total, err
}

// UnreadCount returns how many unread messages agent has; with mentionsOnly, only those
// addressed to it.
func (r *Rooms) UnreadCount(ctx context.Context, agent string, mentionsOnly bool) (int, error) {
	var n int
	err := r.tx(ctx, func(tx *sql.Tx) error {
		if err := requireAgent(ctx, tx, agent); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+unreadQuery+`) WHERE addressed OR NOT :m`,
			sql.Named("a", agent), sql.Named("m", mentionsOnly)).Scan(&n)
	})
	return n, err
}

// Take returns what Unread returns and marks those messages read in the same transaction, so
// concurrent readers never get the same message twice: the oldest unread messages move the
// reading positions, mentions only get single read marks.
func (r *Rooms) Take(ctx context.Context, agent string, mentionsOnly bool, limit int) ([]Message, int, error) {
	var out []Message
	var total int
	err := r.tx(ctx, func(tx *sql.Tx) error {
		var err error
		if out, total, err = unread(ctx, tx, agent, mentionsOnly, limit); err != nil {
			return err
		}
		if mentionsOnly {
			return markEach(ctx, tx, agent, out)
		}
		return markRead(ctx, tx, agent, out)
	})
	return out, total, err
}

// unreadQuery finds unread messages through the indexes: messages in followed rooms after the
// reading position (messages_room), and messages mentioning the agent by name (mentions_agent).
const unreadQuery = `
WITH followed(room) AS (
	SELECT 'general' UNION SELECT room FROM subscriptions WHERE agent = :a
), start AS (
	SELECT read_from FROM agents WHERE name = :a
), candidates AS (
	SELECT m.id, 1 AS followed FROM followed AS f
	JOIN messages AS m ON m.room = f.room
		AND m.id > COALESCE((SELECT last_id FROM read_positions WHERE agent = :a AND room = f.room), (SELECT read_from FROM start))
	UNION
	SELECT m.id, EXISTS (SELECT 1 FROM followed WHERE room = m.room) FROM mentions AS mm
	JOIN messages AS m ON m.id = mm.message_id
	WHERE mm.agent = :a
		AND m.id > COALESCE((SELECT last_id FROM read_positions WHERE agent = :a AND room = m.room), (SELECT read_from FROM start))
)
SELECT m.id, m.room, m.author, m.body, COALESCE(m.reply_to, 0), m.at,
	(EXISTS (SELECT 1 FROM mentions WHERE message_id = m.id AND agent = :a) OR (m.to_all AND MAX(c.followed))) AS addressed
FROM candidates AS c JOIN messages AS m ON m.id = c.id
WHERE m.author != :a AND NOT EXISTS (SELECT 1 FROM read_marks WHERE agent = :a AND message_id = m.id)
GROUP BY m.id
ORDER BY m.id`

func unread(ctx context.Context, tx *sql.Tx, agent string, mentionsOnly bool, limit int) ([]Message, int, error) {
	if err := requireAgent(ctx, tx, agent); err != nil {
		return nil, 0, err
	}
	rows, err := tx.QueryContext(ctx, unreadQuery, sql.Named("a", agent))
	if err != nil {
		return nil, 0, err
	}
	all, err := scanMessages(rows)
	if err != nil {
		return nil, 0, err
	}
	if mentionsOnly {
		var addressed []Message
		for _, m := range all {
			if m.Addressed {
				addressed = append(addressed, m)
			}
		}
		all = addressed
	}
	total := len(all)
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	return all, total, nil
}

// MarkEach marks single messages read without moving the reading position, so earlier unread
// messages in the same rooms stay unread.
func (r *Rooms) MarkEach(ctx context.Context, agent string, msgs []Message) error {
	return r.tx(ctx, func(tx *sql.Tx) error { return markEach(ctx, tx, agent, msgs) })
}

func markEach(ctx context.Context, tx *sql.Tx, agent string, msgs []Message) error {
	for _, m := range msgs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO read_marks (agent, message_id) VALUES (?, ?) ON CONFLICT DO NOTHING`, agent, m.ID); err != nil {
			return err
		}
	}
	return nil
}

// MarkRead moves agent's reading position in each message's room past that message; positions
// never move backwards. msgs must be the oldest unread messages, as Unread returns them, so no
// unread message is skipped.
func (r *Rooms) MarkRead(ctx context.Context, agent string, msgs []Message) error {
	return r.tx(ctx, func(tx *sql.Tx) error { return markRead(ctx, tx, agent, msgs) })
}

func markRead(ctx context.Context, tx *sql.Tx, agent string, msgs []Message) error {
	if len(msgs) == 0 {
		return nil
	}
	for _, m := range msgs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO read_positions (agent, room, last_id) VALUES (?, ?, ?)
			ON CONFLICT (agent, room) DO UPDATE SET last_id = MAX(last_id, excluded.last_id)`, agent, m.Room, m.ID); err != nil {
			return err
		}
	}
	// single marks behind a reading position are no longer needed
	_, err := tx.ExecContext(ctx, `DELETE FROM read_marks WHERE agent = ? AND message_id <=
		COALESCE((SELECT p.last_id FROM read_positions AS p JOIN messages AS m ON m.room = p.room
			WHERE p.agent = read_marks.agent AND m.id = read_marks.message_id), 0)`, agent)
	return err
}

// --- internals -------------------------------------------------------------------

func (r *Rooms) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func requireAgent(ctx context.Context, tx *sql.Tx, agent string) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM agents WHERE name = ?`, agent).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: %q has not joined; run `agora join %s` first", ErrNotFound, agent, agent)
	}
	return nil
}

func requireRoom(ctx context.Context, tx *sql.Tx, room string) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM rooms WHERE name = ?`, room).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: no room #%s; see `agora rooms`, or create it with `agora room-create`", ErrNotFound, room)
	}
	return nil
}

// subscribe makes agent follow room. Reading of the room starts at its newest message unless
// the agent already follows it; a position left from reading a mention there moves forward too.
func subscribe(ctx context.Context, tx *sql.Tx, agent, room string) error {
	res, err := tx.ExecContext(ctx, `INSERT INTO subscriptions (agent, room) VALUES (?, ?) ON CONFLICT DO NOTHING`, agent, room)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil // already followed: keep reading where it is
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO read_positions (agent, room, last_id)
		VALUES (?, ?, (SELECT COALESCE(MAX(id), 0) FROM messages WHERE room = ?))
		ON CONFLICT (agent, room) DO UPDATE SET last_id = MAX(last_id, excluded.last_id)`, agent, room, room)
	return err
}

func followed(ctx context.Context, tx *sql.Tx, agent string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT room FROM subscriptions WHERE agent = ? AND room != 'general' ORDER BY room`, agent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{General}
	for rows.Next() {
		var room string
		if err := rows.Scan(&room); err != nil {
			return nil, err
		}
		out = append(out, room)
	}
	return out, rows.Err()
}

func scanMessages(rows *sql.Rows) ([]Message, error) {
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var at int64
		if err := rows.Scan(&m.ID, &m.Room, &m.Author, &m.Body, &m.ReplyTo, &at, &m.Addressed); err != nil {
			return nil, err
		}
		m.At = time.UnixMilli(at)
		out = append(out, m)
	}
	return out, rows.Err()
}

// Format renders a message for an agent: room, identifier, author, time and reply reference,
// then the body indented, shortened to limit characters when limit > 0.
func Format(m Message, limit int) string {
	body := m.Body
	if limit > 0 && utf8.RuneCountInString(body) > limit {
		body = strings.TrimSpace(string([]rune(body)[:limit])) + " …"
	}
	head := fmt.Sprintf("#%s [%d] %s · %s", m.Room, m.ID, m.Author, m.At.Local().Format("15:04"))
	if m.ReplyTo != 0 {
		head += fmt.Sprintf(" · re %d", m.ReplyTo)
	}
	if m.Addressed {
		head += " · to you"
	}
	return head + "\n  " + strings.ReplaceAll(body, "\n", "\n  ")
}
