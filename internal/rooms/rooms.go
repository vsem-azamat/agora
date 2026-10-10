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

	"github.com/vsem-azamat/agora/internal/agents"
	"github.com/vsem-azamat/agora/internal/store"
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

// The name rule for rooms and agents. Rooms owns it because mentions, which rooms parses,
// name agents; sessions applies it to agent names.
const (
	// MinName and MaxName bound the length of a room or agent name.
	MinName, MaxName = 2, 32
	// NameRule describes a valid room or agent name.
	NameRule = "2-32 lowercase letters, digits and dashes, starting with a letter"
)

// nameRE is NameRule.
var nameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

// ValidName reports whether s may name a room or an agent.
func ValidName(s string) bool { return nameRE.MatchString(s) }

var (
	// ErrNotFound means the room does not exist.
	ErrNotFound = errors.New("not found")
	// ErrExists means the room name is taken.
	ErrExists = errors.New("room exists")
	// ErrBoardOnly refuses a post that claims to come from the board.
	ErrBoardOnly = errors.New("only the board itself posts as agora")
	// ErrReadOnly refuses an agent's post in a bridged room whose outbound policy is read.
	ErrReadOnly = errors.New("read-only bridge")
)

// A mention: @name not preceded by a letter, digit or address character, in any script. The
// name is matched greedily on the lowercased body, so the character after it is never part
// of a name.
var mentionRE = regexp.MustCompile(`(?:^|[^\p{L}\p{N}._@-])@([a-z][a-z0-9-]*)`)

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
	Author    string // "" for a message from outside
	Body      string
	ReplyTo   int64 // 0 when the message replies to nothing
	At        time.Time
	Addressed bool // set in unread lists: the message is addressed to the reader
	Wakes     bool // set in unread lists: the message wakes the reader (addressed, or in a room it follows with ModeWake)

	// ExtAuthorID and ExtAuthorName are set for a message from outside, which came through the
	// bridge of its room.
	ExtAuthorID, ExtAuthorName string
	// Delivery is where a message of a bridged room stands on its way out ("" when it stays
	// here), and DeliveryError why it failed.
	Delivery      Delivery
	DeliveryError string
}

// External reports whether the message came from outside through a bridge.
func (m Message) External() bool { return m.ExtAuthorName != "" }

// From names who wrote the message: its author, or name@bridge for a message from outside.
func (m Message) From() string {
	if m.External() {
		return m.ExtAuthorName + "@" + m.Room
	}
	return m.Author
}

// Delivery is where a message of a bridged room stands on its way out.
type Delivery string

// Delivery states.
const (
	Pending  Delivery = "pending"  // waits for the operator
	Sending  Delivery = "sending"  // the bridge has to send it
	Sent     Delivery = "sent"     // the bridge sent it
	Declined Delivery = "declined" // the operator declined it
	Failed   Delivery = "failed"   // the bridge could not send it
)

// Mode is how an agent follows a room.
type Mode string

const (
	// ModeAll makes every message from others unread; only addressed ones wake the agent.
	ModeAll Mode = "all"
	// ModeMentions makes only messages addressed to the agent unread, as in a room it does not follow.
	ModeMentions Mode = "mentions"
	// ModeWake makes every message from others unread and wake the agent.
	ModeWake Mode = "wake"
)

// Subscription is a room an agent follows and how.
type Subscription struct {
	Room string
	Mode Mode
}

// Filter narrows an agent's unread messages.
type Filter int

const (
	// Everything is every unread message.
	Everything Filter = iota
	// Addressed is the unread messages addressed to the agent.
	Addressed
	// Waking is the unread messages that wake the agent: those addressed to it and those in
	// rooms it follows with ModeWake.
	Waking
)

func (f Filter) keeps(m Message) bool {
	switch f {
	case Addressed:
		return m.Addressed
	case Waking:
		return m.Wakes
	}
	return true
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
		if len(name) < MinName || len(name) > MaxName || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names, all
}

// Create makes a room and subscribes its creator, all at once.
func (r *Rooms) Create(ctx context.Context, name, purpose, creator string) error {
	return store.InTx(ctx, r.db, func(tx *sql.Tx) error { return r.CreateTx(ctx, tx, name, purpose, creator) })
}

