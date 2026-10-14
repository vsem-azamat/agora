// Package rooms keeps rooms, the messages posted in them, who follows which room, and what
// each agent has read.
package rooms

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
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
	// ErrArchived refuses what an archived room does not take: messages, followers, a bridge,
	// archiving it again.
	ErrArchived = errors.New("archived room")
	// ErrNotArchived refuses unarchiving a room that is not archived.
	ErrNotArchived = errors.New("room not archived")
	// ErrBridged refuses archiving a room that has a bridge.
	ErrBridged = errors.New("bridged room")
	// ErrUndelivered refuses archiving a room whose messages are still scheduled to go out.
	ErrUndelivered = errors.New("messages scheduled to go out")
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
	Scheduled int       // messages scheduled to go out
	// ArchivedBy is the agent that archived the room, "" when it is not archived, and
	// ArchivedAt when (zero when it is not archived).
	ArchivedBy string
	ArchivedAt time.Time
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
	// ExtAuthorAvatar is the hash of the avatar the bridge sent for the author outside, or "".
	ExtAuthorAvatar string
	// Delivery is where a message of a bridged room stands on its way out ("" when it stays
	// here), and DeliveryError why it failed.
	Delivery      Delivery
	DeliveryError string
	// ScheduledAt is when a message was scheduled to go out (zero for one that is not); it goes
	// out once the bridge's delay has passed since then.
	ScheduledAt time.Time
	// Quiet is set for the board's notices about a bridge: they wake no one.
	Quiet bool
	// Note is set for a note: a message of a bridged room that stays in Agora and is never
	// handed to the bridge.
	Note bool
	// Incoming is set for a message that came in from the outside chat through the room's
	// bridge: from someone there, or from the operator writing there.
	Incoming bool
	// Thread is the root of the thread the message is a reply in, 0 for a message of the room;
	// Also is set for a reply that is also a message of the room.
	Thread int64
	Also   bool
	// ThreadTitle is the title of the message's thread: the one it is a reply in, or, for a
	// root, its own; "" when it has none.
	ThreadTitle string
	// Summary is the thread of a root, set in histories; zero for a message without replies.
	Summary ThreadSummary
}

// ThreadSummary is what a room's history shows of a thread at its root.
type ThreadSummary struct {
	Replies  int
	Repliers []string // who wrote the replies (as From names them), in the order of their first reply
	LastAt   time.Time
	Unread   int // replies unread for the agent the history was read for
	// Participants, set when a thread is read, are who a new reply addresses besides its author:
	// the root's author, the authors of its replies and the agents mentioned in any of them,
	// without those that left the thread, in name order.
	Participants []string
}

// Thread is a thread of a room, as Threads lists it.
type Thread struct {
	Root    int64
	Room    string
	Author  string // who wrote the root, as From names it
	Title   string // "" for a thread without a title
	Replies int
	LastAt  time.Time // the last reply, or the root or its title when newer
	Unread  int       // replies unread for the agent the threads were listed for
}

// MaxTitle is the longest thread title, in characters.
const MaxTitle = 100

// Posting is a message to post: to the room, or with Thread into the thread of that message.
type Posting struct {
	Author, Room, Body string
	ReplyTo            int64
	// Thread names the thread the message is a reply in: its root or any reply of it; 0 posts
	// to the room. Also makes a reply a message of the room too.
	Thread int64
	Also   bool
	// Note posts a note (see PostNote); Operator posts as the operator does (see PostFromOperator).
	Note, Operator bool
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
	Scheduled Delivery = "scheduled" // goes out once the delay passed, unless the operator cancels it
	Sending   Delivery = "sending"   // the bridge has to send it
	Sent      Delivery = "sent"      // the bridge sent it
	Cancelled Delivery = "cancelled" // the operator cancelled it
	Failed    Delivery = "failed"    // the bridge could not send it
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
	// Quiet is the unread messages that do not wake the agent.
	Quiet
)