// CreateTx is Create inside the caller's transaction.
func (r *Rooms) CreateTx(ctx context.Context, tx *sql.Tx, name, purpose, creator string) error {
	if !ValidName(name) {
		return fmt.Errorf("%w: room name %q: %s", store.ErrInvalid, name, NameRule)
	}
	purpose = strings.TrimSpace(purpose)
	if purpose == "" {
		return fmt.Errorf("%w: a room needs a purpose", store.ErrInvalid)
	}
	if err := agents.ExistsTx(ctx, tx, creator); err != nil {
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
	return subscribe(ctx, tx, creator, name, "")
}

// SubscribeTx makes agent follow room, which must not be #general, with mode inside the
// caller's transaction, as Subscribe does.
func SubscribeTx(ctx context.Context, tx *sql.Tx, agent, room string, mode Mode) error {
	return subscribe(ctx, tx, agent, room, mode)
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

// Subscribe makes agent follow (add) or stop following rooms, at least one; #general stays
// followed. Following with a mode sets it; without one ("") a new subscription gets ModeAll and
// an existing one keeps its mode. mode is ignored when stopping. It returns the rooms the agent
// follows afterwards.
func (r *Rooms) Subscribe(ctx context.Context, agent string, rooms []string, add bool, mode Mode) ([]Subscription, error) {
	if len(rooms) == 0 {
		return nil, fmt.Errorf("%w: name at least one room", store.ErrInvalid)
	}
	switch {
	case !add:
		mode = ""
	case mode == "", mode == ModeAll, mode == ModeMentions, mode == ModeWake:
	default:
		return nil, fmt.Errorf("%w: mode %q: all, mentions or wake", store.ErrInvalid, mode)
	}
	var out []Subscription
	err := store.InTx(ctx, r.db, func(tx *sql.Tx) error {
		if err := agents.ExistsTx(ctx, tx, agent); err != nil {
			return err
		}
		for _, room := range rooms {
			room = strings.TrimPrefix(room, "#")
			if err := requireRoom(ctx, tx, room); err != nil {
				return err
			}
			var err error
			switch {
			case add:
				err = subscribe(ctx, tx, agent, room, mode)
			case room == General:
				// always followed; unsubscribing keeps it and its mode
			default:
				_, err = tx.ExecContext(ctx, `DELETE FROM subscriptions WHERE agent = ? AND room = ?`, agent, room)
			}
			if err != nil {
				return err
			}
		}
		var err error
		out, err = subscriptions(ctx, tx, agent)
		return err
	})
	return out, err
}

// Subscriptions returns the rooms agent follows with their modes, #general first.
func (r *Rooms) Subscriptions(ctx context.Context, agent string) ([]Subscription, error) {
	var out []Subscription
	err := store.InTx(ctx, r.db, func(tx *sql.Tx) error {
		if err := agents.ExistsTx(ctx, tx, agent); err != nil {
			return err
		}
		var err error
		out, err = subscriptions(ctx, tx, agent)
		return err
	})
	return out, err
}

// NoticeRoomTx returns the room where the board tells agent about its own work, inside the
// caller's transaction: the alphabetically first room it follows other than #general, else
// #general.
func NoticeRoomTx(ctx context.Context, tx *sql.Tx, agent string) (string, error) {
	rooms, err := followed(ctx, tx, agent)
	if err != nil || len(rooms) < 2 {
		return General, err
	}
	return rooms[1], nil
}

// Post stores a message from author, a joined agent, and returns its identifier. In a bridged
// room the message goes out under the room's outbound policy: it waits for the operator under
// approve, goes out under open and is refused under read. Only the board posts as Board,
// through PostTx.
func (r *Rooms) Post(ctx context.Context, author, room, body string, replyTo int64) (int64, error) {
	return r.post(ctx, author, room, body, replyTo, false)
}

// PostFromOperator is Post for the operator, through the web app: in a bridged room the message
// goes out at once, except under the policy read, where it stays here.
func (r *Rooms) PostFromOperator(ctx context.Context, author, room, body string, replyTo int64) (int64, error) {
	return r.post(ctx, author, room, body, replyTo, true)
}

func (r *Rooms) post(ctx context.Context, author, room, body string, replyTo int64, operator bool) (int64, error) {
	if author == Board {
		return 0, ErrBoardOnly
	}
	var id int64
	err := store.InTx(ctx, r.db, func(tx *sql.Tx) error {
		var err error
		id, err = r.postTx(ctx, tx, author, room, body, replyTo, operator)
		return err
	})
	return id, err
}

// PostTx stores a message from author (a joined agent, or the board itself) inside the caller's
// transaction, so other packages can post atomically with their own changes. The board's
// messages never go out through a bridge.
func (r *Rooms) PostTx(ctx context.Context, tx *sql.Tx, author, room, body string, replyTo int64) (int64, error) {
	return r.postTx(ctx, tx, author, room, body, replyTo, false)
}

func (r *Rooms) postTx(ctx context.Context, tx *sql.Tx, author, room, body string, replyTo int64, operator bool) (int64, error) {
	room = strings.TrimPrefix(room, "#")
	body = strings.TrimSpace(body)
	if body == "" || utf8.RuneCountInString(body) > MaxBody {
		return 0, fmt.Errorf("%w: a message is 1 to %d characters", store.ErrInvalid, MaxBody)
	}
	if author != Board {
		if err := agents.ExistsTx(ctx, tx, author); err != nil {
			return 0, err
		}
	}
	if err := requireRoom(ctx, tx, room); err != nil {
		return 0, err
	}
	if replyTo != 0 {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE id = ?`, replyTo).Scan(&n); err != nil {
			return 0, err
		}
		if n == 0 {
			return 0, fmt.Errorf("%w: no message %d to reply to", store.ErrInvalid, replyTo)
		}
	}
	var delivery Delivery
	if author != Board {
		var err error
		if delivery, err = outbound(ctx, tx, room, operator); err != nil {
			return 0, err
		}
	}
	return insert(ctx, tx, Message{Room: room, Author: author, Body: body, ReplyTo: replyTo, At: r.now(), Delivery: delivery}, "", nil)
}

// outbound returns the delivery state a new message gets in room under its bridge's policy: ""
// in a room without a bridge.
func outbound(ctx context.Context, tx *sql.Tx, room string, operator bool) (Delivery, error) {
	var policy string
	err := tx.QueryRowContext(ctx, `SELECT policy FROM bridges WHERE name = ?`, room).Scan(&policy)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", nil
	case err != nil:
		return "", err
	case policy == "read" && operator:
		return "", nil
	case policy == "read":
		return "", fmt.Errorf("%w: #%s is bridged read-only: nothing goes out and agents do not post there", ErrReadOnly, room)
	case policy == "open" || operator:
		return Sending, nil
	}
	return Pending, nil
}

// insert stores m with the identifier outside extID ("" for none) and its mentions; addressees
// are mentioned besides the names in the body.
func insert(ctx context.Context, tx *sql.Tx, m Message, extID string, addressees []string) (int64, error) {
	var reply, ext, extAuthor, extName, delivery any
	if m.ReplyTo != 0 {
		reply = m.ReplyTo
	}
	if extID != "" {
		ext = extID
	}
	if m.External() {
		extAuthor, extName = m.ExtAuthorID, m.ExtAuthorName
	}
	if m.Delivery != "" {
		delivery = string(m.Delivery)
	}
	names, all := Mentions(m.Body)
	res, err := tx.ExecContext(ctx, `INSERT INTO messages (room, author, body, reply_to, at, to_all, ext_id, ext_author_id, ext_author_name, delivery)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.Room, m.Author, m.Body, reply, m.At.UnixMilli(), all, ext, extAuthor, extName, delivery)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	for _, n := range names {
		// a former name addresses the agent that gave it up
		if _, err := tx.ExecContext(ctx, `INSERT INTO mentions (message_id, agent)
			VALUES (:id, COALESCE((SELECT agent FROM former_names WHERE name = :n), :n)) ON CONFLICT DO NOTHING`,
			sql.Named("id", id), sql.Named("n", n)); err != nil {
			return 0, err
		}
	}
	for _, a := range addressees {
		if _, err := tx.ExecContext(ctx, `INSERT INTO mentions (message_id, agent) VALUES (?, ?) ON CONFLICT DO NOTHING`, id, a); err != nil {
			return 0, err
		}
	}
	return id, nil
}

// Incoming is a message that came from outside through the bridge of Room.
type Incoming struct {
	Room       string
	ExtID      string // its identifier outside; stored once per room
	AuthorID   string
	AuthorName string
	// Author, when set, is the agent the message is attributed to instead of the external
	// author: the operator, who wrote it outside.
	Author     string
	Body       string // 1 to MaxBody characters
	ReplyExtID string // the identifier outside of the message it replies to, if stored
	At         time.Time
	Addressees []string // agents the message addresses besides its mentions
}

// PostIncomingTx stores in inside the caller's transaction and returns its identifier, or 0 when
// a message with its identifier outside is already stored in the room.
func (r *Rooms) PostIncomingTx(ctx context.Context, tx *sql.Tx, in Incoming) (int64, error) {
	body := strings.TrimSpace(in.Body)
	if in.ExtID == "" || body == "" || utf8.RuneCountInString(body) > MaxBody {
		return 0, fmt.Errorf("%w: a message from outside needs an id and 1 to %d characters", store.ErrInvalid, MaxBody)
	}
	if id, err := extMessageTx(ctx, tx, in.Room, in.ExtID); id != 0 || err != nil {
		return 0, err
	}
	m := Message{Room: in.Room, Author: in.Author, Body: body, At: in.At}
	if m.At.IsZero() {
		m.At = r.now()
	}
	if in.Author == "" {
		m.ExtAuthorID, m.ExtAuthorName = in.AuthorID, in.AuthorName
	}
	if in.ReplyExtID != "" {
		reply, err := extMessageTx(ctx, tx, in.Room, in.ReplyExtID)
		if err != nil {
			return 0, err
		}
		m.ReplyTo = reply
	}
	return insert(ctx, tx, m, in.ExtID, in.Addressees)
}

// SettleTx moves message id, in room (any room when ""), from the delivery state from to to,
// inside the caller's transaction. A message that is sent keeps its identifier outside extID,
// unless another message of the room has it (one that came in before the bridge answered); a
// failed one keeps reason. It returns the message's room and author, and whether it was in
// the state from.
func SettleTx(ctx context.Context, tx *sql.Tx, id int64, room string, from, to Delivery, extID, reason string) (string, string, bool, error) {
	var ext, why any
	if extID != "" {
		taken, err := extMessageTx(ctx, tx, room, extID)
		if err != nil {
			return "", "", false, err
		}
		if taken == 0 {
			ext = extID
		}
	}
	if reason != "" {
		why = reason
	}
	var author string
	err := tx.QueryRowContext(ctx, `UPDATE messages SET delivery = :to, ext_id = COALESCE(:ext, ext_id), delivery_error = :why
		WHERE id = :id AND delivery = :from AND (:room = '' OR room = :room) RETURNING room, author`,
		sql.Named("to", string(to)), sql.Named("ext", ext), sql.Named("why", why), sql.Named("id", id),
		sql.Named("from", string(from)), sql.Named("room", room)).Scan(&room, &author)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	return room, author, err == nil, err
}

// extMessageTx returns the message of room with the identifier outside extID, or 0.
func extMessageTx(ctx context.Context, tx *sql.Tx, room, extID string) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM messages WHERE room = ? AND ext_id = ?`, room, extID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// History returns the last messages of room, oldest first; last <= 0 means DefaultHistory.
func (r *Rooms) History(ctx context.Context, room string, last int) ([]Message, error) {
	room = strings.TrimPrefix(room, "#")
	if last <= 0 {
		last = DefaultHistory
	}
	var out []Message
	err := store.InTx(ctx, r.db, func(tx *sql.Tx) error {
		if err := requireRoom(ctx, tx, room); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id, room, author, body, COALESCE(reply_to, 0), at, 0, 0,
			COALESCE(ext_author_id, ''), COALESCE(ext_author_name, ''), COALESCE(delivery, ''), COALESCE(delivery_error, '') FROM
			(SELECT * FROM messages WHERE room = ? ORDER BY id DESC LIMIT ?) ORDER BY id`, room, last)
		if err != nil {
			return err
		}
		out, err = scanMessages(rows)
		return err
	})
	return out, err
}

// Unread returns up to limit of agent's unread messages that pass filter, oldest first, and how
// many there are in all. limit <= 0 means no limit.
func (r *Rooms) Unread(ctx context.Context, agent string, filter Filter, limit int) ([]Message, int, error) {
	var out []Message
	var total int
	err := store.InTx(ctx, r.db, func(tx *sql.Tx) error {
		var err error
		out, total, err = unread(ctx, tx, agent, filter, limit)
		return err
	})
	return out, total, err
}

// UnreadCount returns how many of agent's unread messages pass filter.
func (r *Rooms) UnreadCount(ctx context.Context, agent string, filter Filter) (int, error) {
	var n int
	err := store.InTx(ctx, r.db, func(tx *sql.Tx) error {
		if err := agents.ExistsTx(ctx, tx, agent); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+unreadQuery+`)
			WHERE CASE :f WHEN :addressed THEN addressed WHEN :waking THEN addressed OR wake_room ELSE 1 END`,
			sql.Named("a", agent), sql.Named("general", General), sql.Named("f", filter),
			sql.Named("addressed", Addressed), sql.Named("waking", Waking)).Scan(&n)
	})
	return n, err
}

// Take returns what Unread returns and marks those messages read in the same transaction, so
// concurrent readers never get the same message twice: the oldest unread messages move the
// reading positions, a filtered selection only gets single read marks.
func (r *Rooms) Take(ctx context.Context, agent string, filter Filter, limit int) ([]Message, int, error) {
	var out []Message
	var total int
	err := store.InTx(ctx, r.db, func(tx *sql.Tx) error {
		var err error
		if out, total, err = unread(ctx, tx, agent, filter, limit); err != nil {
			return err
		}
		if filter != Everything {
			return markEach(ctx, tx, agent, out)
		}
		return markRead(ctx, tx, agent, out)
	})
	return out, total, err
}