func (f Filter) keeps(m Message) bool {
	switch f {
	case Addressed:
		return m.Addressed
	case Waking:
		return m.Wakes
	case Quiet:
		return !m.Wakes
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
	var archived sql.NullInt64
	switch err := tx.QueryRowContext(ctx, `SELECT archived_at FROM rooms WHERE name = ?`, name).Scan(&archived); {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return err
	case archived.Valid:
		return store.Refuse(ErrExists, "#%s already exists, archived; bring it back with `agora room-unarchive %s`", name, name)
	default:
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

// listQuery lists the rooms with their counts, archived ones too when :all. The scheduled count
// names 'scheduled' literally, so the partial index messages_scheduled covers it; a bound parameter
// would keep the planner from it.
const listQuery = `SELECT r.name, r.purpose, r.created_by, r.created_at,
	(SELECT COUNT(*) FROM messages WHERE room = r.name), COALESCE((SELECT MAX(at) FROM messages WHERE room = r.name), 0),
	(SELECT COUNT(*) FROM messages WHERE room = r.name AND delivery = 'scheduled'),
	COALESCE(r.archived_by, ''), COALESCE(r.archived_at, 0)
	FROM rooms AS r WHERE :all OR r.archived_at IS NULL ORDER BY r.name`

// List returns the rooms that are not archived, or with all every room, in name order.
func (r *Rooms) List(ctx context.Context, all bool) ([]Room, error) {
	rows, err := r.db.QueryContext(ctx, listQuery, sql.Named("all", all))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Room
	for rows.Next() {
		var x Room
		var created, last, archived int64
		if err := rows.Scan(&x.Name, &x.Purpose, &x.CreatedBy, &created, &x.Messages, &last, &x.Scheduled, &x.ArchivedBy, &archived); err != nil {
			return nil, err
		}
		x.CreatedAt = time.UnixMilli(created)
		if x.ArchivedBy != "" {
			x.ArchivedAt = time.UnixMilli(archived)
		}
		if last > 0 {
			x.LastAt = time.UnixMilli(last)
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// Names returns the names of at most limit rooms that are not archived, in name order, with room
// keep among them when it is one of them, and how many rooms there are in all. It counts no
// messages.
func (r *Rooms) Names(ctx context.Context, limit int, keep string) (names []string, total int, err error) {
	if err = r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM rooms WHERE archived_at IS NULL`).Scan(&total); err != nil {
		return nil, 0, err
	}
	var kept bool
	if err = r.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM rooms WHERE name = ? AND archived_at IS NULL)`, keep).Scan(&kept); err != nil {
		return nil, 0, err
	}
	rest := limit
	if kept {
		rest--
	}
	rows, err := r.db.QueryContext(ctx, `SELECT name FROM rooms WHERE archived_at IS NULL AND name <> ? ORDER BY name LIMIT ?`, keep, max(rest, 0))
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			return nil, 0, err
		}
		names = append(names, name)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	if kept {
		names = append(names, keep)
		slices.Sort(names)
	}
	return names, total, nil
}

// Archive archives room for agent (archive) or brings it back, and the board says so in
// #general. #general, a bridged room, a room whose messages are still scheduled (left from a
// removed bridge), archiving an archived room and unarchiving one that is not archived are
// refused.
func (r *Rooms) Archive(ctx context.Context, agent, room string, archive bool) error {
	room = strings.TrimPrefix(room, "#")
	return store.InTx(ctx, r.db, func(tx *sql.Tx) error {
		if err := agents.ExistsTx(ctx, tx, agent); err != nil {
			return err
		}
		if err := requireRoom(ctx, tx, room); err != nil {
			return err
		}
		archived, err := isArchived(ctx, tx, room)
		if err != nil {
			return err
		}
		verb := "unarchived"
		switch {
		case !archive && !archived:
			return store.Refuse(ErrNotArchived, "#%s is not archived", room)
		case !archive:
			_, err = tx.ExecContext(ctx, `UPDATE rooms SET archived_at = NULL, archived_by = NULL WHERE name = ?`, room)
		case room == General:
			return store.Refuse(store.ErrInvalid, "#%s is everyone's room and cannot be archived", General)
		case archived:
			return store.Refuse(ErrArchived, "#%s is already archived", room)
		default:
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM bridges WHERE name = ?`, room).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				return store.Refuse(ErrBridged, "#%s has a bridge; remove it first with `agora bridge remove %s`", room, room)
			}
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE room = ? AND delivery = 'scheduled'`, room).Scan(&n); err != nil {
				return err
			}
			switch {
			case n == 1:
				return store.Refuse(ErrUndelivered, "a message of #%s is still scheduled to go out; cancel it first", room)
			case n > 1:
				return store.Refuse(ErrUndelivered, "%d messages of #%s are still scheduled to go out; cancel them first", n, room)
			}
			verb = "archived"
			_, err = tx.ExecContext(ctx, `UPDATE rooms SET archived_at = ?, archived_by = ? WHERE name = ?`, r.now().UnixMilli(), agent, room)
		}
		if err != nil {
			return err
		}
		_, err = r.PostTx(ctx, tx, Board, General, fmt.Sprintf("#%s %s by %s", room, verb, agent), 0)
		return err
	})
}

// RequireUnarchivedTx refuses room, which exists, when it is archived, inside the caller's
// transaction.
func RequireUnarchivedTx(ctx context.Context, tx *sql.Tx, room string) error {
	archived, err := isArchived(ctx, tx, room)
	if err != nil || !archived {
		return err
	}
	return store.Refuse(ErrArchived, "#%s is archived; bring it back first with `agora room-unarchive %s`", room, room)
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
			if add {
				if err := RequireUnarchivedTx(ctx, tx, room); err != nil {
					return err
				}
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
// room the message goes out under the room's outbound policy: it is scheduled under delay, goes
// out under open and is refused under read. Only the board posts as Board,
// through PostTx.
func (r *Rooms) Post(ctx context.Context, author, room, body string, replyTo int64) (int64, error) {
	return r.Send(ctx, Posting{Author: author, Room: room, Body: body, ReplyTo: replyTo})
}

// Send posts p as Post, PostNote or PostFromOperator do, into a thread when p names one.
func (r *Rooms) Send(ctx context.Context, p Posting) (int64, error) {
	how := sendAsAgent
	switch {
	case p.Note:
		how = keepAsNote
	case p.Operator:
		how = sendAsOperator
	}
	return r.post(ctx, p, how)
}

// PostFromOperator is Post for the operator, through the web app: in a bridged room the message
// goes out at once, except under the policy read, where it stays here as a note.
func (r *Rooms) PostFromOperator(ctx context.Context, author, room, body string, replyTo int64) (int64, error) {
	return r.Send(ctx, Posting{Author: author, Room: room, Body: body, ReplyTo: replyTo, Operator: true})
}

// PostNote is Post for a note, from an agent or the operator: in a bridged room the message stays
// in Agora under every policy, read included, and is marked as a note; in a room without a
// bridge it is an ordinary message.
func (r *Rooms) PostNote(ctx context.Context, author, room, body string, replyTo int64) (int64, error) {
	return r.Send(ctx, Posting{Author: author, Room: room, Body: body, ReplyTo: replyTo, Note: true})
}

// SendOutTx stores a message of author in the bridged room that goes out at once, as the
// operator's messages do, inside the caller's transaction: the operator sends a note out with it.
// The caller checks that the room's policy lets it go out.
func (r *Rooms) SendOutTx(ctx context.Context, tx *sql.Tx, author, room, body string, replyTo int64) (int64, error) {
	return r.postTx(ctx, tx, Posting{Author: author, Room: room, Body: body, ReplyTo: replyTo}, sendAsOperator)
}

// sending is how a new message of a bridged room may leave Agora.
type sending int

const (
	sendAsAgent    sending = iota // under the outbound policy
	sendAsOperator                // at once, except under read, where it is a note
	keepAsNote                    // never: a note
)

func (r *Rooms) post(ctx context.Context, p Posting, how sending) (int64, error) {
	if p.Author == Board {
		return 0, ErrBoardOnly
	}
	var id int64
	err := store.InTx(ctx, r.db, func(tx *sql.Tx) error {
		var err error
		id, err = r.postTx(ctx, tx, p, how)
		return err
	})
	return id, err
}

// PostTx stores a message from author (a joined agent, or the board itself) inside the caller's
// transaction, so other packages can post atomically with their own changes. The board's
// messages never go out through a bridge.
func (r *Rooms) PostTx(ctx context.Context, tx *sql.Tx, author, room, body string, replyTo int64) (int64, error) {
	return r.postTx(ctx, tx, Posting{Author: author, Room: room, Body: body, ReplyTo: replyTo}, sendAsAgent)
}

func (r *Rooms) postTx(ctx context.Context, tx *sql.Tx, p Posting, how sending) (int64, error) {
	author, replyTo := p.Author, p.ReplyTo
	room := strings.TrimPrefix(p.Room, "#")
	body := strings.TrimSpace(p.Body)
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
	if err := RequireUnarchivedTx(ctx, tx, room); err != nil {
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
	m := Message{Room: room, Author: author, Body: body, ReplyTo: replyTo, At: r.now()}
	var participants []string
	if p.Thread != 0 {
		root, err := rootTx(ctx, tx, p.Thread)
		if err != nil {
			return 0, err
		}
		var in string
		if err := tx.QueryRowContext(ctx, `SELECT room FROM messages WHERE id = ?`, root).Scan(&in); err != nil {
			return 0, err
		}
		if in != room {
			return 0, fmt.Errorf("%w: message %d is in #%s, not #%s", store.ErrInvalid, p.Thread, in, room)
		}
		m.Thread, m.Also = root, p.Also
		// posting in a thread makes the author a participant again
		if _, err := tx.ExecContext(ctx, `DELETE FROM thread_leaves WHERE root = ? AND agent = ?`, root, author); err != nil {
			return 0, err
		}
		if participants, err = participantsTx(ctx, tx, root, author); err != nil {
			return 0, err
		}
	} else if p.Also {
		return 0, fmt.Errorf("%w: also to the room needs a thread", store.ErrInvalid)
	}
	if author != Board {
		var err error
		if m.Delivery, m.Note, err = outbound(ctx, tx, room, how); err != nil {
			return 0, err
		}
	}
	return insert(ctx, tx, m, "", participants)
}

// rootTx returns the root of the thread message id names: the root of the thread id is a reply
// in, or id itself. A message that does not exist is refused.
func rootTx(ctx context.Context, tx *sql.Tx, id int64) (int64, error) {
	var root int64
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(thread, id) FROM messages WHERE id = ?`, id).Scan(&root)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("%w: no message %d", store.ErrInvalid, id)
	}
	return root, err
}

// participantsTx returns the participants of the thread of root other than except: the root's
// author, the authors of its replies, the agents mentioned in any of them (replies address
// participants through mentions too) and, in a bridged room, the bridge's agents, without those
// that left it.
func participantsTx(ctx context.Context, tx *sql.Tx, root int64, except string) ([]string, error) {
	return store.Strings(ctx, tx, `SELECT author FROM messages WHERE (id = :root OR thread = :root) AND author NOT IN ('', :board)
		UNION SELECT mm.agent FROM mentions AS mm JOIN messages AS m ON m.id = mm.message_id WHERE m.id = :root OR m.thread = :root
		UNION SELECT agent FROM bridge_agents WHERE bridge = (SELECT room FROM messages WHERE id = :root)
		EXCEPT SELECT agent FROM thread_leaves WHERE root = :root
		EXCEPT SELECT :except
		ORDER BY 1`, sql.Named("root", root), sql.Named("board", Board), sql.Named("except", except))
}

// outbound returns the delivery state a new message gets in room under its bridge's policy, ""
// in a room without a bridge, and whether it is a note, which only a bridged room has.
func outbound(ctx context.Context, tx *sql.Tx, room string, how sending) (Delivery, bool, error) {
	var policy string
	err := tx.QueryRowContext(ctx, `SELECT policy FROM bridges WHERE name = ?`, room).Scan(&policy)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, err
	case how == keepAsNote, policy == "read" && how == sendAsOperator:
		// what the operator writes in a read-only room stays in Agora: a note
		return "", true, nil
	case policy == "read":
		return "", false, store.Refuse(ErrReadOnly, "#%s is bridged read-only: nothing goes out, and agents post only notes there (--note)", room)
	case policy == "open" || how == sendAsOperator:
		return Sending, false, nil
	}
	return Scheduled, false, nil
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
	var scheduled any
	if m.Delivery == Scheduled {
		scheduled = m.At.UnixMilli()
	}
	var thread any
	if m.Thread != 0 {
		thread = m.Thread
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO messages (room, author, body, reply_to, at, to_all, ext_id, ext_author_id, ext_author_name, delivery, quiet, note, scheduled_at, thread, also)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.Room, m.Author, m.Body, reply, m.At.UnixMilli(), all, ext, extAuthor, extName, delivery, m.Quiet, m.Note, scheduled, thread, m.Also)
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
	// ThreadKey names the thread outside the message belongs to, such as a forum topic; the
	// first message with a new key becomes the root of a thread titled ThreadTitle, later ones
	// are replies in it. "" for a message of the room.
	ThreadKey, ThreadTitle string
}

// PostIncomingTx stores in inside the caller's transaction and returns its identifier, or 0 when
// a message with its identifier outside is already stored in the room.
func (r *Rooms) PostIncomingTx(ctx context.Context, tx *sql.Tx, in Incoming) (int64, error) {
	body := strings.TrimSpace(in.Body)
	if in.ExtID == "" || body == "" || utf8.RuneCountInString(body) > MaxBody {
		return 0, fmt.Errorf("%w: a message from outside needs an id and 1 to %d characters", store.ErrInvalid, MaxBody)
	}
	if err := RequireUnarchivedTx(ctx, tx, in.Room); err != nil {
		return 0, err
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
	if in.ThreadKey == "" {
		return insert(ctx, tx, m, in.ExtID, in.Addressees)
	}
	var root int64
	err := tx.QueryRowContext(ctx, `SELECT root FROM thread_keys WHERE room = ? AND key = ?`, in.Room, in.ThreadKey).Scan(&root)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// a new key: on a platform whose thread key names its root (the root's identifier
		// outside), a message replying in that root's thread joins it, as the root came in before
		// the key did; otherwise the message is the root of the thread outside
		if m.ReplyTo != 0 {
			var replied int64
			var named bool
			if err := tx.QueryRowContext(ctx, `SELECT r.id, r.ext_id = :key AND NOT EXISTS (SELECT 1 FROM thread_keys WHERE root = r.id)
				FROM messages AS m JOIN messages AS r ON r.id = COALESCE(m.thread, m.id) WHERE m.id = :reply`,
				sql.Named("key", in.ThreadKey), sql.Named("reply", m.ReplyTo)).Scan(&replied, &named); err != nil {
				return 0, err
			}
			if named {
				root = replied
			}
		}
		id := root
		if root == 0 {
			if id, err = insert(ctx, tx, m, in.ExtID, in.Addressees); err != nil {
				return 0, err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO thread_keys (room, key, root) VALUES (?, ?, ?)`, in.Room, in.ThreadKey, id); err != nil {
			return 0, err
		}
		// the title the thread gets from outside is its first; later ones are ignored
		if title := in.ThreadTitle; title != "" {
			if _, err := tx.ExecContext(ctx, `INSERT INTO threads (root, title, titled_by, titled_at) VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`,
				id, title, Board, r.now().UnixMilli()); err != nil {
				return 0, err
			}
		}
		if root == 0 {
			return id, nil
		}
	case err != nil:
		return 0, err
	}
	m.Thread = root
	if in.Author != "" {
		// the operator writing in the thread outside takes part in it again, as posting here does
		if _, err := tx.ExecContext(ctx, `DELETE FROM thread_leaves WHERE root = ? AND agent = ?`, root, in.Author); err != nil {
			return 0, err
		}
	}
	participants, err := participantsTx(ctx, tx, root, in.Author)
	if err != nil {
		return 0, err
	}
	return insert(ctx, tx, m, in.ExtID, append(in.Addressees, participants...))
}

// SettleTx moves message id, in room (any room when ""), from the delivery state from to to,
// inside the caller's transaction; a failed message keeps reason. It returns the message's room
// and author, and whether it was in the state from.
func SettleTx(ctx context.Context, tx *sql.Tx, id int64, room string, from, to Delivery, reason string) (string, string, bool, error) {
	var why any
	if reason != "" {
		why = reason
	}
	var author string
	err := tx.QueryRowContext(ctx, `UPDATE messages SET delivery = :to, delivery_error = :why
		WHERE id = :id AND delivery = :from AND (:room = '' OR room = :room) RETURNING room, author`,
		sql.Named("to", string(to)), sql.Named("why", why), sql.Named("id", id),
		sql.Named("from", string(from)), sql.Named("room", room)).Scan(&room, &author)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	return room, author, err == nil, err
}

// SentTx marks message id of room, handed to its bridge, as sent with the identifier outside
// extID ("" for none), inside the caller's transaction, and reports whether it was waiting for
// the answer. Its echo, a message that came in with extID after it was posted (from outside or
// from the operator outside, so without a delivery state), is merged into it: replies to the
// echo reply to it, replies in the echo's thread move to the thread of the sent message (its own,
// or the one it is a reply in), and the echo is deleted. When another message of the room has extID, the
// message is marked sent without it, nothing else changes, and taken is true.
func SentTx(ctx context.Context, tx *sql.Tx, id int64, room, extID string) (ok, taken bool, err error) {
	if _, _, ok, err = SettleTx(ctx, tx, id, room, Sending, Sent, ""); !ok || err != nil || extID == "" {
		return ok, false, err
	}
	var holder int64
	var echo bool
	err = tx.QueryRowContext(ctx, `SELECT id, id > ? AND delivery IS NULL FROM messages WHERE room = ? AND ext_id = ?`, id, room, extID).Scan(&holder, &echo)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return false, false, err
	case !echo:
		return true, true, nil
	default:
		// the echo's thread moves to the thread the sent message belongs to, so threads never nest
		root, err := rootTx(ctx, tx, id)
		if err != nil {
			return false, false, err
		}
		for _, q := range []string{
			`UPDATE messages SET reply_to = :id WHERE reply_to = :echo`,
			// what agents read in the echo's thread stays read, reply by reply: the thread it
			// moves to keeps its own reading positions
			`INSERT INTO read_marks (agent, message_id) SELECT tr.agent, m.id FROM thread_reads AS tr
				JOIN messages AS m ON m.thread = :echo AND m.id <= tr.last_id WHERE tr.root = :echo ON CONFLICT DO NOTHING`,
			`UPDATE messages SET thread = :root WHERE thread = :echo`,
			`UPDATE OR IGNORE threads SET root = :root WHERE root = :echo`,
			`DELETE FROM threads WHERE root = :echo`,
			`UPDATE OR IGNORE thread_leaves SET root = :root WHERE root = :echo`,
			`UPDATE OR IGNORE thread_keys SET root = :root WHERE root = :echo`,
			`DELETE FROM thread_keys WHERE root = :echo`,
			`DELETE FROM thread_leaves WHERE root = :echo`,
			`DELETE FROM thread_reads WHERE root = :echo`,
			`DELETE FROM mentions WHERE message_id = :echo`,
			`DELETE FROM read_marks WHERE message_id = :echo`,
			`DELETE FROM messages WHERE id = :echo`,
		} {
			if _, err := tx.ExecContext(ctx, q, sql.Named("id", id), sql.Named("root", root), sql.Named("echo", holder)); err != nil {
				return false, false, err
			}
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE messages SET ext_id = ? WHERE id = ?`, extID, id)
	return err == nil, false, err
}

// NoticeTx posts a notice of the board about room's bridge inside the caller's transaction:
// unread like any message, but it wakes no one.
func (r *Rooms) NoticeTx(ctx context.Context, tx *sql.Tx, room, body string) error {
	if err := requireRoom(ctx, tx, room); err != nil {
		return err
	}
	if err := RequireUnarchivedTx(ctx, tx, room); err != nil {
		return err
	}
	_, err := insert(ctx, tx, Message{Room: room, Author: Board, Body: strings.TrimSpace(body), At: r.now(), Quiet: true}, "", nil)
	return err
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
	return r.HistoryFor(ctx, room, last, "")
}

// HistoryFor is History read for agent reader ("" for nobody): the room's messages, without the
// replies in threads that are not also posted to the room, each root with its thread's summary,
// whose Unread counts the replies unread for reader.
func (r *Rooms) HistoryFor(ctx context.Context, room string, last int, reader string) ([]Message, error) {
	room = strings.TrimPrefix(room, "#")
	if last <= 0 {
		last = DefaultHistory
	}
	var out []Message
	err := store.InTx(ctx, r.db, func(tx *sql.Tx) error {
		if err := requireRoom(ctx, tx, room); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+messageColumns+` FROM
			(SELECT * FROM messages WHERE room = ? AND (thread IS NULL OR also) ORDER BY id DESC LIMIT ?) AS m ORDER BY m.id`, room, last)
		if err != nil {
			return err
		}
		if out, err = scanMessages(rows); err != nil {
			return err
		}
		return summarize(ctx, tx, out, reader)
	})
	return out, err
}

// ThreadHistory returns the thread that message id names (see Posting.Thread): its root, then
// its last replies, oldest first; last <= 0 means DefaultHistory. The root carries the thread's
// summary, read for reader as in HistoryFor, with its participants.
func (r *Rooms) ThreadHistory(ctx context.Context, id int64, last int, reader string) ([]Message, error) {
	if last <= 0 {
		last = DefaultHistory
	}
	var out []Message
	err := store.InTx(ctx, r.db, func(tx *sql.Tx) error {
		root, err := rootTx(ctx, tx, id)
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+messageColumns+` FROM
			(SELECT * FROM messages WHERE id = :root
				UNION ALL SELECT * FROM (SELECT * FROM messages WHERE thread = :root ORDER BY id DESC LIMIT :last)) AS m ORDER BY m.id`,
			sql.Named("root", root), sql.Named("last", last))
		if err != nil {
			return err
		}
		if out, err = scanMessages(rows); err != nil {
			return err
		}
		if err := summarize(ctx, tx, out[:1], reader); err != nil {
			return err
		}
		out[0].Summary.Participants, err = participantsTx(ctx, tx, root, "")
		return err
	})
	return out, err
}

// summarize sets the thread summary of every message of msgs that has replies; Unread counts the
// replies unread for reader, none when reader is "" or has not joined.
func summarize(ctx context.Context, tx *sql.Tx, msgs []Message, reader string) error {
	at := map[int64]int{}
	for i, m := range msgs {
		if m.Thread == 0 {
			at[m.ID] = i
		}
	}
	if len(at) == 0 {
		return nil
	}
	var roots []string
	for id := range at {
		roots = append(roots, strconv.FormatInt(id, 10))
	}
	in := "[" + strings.Join(roots, ",") + "]" // a JSON array, read with json_each
	rows, err := tx.QueryContext(ctx, `SELECT thread, CASE WHEN author != '' THEN author ELSE ext_author_name || '@' || room END AS who,
		COUNT(*), MAX(at) FROM messages WHERE thread IN (SELECT value FROM json_each(?)) GROUP BY thread, who ORDER BY thread, MIN(id)`, in)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var root, last int64
		var who string
		var n int
		if err := rows.Scan(&root, &who, &n, &last); err != nil {
			return err
		}
		sum := &msgs[at[root]].Summary
		sum.Replies += n
		sum.Repliers = append(sum.Repliers, who)
		if t := time.UnixMilli(last); t.After(sum.LastAt) {
			sum.LastAt = t
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return countUnread(ctx, tx, msgs, reader)
}

// countUnread sets Summary.Unread of each message of msgs that is a root to the replies in its
// thread unread for reader; none when reader is "" or has not joined.
func countUnread(ctx context.Context, tx *sql.Tx, msgs []Message, reader string) error {
	at := map[int64]int{}
	var roots []string
	for i, m := range msgs {
		if m.Thread == 0 {
			at[m.ID] = i
			roots = append(roots, strconv.FormatInt(m.ID, 10))
		}
	}
	if len(roots) == 0 || reader == "" {
		return nil
	}
	in := "[" + strings.Join(roots, ",") + "]"
	// a reader that has not joined has nothing unread
	var joined bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM agents WHERE name = ?)`, reader).Scan(&joined); err != nil || !joined {
		return err
	}
	unread, err := tx.QueryContext(ctx, `SELECT thread, COUNT(*) FROM (`+unreadQuery+`) WHERE thread IN (SELECT value FROM json_each(:roots)) GROUP BY thread`,
		sql.Named("a", reader), sql.Named("general", General), sql.Named("roots", in))
	if err != nil {
		return err
	}
	defer unread.Close()
	for unread.Next() {
		var root int64
		var n int
		if err := unread.Scan(&root, &n); err != nil {
			return err
		}
		msgs[at[root]].Summary.Unread = n
	}
	return unread.Err()
}

// SetThreadTitle gives the thread that message id names (see Posting.Thread) a title of 1 to
// MaxTitle characters on one line, replacing an earlier one; agent is the joined agent that titles it.
func (r *Rooms) SetThreadTitle(ctx context.Context, agent string, id int64, title string) error {
	title = strings.Join(strings.Fields(title), " ")
	if title == "" || utf8.RuneCountInString(title) > MaxTitle {
		return fmt.Errorf("%w: a thread title is 1 to %d characters", store.ErrInvalid, MaxTitle)
	}
	return store.InTx(ctx, r.db, func(tx *sql.Tx) error {
		if err := agents.ExistsTx(ctx, tx, agent); err != nil {
			return err
		}
		root, err := rootTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := requireUnarchivedMessage(ctx, tx, root); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO threads (root, title, titled_by, titled_at) VALUES (?, ?, ?, ?)
			ON CONFLICT (root) DO UPDATE SET title = excluded.title, titled_by = excluded.titled_by, titled_at = excluded.titled_at`,
			root, title, agent, r.now().UnixMilli())
		return err
	})
}

// LeaveThread makes agent leave the thread that message id names (see Posting.Thread): its
// replies so far are read, and later ones no longer address agent unless they mention it, until
// agent posts there again.
func (r *Rooms) LeaveThread(ctx context.Context, agent string, id int64) error {
	return store.InTx(ctx, r.db, func(tx *sql.Tx) error {
		if err := agents.ExistsTx(ctx, tx, agent); err != nil {
			return err
		}
		root, err := rootTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO thread_leaves (root, agent) VALUES (?, ?) ON CONFLICT DO NOTHING`, root, agent); err != nil {
			return err
		}
		var newest int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM messages WHERE thread = ?`, root).Scan(&newest); err != nil || newest == 0 {
			return err
		}
		if _, err := readThreadTx(ctx, tx, agent, root, newest); err != nil {
			return err
		}
		// replies also in the room are read by the room's position, so they get read marks
		_, err = tx.ExecContext(ctx, `INSERT INTO read_marks (agent, message_id) SELECT :a, id FROM messages AS m
			WHERE m.thread = :root AND m.also AND m.id > COALESCE((SELECT last_id FROM read_positions WHERE agent = :a AND room = m.room), 0)
			ON CONFLICT DO NOTHING`, sql.Named("a", agent), sql.Named("root", root))
		return err
	})
}

// Threads returns the titled threads of room, or with room "" of every room that is not archived,
// the most recently active first; Unread counts the replies unread for reader (none for "" or an
// agent that has not joined). With withUnread, the threads without a title in which reader has
// unread replies are listed too.
func (r *Rooms) Threads(ctx context.Context, room, reader string, withUnread bool) ([]Thread, error) {
	room = strings.TrimPrefix(room, "#")
	var out []Thread
	err := store.InTx(ctx, r.db, func(tx *sql.Tx) error {
		if room != "" {
			if err := requireRoom(ctx, tx, room); err != nil {
				return err
			}
		}
		untitled := "[]" // roots of threads without a title with replies unread for reader, as a JSON array
		if withUnread && reader != "" {
			var joined bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM agents WHERE name = ?)`, reader).Scan(&joined); err != nil {
				return err
			}
			if joined {
				if err := tx.QueryRowContext(ctx, `SELECT COALESCE(json_group_array(DISTINCT thread), '[]') FROM (`+unreadQuery+`) WHERE thread != 0`,
					sql.Named("a", reader), sql.Named("general", General)).Scan(&untitled); err != nil {
					return err
				}
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT m.id, m.room, CASE WHEN m.author != '' THEN m.author ELSE m.ext_author_name || '@' || m.room END,
				COALESCE(t.title, ''), COUNT(rp.id), MAX(m.at, COALESCE(t.titled_at, 0), COALESCE(MAX(rp.at), 0)) AS last
			FROM messages AS m LEFT JOIN threads AS t ON t.root = m.id
			LEFT JOIN messages AS rp ON rp.thread = m.id
			WHERE (t.root IS NOT NULL OR m.id IN (SELECT value FROM json_each(:untitled)))
				AND CASE :room WHEN '' THEN NOT EXISTS (SELECT 1 FROM rooms WHERE name = m.room AND archived_at IS NOT NULL) ELSE m.room = :room END
			GROUP BY m.id ORDER BY last DESC, m.id DESC`, sql.Named("room", room), sql.Named("untitled", untitled))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var th Thread
			var last int64
			if err := rows.Scan(&th.Root, &th.Room, &th.Author, &th.Title, &th.Replies, &last); err != nil {
				return err
			}
			th.LastAt = time.UnixMilli(last)
			out = append(out, th)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		// the counts come as thread summaries do: from roots
		roots := make([]Message, len(out))
		for i, th := range out {
			roots[i] = Message{ID: th.Root}
		}
		if err := countUnread(ctx, tx, roots, reader); err != nil {
			return err
		}
		for i := range out {
			out[i].Unread = roots[i].Summary.Unread
		}
		return nil
	})
	return out, err
}

// requireUnarchivedMessage refuses a change to a message of an archived room.
func requireUnarchivedMessage(ctx context.Context, tx *sql.Tx, id int64) error {
	var room string
	if err := tx.QueryRowContext(ctx, `SELECT room FROM messages WHERE id = ?`, id).Scan(&room); err != nil {
		return err
	}
	return RequireUnarchivedTx(ctx, tx, room)
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
			WHERE CASE :f WHEN :addressed THEN addressed WHEN :waking THEN addressed OR wake_room
				WHEN :quiet THEN NOT (addressed OR wake_room) ELSE 1 END`,
			sql.Named("a", agent), sql.Named("general", General), sql.Named("f", filter),
			sql.Named("addressed", Addressed), sql.Named("waking", Waking), sql.Named("quiet", Quiet)).Scan(&n)
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

// unreadQuery finds unread messages, never in an archived room, through the indexes: messages in rooms followed with ModeAll
// or ModeWake after the reading position (messages_room), @all messages in rooms followed with
// ModeMentions after the position (messages_to_all, so their chatter is never walked), and
// messages mentioning the agent by name (mentions_agent). Replies in threads that are not also
// posted to the room come only through mentions (a reply mentions its thread's participants),
// after the agent's reading position in their thread (thread_reads), not in the room. It takes the agent as :a and General
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
	WHERE f.mode != 'mentions' AND (m.thread IS NULL OR m.also)
	UNION
	SELECT m.id, 1 FROM followed AS f
	JOIN messages AS m INDEXED BY messages_to_all ON m.room = f.room AND m.to_all
		AND m.id > COALESCE((SELECT last_id FROM read_positions WHERE agent = :a AND room = f.room), (SELECT read_from FROM start))
	WHERE f.mode = 'mentions' AND (m.thread IS NULL OR m.also)
	UNION
	SELECT m.id, EXISTS (SELECT 1 FROM followed WHERE room = m.room) FROM mentions AS mm
	JOIN messages AS m ON m.id = mm.message_id
	WHERE mm.agent = :a
		AND m.id > CASE WHEN m.thread IS NULL OR m.also
			THEN COALESCE((SELECT last_id FROM read_positions WHERE agent = :a AND room = m.room), (SELECT read_from FROM start))
			ELSE COALESCE((SELECT last_id FROM thread_reads WHERE agent = :a AND root = m.thread), (SELECT read_from FROM start)) END
)
SELECT m.id, m.room, m.author, m.body, COALESCE(m.reply_to, 0), m.at,
	(EXISTS (SELECT 1 FROM mentions WHERE message_id = m.id AND agent = :a) OR (m.to_all AND MAX(c.followed))) AS addressed,
	(NOT m.quiet AND (m.thread IS NULL OR m.also) AND EXISTS (SELECT 1 FROM followed WHERE room = m.room AND mode = 'wake' AND m.id > wake_from)) AS wake_room,
	COALESCE(m.ext_author_id, ''), COALESCE(m.ext_author_name, ''), ` + outsideAvatarColumn + `, COALESCE(m.delivery, ''), COALESCE(m.delivery_error, ''),
	m.note, m.ext_id IS NOT NULL AND m.delivery IS NULL, COALESCE(m.scheduled_at, 0), ` + threadColumns + `
FROM candidates AS c CROSS JOIN messages AS m ON m.id = c.id -- CROSS keeps the planner from scanning messages
WHERE m.author != :a AND NOT EXISTS (SELECT 1 FROM read_marks WHERE agent = :a AND message_id = m.id)
	AND NOT EXISTS (SELECT 1 FROM rooms WHERE name = m.room AND archived_at IS NOT NULL)
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
	Unread    int // unread messages of the room, replies also in the room included
	Addressed int // how many of them address the agent
	// Threads counts the unread replies in the room's threads that are not also in the room,
	// which Unread leaves out.
	Threads int
}

// UnreadByRoom counts agent's unread messages per room, for rooms with any, in name order,
// without changing what is read.
func (r *Rooms) UnreadByRoom(ctx context.Context, agent string) ([]RoomUnread, error) {
	var out []RoomUnread
	err := store.InTx(ctx, r.db, func(tx *sql.Tx) error {
		if err := agents.ExistsTx(ctx, tx, agent); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT room, SUM(thread = 0 OR also), SUM(addressed AND (thread = 0 OR also)), SUM(thread != 0 AND NOT also)
			FROM (`+unreadQuery+`) GROUP BY room ORDER BY room`,
			sql.Named("a", agent), sql.Named("general", General))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var u RoomUnread
			if err := rows.Scan(&u.Room, &u.Unread, &u.Addressed, &u.Threads); err != nil {
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
// behind where the agent's reading started when it joined. Replies in threads that are not also
// in the room are read apart, in their thread, so the room's position leaves them unread. When
// through is a reply in a thread, the agent's position in that thread moves up to it, which
// reads the thread's replies up to it; a reply also in the room moves the room's position too.
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
		var thread int64
		var also bool
		err := tx.QueryRowContext(ctx, `SELECT room, COALESCE(thread, 0), also FROM messages WHERE id = ?`, through).Scan(&in, &thread, &also)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && in != room) {
			return fmt.Errorf("%w: message %d is not in #%s", store.ErrInvalid, through, room)
		}
		if err != nil {
			return err
		}
		if thread != 0 {
			if moved, err = readThreadTx(ctx, tx, agent, thread, through); err != nil || !also {
				return err
			}
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

// readThreadTx moves agent's reading position in the thread of root up to the reply through and
// reports whether it moved; it never moves backwards.
func readThreadTx(ctx context.Context, tx *sql.Tx, agent string, root, through int64) (bool, error) {
	res, err := tx.ExecContext(ctx, `INSERT INTO thread_reads (agent, root, last_id) VALUES (?, ?, ?)
		ON CONFLICT (agent, root) DO UPDATE SET last_id = excluded.last_id WHERE excluded.last_id > last_id`, agent, root, through)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
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
		if m.Thread != 0 && !m.Also {
			// a reply in a thread only is read in its thread
			if _, err := readThreadTx(ctx, tx, agent, m.Thread, m.ID); err != nil {
				return err
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO read_positions (agent, room, last_id) VALUES (?, ?, ?)
			ON CONFLICT (agent, room) DO UPDATE SET last_id = MAX(last_id, excluded.last_id)`, agent, m.Room, m.ID); err != nil {
			return err
		}
	}
	// single marks behind a reading position are no longer needed: the room's for its messages,
	// a thread's for its replies that are not also in the room
	_, err := tx.ExecContext(ctx, `DELETE FROM read_marks WHERE agent = :a AND message_id <= (SELECT
		CASE WHEN m.thread IS NULL OR m.also
			THEN COALESCE((SELECT last_id FROM read_positions WHERE agent = :a AND room = m.room), 0)
			ELSE COALESCE((SELECT last_id FROM thread_reads WHERE agent = :a AND root = m.thread), 0) END
		FROM messages AS m WHERE m.id = read_marks.message_id)`, sql.Named("a", agent))
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

// isArchived reports whether room, which exists, is archived.
func isArchived(ctx context.Context, tx *sql.Tx, room string) (bool, error) {
	var archived bool
	err := tx.QueryRowContext(ctx, `SELECT archived_at IS NOT NULL FROM rooms WHERE name = ?`, room).Scan(&archived)
	return archived, err
}

// followed returns the rooms agent follows, #general first, without archived rooms.
func followed(ctx context.Context, tx *sql.Tx, agent string) ([]string, error) {
	rooms, err := store.Strings(ctx, tx, `SELECT s.room FROM subscriptions AS s JOIN rooms AS r ON r.name = s.room
		WHERE s.agent = ? AND s.room != ? AND r.archived_at IS NULL ORDER BY s.room`, agent, General)
	if err != nil {
		return nil, err
	}
	return append([]string{General}, rooms...), nil
}

// subscriptions returns the rooms agent follows with their modes, #general first, without
// archived rooms.
func subscriptions(ctx context.Context, tx *sql.Tx, agent string) ([]Subscription, error) {
	rows, err := tx.QueryContext(ctx, `SELECT room, mode FROM (
		SELECT :general AS room, COALESCE((SELECT mode FROM subscriptions WHERE agent = :a AND room = :general), 'all') AS mode
		UNION ALL SELECT s.room, s.mode FROM subscriptions AS s JOIN rooms AS r ON r.name = s.room
		WHERE s.agent = :a AND s.room != :general AND r.archived_at IS NULL
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

// messageColumns selects message m as scanMessages reads it, outside unread lists.
const messageColumns = `m.id, m.room, m.author, m.body, COALESCE(m.reply_to, 0), m.at, 0, 0,
	COALESCE(m.ext_author_id, ''), COALESCE(m.ext_author_name, ''), ` + outsideAvatarColumn + `, COALESCE(m.delivery, ''), COALESCE(m.delivery_error, ''),
	m.note, m.ext_id IS NOT NULL AND m.delivery IS NULL, COALESCE(m.scheduled_at, 0), ` + threadColumns

// threadColumns selects the thread of message m: its root (0 for a message of the room), also,
// and the thread's title, of the thread m is a reply in or of its own.
const threadColumns = `COALESCE(m.thread, 0) AS thread, m.also, COALESCE((SELECT title FROM threads WHERE root = COALESCE(m.thread, m.id)), '')`

// outsideAvatarColumn selects the avatar of the author outside of message m, or "".
const outsideAvatarColumn = `COALESCE((SELECT oa.avatar FROM outside_avatars AS oa WHERE oa.room = m.room AND oa.author = m.ext_author_id), '')`

func scanMessages(rows *sql.Rows) ([]Message, error) {
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var at, scheduled int64
		var wakeRoom bool
		if err := rows.Scan(&m.ID, &m.Room, &m.Author, &m.Body, &m.ReplyTo, &at, &m.Addressed, &wakeRoom,
			&m.ExtAuthorID, &m.ExtAuthorName, &m.ExtAuthorAvatar, &m.Delivery, &m.DeliveryError, &m.Note, &m.Incoming, &scheduled,
			&m.Thread, &m.Also, &m.ThreadTitle); err != nil {
			return nil, err
		}
		m.At = time.UnixMilli(at)
		if scheduled > 0 {
			m.ScheduledAt = time.UnixMilli(scheduled)
		}
		m.Wakes = m.Addressed || wakeRoom
		out = append(out, m)
	}
	return out, rows.Err()
}

// deliveryLabel is how a delivery state follows a message's head: "" for a message that stays
// here, else " · scheduled", " · sending", " · sent", " · cancelled" or " · not sent: <reason>".
func deliveryLabel(d Delivery, reason string) string {
	switch d {
	case "":
		return ""
	case Failed:
		return " · not sent: " + reason
	}
	return " · " + string(d)
}

// Format renders a message for an agent: room, identifier, author, time, reply reference, and
// in a bridged room whether it came from outside, is a note or where it stands on its way out,
// then the body indented, shortened to limit characters when limit > 0.
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
	switch {
	case m.Incoming:
		head += " · from outside"
	case m.Note:
		head += " · note"
	}
	head += deliveryLabel(m.Delivery, m.DeliveryError) + threadLabel(m.Thread, m.ThreadTitle, m.Summary)
	return head + "\n  " + strings.ReplaceAll(body, "\n", "\n  ")
}

// threadLabel is how a message's thread follows its head: " · in thread 40 «title»" for a reply
// in a thread, " · thread «title»: 3 replies (builder, tester), last 14:02, 1 unread" for a root
// with replies or a title, else "".
func threadLabel(thread int64, title string, sum ThreadSummary) string {
	if title != "" {
		title = " «" + title + "»"
	}
	if thread != 0 {
		return fmt.Sprintf(" · in thread %d%s", thread, title)
	}
	if sum.Replies == 0 {
		if title == "" {
			return ""
		}
		return " · thread" + title
	}
	label := fmt.Sprintf(" · thread%s: %d repl%s (%s), last %s", title, sum.Replies, map[bool]string{true: "y", false: "ies"}[sum.Replies == 1],
		strings.Join(sum.Repliers, ", "), sum.LastAt.Local().Format("15:04"))
	if sum.Unread > 0 {
		label += fmt.Sprintf(", %d unread", sum.Unread)
	}
	return label
}