// unreadQuery finds unread messages through the indexes: messages in rooms followed with ModeAll
// or ModeWake after the reading position (messages_room), @all messages in rooms followed with
// ModeMentions after the position (messages_to_all, so their chatter is never walked), and
// messages mentioning the agent by name (mentions_agent). It takes the agent as :a and General
// as :general. wake_room says the message is in a room the agent follows with ModeWake and
// came after the mode became ModeWake.
const unreadQuery = `
WITH followed(room, mode, wake_from) AS (
	SELECT room, mode, wake_from FROM subscriptions WHERE agent = :a
	UNION ALL
	SELECT :general, 'all', 0 WHERE NOT EXISTS (SELECT 1 FROM subscriptions WHERE agent = :a AND room = :general)
), start AS (
	SELECT read_from FROM agents WHERE name = :a
), candidates AS (
	SELECT m.id, 1 AS followed FROM followed AS f
	JOIN messages AS m ON m.room = f.room
		AND m.id > COALESCE((SELECT last_id FROM read_positions WHERE agent = :a AND room = f.room), (SELECT read_from FROM start))
	WHERE f.mode != 'mentions'
	UNION
	SELECT m.id, 1 FROM followed AS f
	JOIN messages AS m INDEXED BY messages_to_all ON m.room = f.room AND m.to_all
		AND m.id > COALESCE((SELECT last_id FROM read_positions WHERE agent = :a AND room = f.room), (SELECT read_from FROM start))
	WHERE f.mode = 'mentions'
	UNION
	SELECT m.id, EXISTS (SELECT 1 FROM followed WHERE room = m.room) FROM mentions AS mm
	JOIN messages AS m ON m.id = mm.message_id
	WHERE mm.agent = :a
		AND m.id > COALESCE((SELECT last_id FROM read_positions WHERE agent = :a AND room = m.room), (SELECT read_from FROM start))
)
SELECT m.id, m.room, m.author, m.body, COALESCE(m.reply_to, 0), m.at,
	(EXISTS (SELECT 1 FROM mentions WHERE message_id = m.id AND agent = :a) OR (m.to_all AND MAX(c.followed))) AS addressed,
	EXISTS (SELECT 1 FROM followed WHERE room = m.room AND mode = 'wake' AND m.id > wake_from) AS wake_room,
	COALESCE(m.ext_author_id, ''), COALESCE(m.ext_author_name, ''), COALESCE(m.delivery, ''), COALESCE(m.delivery_error, '')
FROM candidates AS c CROSS JOIN messages AS m ON m.id = c.id -- CROSS keeps the planner from scanning messages
WHERE m.author != :a AND NOT EXISTS (SELECT 1 FROM read_marks WHERE agent = :a AND message_id = m.id)
GROUP BY m.id
ORDER BY m.id`

func unread(ctx context.Context, tx *sql.Tx, agent string, filter Filter, limit int) ([]Message, int, error) {
	if err := agents.ExistsTx(ctx, tx, agent); err != nil {
		return nil, 0, err
	}
	rows, err := tx.QueryContext(ctx, unreadQuery, sql.Named("a", agent), sql.Named("general", General))
	if err != nil {
		return nil, 0, err
	}
	all, err := scanMessages(rows)
	if err != nil {
		return nil, 0, err
	}
	if filter != Everything {
		var kept []Message
		for _, m := range all {
			if filter.keeps(m) {
				kept = append(kept, m)
			}
		}
		all = kept
	}
	total := len(all)
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	return all, total, nil
}

// RoomUnread counts an agent's unread messages in one room.
type RoomUnread struct {
	Room      string
	Unread    int
	Addressed int // how many of the unread messages address the agent
}

// UnreadByRoom counts agent's unread messages per room, for rooms with any, in name order,
// without changing what is read.
func (r *Rooms) UnreadByRoom(ctx context.Context, agent string) ([]RoomUnread, error) {
	var out []RoomUnread
	err := store.InTx(ctx, r.db, func(tx *sql.Tx) error {
		if err := agents.ExistsTx(ctx, tx, agent); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT room, COUNT(*), SUM(addressed) FROM (`+unreadQuery+`) GROUP BY room ORDER BY room`,
			sql.Named("a", agent), sql.Named("general", General))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var u RoomUnread
			if err := rows.Scan(&u.Room, &u.Unread, &u.Addressed); err != nil {
				return err
			}
			out = append(out, u)
		}
		return rows.Err()
	})
	return out, err
}

// MarkRoomRead moves agent's reading position in room up to the message through, which must
// be in that room, and reports whether it moved; the position never moves backwards, also not
// behind where the agent's reading started when it joined.
func (r *Rooms) MarkRoomRead(ctx context.Context, agent, room string, through int64) (bool, error) {
	room = strings.TrimPrefix(room, "#")
	moved := false
	err := store.InTx(ctx, r.db, func(tx *sql.Tx) error {
		if err := agents.ExistsTx(ctx, tx, agent); err != nil {
			return err
		}
		if err := requireRoom(ctx, tx, room); err != nil {
			return err
		}
		var in string
		err := tx.QueryRowContext(ctx, `SELECT room FROM messages WHERE id = ?`, through).Scan(&in)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && in != room) {
			return fmt.Errorf("%w: message %d is not in #%s", store.ErrInvalid, through, room)
		}
		if err != nil {
			return err
		}
		var pos int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT last_id FROM read_positions WHERE agent = ? AND room = ?),
			(SELECT read_from FROM agents WHERE name = ?))`, agent, room, agent).Scan(&pos); err != nil {
			return err
		}
		if through <= pos {
			return nil
		}
		moved = true
		return markRead(ctx, tx, agent, []Message{{ID: through, Room: room}})
	})
	return moved, err
}

// MarkEach marks single messages read without moving the reading position, so earlier unread
// messages in the same rooms stay unread.
func (r *Rooms) MarkEach(ctx context.Context, agent string, msgs []Message) error {
	return store.InTx(ctx, r.db, func(tx *sql.Tx) error { return markEach(ctx, tx, agent, msgs) })
}

func markEach(ctx context.Context, tx *sql.Tx, agent string, msgs []Message) error {
	for _, m := range msgs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO read_marks (agent, message_id) VALUES (?, ?) ON CONFLICT DO NOTHING`, agent, m.ID); err != nil {
			return err
		}
	}
	return nil
}

// markRead moves agent's reading position in each message's room past that message; positions
// never move backwards. msgs must be the oldest unread messages, as Unread returns them, so no
// unread message is skipped.
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

// subscribe makes agent follow room with mode ("" keeps the mode of a followed room, else
// ModeAll). Reading of a room the agent does not follow starts at its newest message; a
// position left from reading a mention there moves forward too. #general is always followed:
// its row only records a mode. Changing to ModeWake records the newest message, so only later
// ones wake the agent; changing from ModeMentions to another mode moves the position forward
// to just before the oldest unread message addressed to the agent there (the newest message
// when there is none), so chatter from the mentions time does not become unread.
func subscribe(ctx context.Context, tx *sql.Tx, agent, room string, mode Mode) error {
	var newest int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM messages WHERE room = ?`, room).Scan(&newest); err != nil {
		return err
	}
	var old Mode
	err := tx.QueryRowContext(ctx, `SELECT mode FROM subscriptions WHERE agent = ? AND room = ?`, agent, room).Scan(&old)
	switch {
	case errors.Is(err, sql.ErrNoRows) && room == General:
		old = ModeAll // followed implicitly
	case errors.Is(err, sql.ErrNoRows):
		if mode == "" {
			mode = ModeAll
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO subscriptions (agent, room, mode, wake_from) VALUES (?, ?, ?, ?)`,
			agent, room, mode, newest); err != nil {
			return err
		}
		return markRead(ctx, tx, agent, []Message{{ID: newest, Room: room}})
	case err != nil:
		return err
	}
	if mode == "" || mode == old {
		return nil // already followed this way: keep reading where it is
	}
	if old == ModeMentions {
		addressed, _, err := unread(ctx, tx, agent, Addressed, 0)
		if err != nil {
			return err
		}
		through := newest
		for _, m := range addressed {
			if m.Room == room {
				through = m.ID - 1
				break
			}
		}
		if err := moveForward(ctx, tx, agent, room, through); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO subscriptions (agent, room, mode, wake_from) VALUES (?, ?, ?, ?)
		ON CONFLICT (agent, room) DO UPDATE SET mode = excluded.mode, wake_from = excluded.wake_from`, agent, room, mode, newest)
	return err
}

// moveForward moves agent's reading position in room up to the message id through, only if
// that is beyond where it reads now.
func moveForward(ctx context.Context, tx *sql.Tx, agent, room string, through int64) error {
	var pos int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT last_id FROM read_positions WHERE agent = ? AND room = ?),
		(SELECT read_from FROM agents WHERE name = ?))`, agent, room, agent).Scan(&pos); err != nil {
		return err
	}
	if through <= pos {
		return nil
	}
	return markRead(ctx, tx, agent, []Message{{ID: through, Room: room}})
}

func followed(ctx context.Context, tx *sql.Tx, agent string) ([]string, error) {
	rooms, err := store.Strings(ctx, tx, `SELECT room FROM subscriptions WHERE agent = ? AND room != ? ORDER BY room`, agent, General)
	if err != nil {
		return nil, err
	}
	return append([]string{General}, rooms...), nil
}

// subscriptions returns the rooms agent follows with their modes, #general first.
func subscriptions(ctx context.Context, tx *sql.Tx, agent string) ([]Subscription, error) {
	rows, err := tx.QueryContext(ctx, `SELECT room, mode FROM (
		SELECT :general AS room, COALESCE((SELECT mode FROM subscriptions WHERE agent = :a AND room = :general), 'all') AS mode
		UNION ALL SELECT room, mode FROM subscriptions WHERE agent = :a AND room != :general
	) ORDER BY room != :general, room`,
		sql.Named("a", agent), sql.Named("general", General))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Subscription
	for rows.Next() {
		var s Subscription
		if err := rows.Scan(&s.Room, &s.Mode); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func scanMessages(rows *sql.Rows) ([]Message, error) {
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var at int64
		var wakeRoom bool
		if err := rows.Scan(&m.ID, &m.Room, &m.Author, &m.Body, &m.ReplyTo, &at, &m.Addressed, &wakeRoom,
			&m.ExtAuthorID, &m.ExtAuthorName, &m.Delivery, &m.DeliveryError); err != nil {
			return nil, err
		}
		m.At = time.UnixMilli(at)
		m.Wakes = m.Addressed || wakeRoom
		out = append(out, m)
	}
	return out, rows.Err()
}

// deliveryLabel is how a delivery state follows a message's head: "" for a message that stays
// here, else " · pending", " · sent", " · not sent: <reason>" and so on.
func deliveryLabel(d Delivery, reason string) string {
	switch d {
	case "":
		return ""
	case Failed:
		return " · not sent: " + reason
	}
	return " · " + string(d)
}

// Format renders a message for an agent: room, identifier, author, time, reply reference and
// delivery state, then the body indented, shortened to limit characters when limit > 0.
func Format(m Message, limit int) string {
	body := m.Body
	if limit > 0 && utf8.RuneCountInString(body) > limit {
		body = strings.TrimSpace(string([]rune(body)[:limit])) + " …"
	}
	head := fmt.Sprintf("#%s [%d] %s · %s", m.Room, m.ID, m.From(), m.At.Local().Format("15:04"))
	if m.ReplyTo != 0 {
		head += fmt.Sprintf(" · re %d", m.ReplyTo)
	}
	if m.Addressed {
		head += " · to you"
	}
	head += deliveryLabel(m.Delivery, m.DeliveryError)
	return head + "\n  " + strings.ReplaceAll(body, "\n", "\n  ")
}
