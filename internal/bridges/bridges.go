// Package bridges keeps the bridges that connect rooms to chats outside Agora: which room each
// one serves, its command and outbound policy, the messages that come in through it and what
// goes out.
package bridges

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vsem-azamat/agora/internal/agents"
	"github.com/vsem-azamat/agora/internal/rooms"
	"github.com/vsem-azamat/agora/internal/store"
)

// Policy says what goes out of a bridged room.
type Policy string

// Outbound policies.
const (
	Open  Policy = "open"  // every message goes out at once
	Delay Policy = "delay" // agents' messages go out once the delay passed, unless the operator cancels them
	Read  Policy = "read"  // nothing goes out; agents may not post
)

// DefaultDelay is how long an agent's message waits under Delay when the hub sets no delay.
const DefaultDelay = time.Minute

const (
	// Truncated ends a message from outside that was shortened to rooms.MaxBody characters.
	Truncated = " … [truncated]"
	// Empty is the text of a message from outside that came without one.
	Empty = "[empty]"
	// MaxIdentifier bounds, in bytes, an identifier outside or a cursor.
	MaxIdentifier = 256
	// maxName bounds an external author's name and id, in characters.
	maxName = 100
	// maxError bounds the reason a failed message keeps, in characters.
	maxError = 500
	// MaxCap bounds each capability a bridge names, in characters.
	MaxCap = 64
)

var (
	// ErrNotFound means there is no such bridge, no such scheduled message or no such note.
	ErrNotFound = errors.New("not found")
	// ErrExists means the room already has a bridge.
	ErrExists = errors.New("bridge exists")
)

// Bridge is a stored bridge.
type Bridge struct {
	Name       string // also its room's
	Command    string
	Policy     Policy
	CreatedBy  string
	CreatedAt  time.Time
	Addressees []string // in name order
	Cursor     string
}

// Bridges keeps bridges in the hub database.
type Bridges struct {
	db    *sql.DB
	rooms *rooms.Rooms
	now   func() time.Time

	// Policy is the outbound policy a bridge gets when it is added; Delay when "".
	Policy Policy
	// Delay is how long an agent's message waits under Delay; DefaultDelay when 0.
	Delay time.Duration
}

// delay is how long an agent's message waits under Delay.
func (b *Bridges) delay() time.Duration {
	if b.Delay <= 0 {
		return DefaultDelay
	}
	return b.Delay
}

// DueAt is when a message scheduled at goes out.
func (b *Bridges) DueAt(scheduled time.Time) time.Time { return scheduled.Add(b.delay()) }

// ValidPolicy refuses a policy that is not open, delay or read.
func ValidPolicy(p Policy) error {
	switch p {
	case Open, Delay, Read:
		return nil
	}
	return store.Refuse(store.ErrInvalid, "policy %q: open, delay or read", p)
}

// New returns Bridges over db; now is the clock (time.Now when nil).
func New(db *sql.DB, r *rooms.Rooms, now func() time.Time) *Bridges {
	if now == nil {
		now = time.Now
	}
	return &Bridges{db: db, rooms: r, now: now}
}

// DefaultPurpose is the purpose of a room created for a bridge without one.
const DefaultPurpose = "A chat outside Agora, connected by a bridge."

// Add stores a bridge named name for the room of that name, creating the room with purpose
// when it does not exist, and subscribes agent's addressees to it with the mode wake, all at
// once. It reports whether it created the room.
func (b *Bridges) Add(ctx context.Context, agent, name, command, purpose string, addressees []string) (bool, error) {
	name = strings.TrimPrefix(name, "#")
	command = strings.TrimSpace(command)
	if !rooms.ValidName(name) {
		return false, store.Refuse(store.ErrInvalid, "bridge name %q: %s", name, rooms.NameRule)
	}
	if name == rooms.General {
		return false, store.Refuse(store.ErrInvalid, "#%s is everyone's room and cannot be bridged", rooms.General)
	}
	if command == "" {
		return false, store.Refuse(store.ErrInvalid, "a bridge needs a command")
	}
	if strings.TrimSpace(purpose) == "" {
		purpose = DefaultPurpose
	}
	created := false
	err := store.InTx(ctx, b.db, func(tx *sql.Tx) error {
		if err := agents.ExistsTx(ctx, tx, agent); err != nil {
			return err
		}
		for _, a := range addressees {
			if err := agents.ExistsTx(ctx, tx, a); err != nil {
				return err
			}
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM bridges WHERE name = ?`, name).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return store.Refuse(ErrExists, "#%s already has a bridge; change it with `agora bridge set %s`, or remove it first with `agora bridge remove %s`", name, name, name)
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM rooms WHERE name = ?`, name).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if err := b.rooms.CreateTx(ctx, tx, name, purpose, agent); err != nil {
				return err
			}
			created = true
		} else if err := rooms.RequireUnarchivedTx(ctx, tx, name); err != nil {
			return err
		}
		policy := b.Policy
		if policy == "" {
			policy = Delay
		}
		// messages left scheduled by a bridge removed before they went out start their delay
		// again, so none goes out the moment the room is bridged again
		if _, err := tx.ExecContext(ctx, `UPDATE messages SET scheduled_at = ? WHERE room = ? AND delivery = 'scheduled'`,
			b.now().UnixMilli(), name); err != nil {
			return err
		}
		// the keys of a bridge removed earlier belong to its chat, which this one may not be
		if _, err := tx.ExecContext(ctx, `DELETE FROM thread_keys WHERE room = ?`, name); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO bridges (name, command, policy, created_by, created_at) VALUES (?, ?, ?, ?, ?)`,
			name, command, policy, agent, b.now().UnixMilli()); err != nil {
			return err
		}
		for _, a := range addressees {
			if _, err := tx.ExecContext(ctx, `INSERT INTO bridge_agents (bridge, agent) VALUES (?, ?) ON CONFLICT DO NOTHING`, name, a); err != nil {
				return err
			}
			if err := rooms.SubscribeTx(ctx, tx, a, name, rooms.ModeWake); err != nil {
				return err
			}
		}
		return nil
	})
	return created, err
}

// Remove deletes the bridge name; its room, subscriptions and messages stay, and the board says
// in the room that it is no longer bridged. Messages of the room left sending are marked not
// sent, since no bridge answers for them any more, and the board tells their authors that they
// may have gone out all the same.
func (b *Bridges) Remove(ctx context.Context, agent, name string) error {
	name = strings.TrimPrefix(name, "#")
	return store.InTx(ctx, b.db, func(tx *sql.Tx) error {
		if err := agents.ExistsTx(ctx, tx, agent); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM bridges WHERE name = ?`, name)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return store.Refuse(ErrNotFound, "no bridge %s; see `agora bridge list`", name)
		}
		if _, err := b.rooms.PostTx(ctx, tx, rooms.Board, name, agent+" removed the bridge of this room; the room stays, no longer bridged.", 0); err != nil {
			return err
		}
		// no bridge answers for the messages left sending any more
		failed, err := failSending(ctx, tx, name)
		if err != nil {
			return err
		}
		for _, f := range failed {
			text := fmt.Sprintf("@%s your message %d may not have gone out: the bridge was removed before it answered. Check the chat before you send it again.", f.author, f.id)
			if _, err := b.rooms.PostTx(ctx, tx, rooms.Board, name, text, f.id); err != nil {
				return err
			}
		}
		return nil
	})
}

// ForgetThreadKeys deletes the thread keys of the bridge's room. The hub calls it once a bridge
// whose command changed has stopped: lines its old process wrote after Set committed made keys
// of the old chat again.
func (b *Bridges) ForgetThreadKeys(ctx context.Context, name string) error {
	_, err := b.db.ExecContext(ctx, `DELETE FROM thread_keys WHERE room = ?`, strings.TrimPrefix(name, "#"))
	return err
}

// Change is what Set changes in a bridge.
type Change struct {
	SetAgents  bool     // replace the bridge's agents with Agents
	Agents     []string // the new agents; none when empty
	SetCommand bool     // replace the bridge's command with Command, which may not be empty
	Command    string   // the new command
}

// Changed is what Set did.
type Changed struct {
	Agents  []string // the bridge's agents now, in name order
	Dropped []string // the agents no longer among them, in name order
	Restart bool     // the command changed, so the hub runs the new one
}

// Set changes the bridge name in place, all at once: it replaces its agents, subscribing those
// that were not among them to the room with the mode wake, and its command. Its cursor, policy
// and creator stay, and so do the subscriptions of the agents it drops. The board says in the
// room what agent changed, waking no one.
func (b *Bridges) Set(ctx context.Context, agent, name string, c Change) (Changed, error) {
	name = strings.TrimPrefix(name, "#")
	command := strings.TrimSpace(c.Command)
	if !c.SetAgents && !c.SetCommand {
		return Changed{}, store.Refuse(store.ErrInvalid, "nothing to change: give agents or a command")
	}
	if c.SetCommand && command == "" {
		return Changed{}, store.Refuse(store.ErrInvalid, "a bridge needs a command")
	}
	var out Changed
	err := store.InTx(ctx, b.db, func(tx *sql.Tx) error {
		if err := agents.ExistsTx(ctx, tx, agent); err != nil {
			return err
		}
		for _, a := range c.Agents {
			if err := agents.ExistsTx(ctx, tx, a); err != nil {
				return err
			}
		}
		var old string
		err := tx.QueryRowContext(ctx, `SELECT command FROM bridges WHERE name = ?`, name).Scan(&old)
		if errors.Is(err, sql.ErrNoRows) {
			return store.Refuse(ErrNotFound, "no bridge %s; see `agora bridge list`", name)
		}
		if err != nil {
			return err
		}
		before, err := store.Strings(ctx, tx, `SELECT agent FROM bridge_agents WHERE bridge = ? ORDER BY agent`, name)
		if err != nil {
			return err
		}
		out.Agents = before
		var said []string
		if c.SetAgents {
			now := slices.Clone(c.Agents)
			slices.Sort(now)
			now = slices.Compact(now)
			if _, err := tx.ExecContext(ctx, `DELETE FROM bridge_agents WHERE bridge = ?`, name); err != nil {
				return err
			}
			for _, a := range now {
				if _, err := tx.ExecContext(ctx, `INSERT INTO bridge_agents (bridge, agent) VALUES (?, ?)`, name, a); err != nil {
					return err
				}
				if slices.Contains(before, a) {
					continue // keeps the mode it follows the room with
				}
				if err := rooms.SubscribeTx(ctx, tx, a, name, rooms.ModeWake); err != nil {
					return err
				}
			}
			for _, a := range before {
				if !slices.Contains(now, a) {
					out.Dropped = append(out.Dropped, a)
				}
			}
			out.Agents = now
			list := "none"
			if len(now) > 0 {
				list = strings.Join(now, ", ")
			}
			said = append(said, "set the bridge's agents: "+list)
		}
		if c.SetCommand && command != old {
			if _, err := tx.ExecContext(ctx, `UPDATE bridges SET command = ? WHERE name = ?`, command, name); err != nil {
				return err
			}
			// the keys name threads of the chat the old command served
			if _, err := tx.ExecContext(ctx, `DELETE FROM thread_keys WHERE room = ?`, name); err != nil {
				return err
			}
			out.Restart = true
			said = append(said, "changed the bridge's command; the hub runs the new one")
		}
		if len(said) == 0 {
			return nil
		}
		return b.rooms.NoticeTx(ctx, tx, name, agent+" "+strings.Join(said, " and ")+".")
	})
	if err != nil {
		return Changed{}, err
	}
	return out, nil
}

// settled is a message failSending marked not sent.
type settled struct {
	id     int64
	author string
}

// failSending marks the messages of room still sending as not sent, since its bridge was
// removed, and returns them in posting order.
func failSending(ctx context.Context, tx *sql.Tx, room string) ([]settled, error) {
	rows, err := tx.QueryContext(ctx, `UPDATE messages SET delivery = 'failed', delivery_error = ?
		WHERE room = ? AND delivery = 'sending' RETURNING id, author`, removedReason, room)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []settled
	for rows.Next() {
		var m settled
		if err := rows.Scan(&m.id, &m.author); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b settled) int { return cmp.Compare(a.id, b.id) })
	return out, nil
}

// removedReason is why a message left sending when its bridge was removed is not known to have
// gone out: the bridge may have sent it and been stopped before it answered.
const removedReason = "the bridge was removed before it answered; it may have gone out"

// List returns every bridge in name order.
func (b *Bridges) List(ctx context.Context) ([]Bridge, error) {
	rows, err := b.db.QueryContext(ctx, `SELECT b.name, b.command, b.policy, b.created_by, b.created_at, b.cursor,
		COALESCE((SELECT group_concat(agent, ' ' ORDER BY agent) FROM bridge_agents WHERE bridge = b.name), '')
		FROM bridges AS b ORDER BY b.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Bridge
	for rows.Next() {
		var x Bridge
		var created int64
		var addressees string
		if err := rows.Scan(&x.Name, &x.Command, &x.Policy, &x.CreatedBy, &created, &x.Cursor, &addressees); err != nil {
			return nil, err
		}
		x.CreatedAt = time.UnixMilli(created)
		x.Addressees = strings.Fields(addressees)
		out = append(out, x)
	}
	return out, rows.Err()
}

// Get returns the bridge name, or ErrNotFound.
func (b *Bridges) Get(ctx context.Context, name string) (Bridge, error) {
	list, err := b.List(ctx)
	if err != nil {
		return Bridge{}, err
	}
	for _, x := range list {
		if x.Name == name {
			return x, nil
		}
	}
	return Bridge{}, store.Refuse(ErrNotFound, "no bridge %s", name)
}

// SetPolicy changes the outbound policy of the bridge name.
func (b *Bridges) SetPolicy(ctx context.Context, name string, p Policy) error {
	if err := ValidPolicy(p); err != nil {
		return err
	}
	res, err := b.db.ExecContext(ctx, `UPDATE bridges SET policy = ? WHERE name = ?`, p, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.Refuse(ErrNotFound, "no bridge %s", name)
	}
	return nil
}

// In is a message that came in through a bridge.
type In struct {
	ID         string
	AuthorID   string
	AuthorName string
	Self       bool // the operator wrote it outside
	Text       string
	ReplyTo    string
	At         time.Time // zero for now
	Cursor     string    // "" leaves the stored cursor
	Addressed  bool
	// ThreadKey and ThreadTitle name the thread outside the message is in (see
	// rooms.Incoming); the hub sets them only for a bridge that declared threads.
	ThreadKey, ThreadTitle string
}

// Receive stores a message that came in through the bridge name, once per identifier outside,
// and keeps its cursor. A message marked Self is attributed to operator, when there is one. A
// line without an identifier only moves the cursor; one without text is stored as Empty. It
// returns the stored message's identifier, or 0 when nothing was stored.
func (b *Bridges) Receive(ctx context.Context, name string, in In, operator string) (int64, error) {
	if in.ID == "" && in.Cursor == "" {
		return 0, store.Refuse(store.ErrInvalid, "an in line needs an id or a cursor")
	}
	for _, v := range []string{in.ID, in.ReplyTo, in.Cursor, in.ThreadKey} {
		if len(v) > MaxIdentifier {
			return 0, store.Refuse(store.ErrInvalid, "identifiers and cursors are at most %d bytes", MaxIdentifier)
		}
	}
	text := strings.TrimSpace(in.Text)
	if text == "" {
		text = Empty
	}
	if utf8.RuneCountInString(text) > rooms.MaxBody {
		keep := rooms.MaxBody - utf8.RuneCountInString(Truncated)
		text = string([]rune(text)[:keep]) + Truncated
	}
	authorID := clean(in.AuthorID, maxName)
	authorName := clean(in.AuthorName, maxName)
	if authorName == "" {
		authorName = authorID
	}
	if authorName == "" {
		authorName = "unknown"
	}
	var id int64
	err := store.InTx(ctx, b.db, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE bridges SET cursor = CASE WHEN ? = '' THEN cursor ELSE ? END WHERE name = ?`, in.Cursor, in.Cursor, name)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return store.Refuse(ErrNotFound, "no bridge %s", name)
		}
		if in.ID == "" {
			return nil // only the cursor
		}
		var addressees []string
		if in.Addressed {
			if addressees, err = store.Strings(ctx, tx, `SELECT agent FROM bridge_agents WHERE bridge = ? ORDER BY agent`, name); err != nil {
				return err
			}
		}
		// an answer to an agent's message addresses that agent, as a mention would
		if in.ReplyTo != "" {
			var author string
			err := tx.QueryRowContext(ctx, `SELECT m.author FROM messages AS m JOIN agents AS a ON a.name = m.author
				WHERE m.room = ? AND m.ext_id = ?`, name, in.ReplyTo).Scan(&author)
			switch {
			case errors.Is(err, sql.ErrNoRows):
			case err != nil:
				return err
			case !slices.Contains(addressees, author):
				addressees = append(addressees, author)
			}
		}
		msg := rooms.Incoming{
			Room: name, ExtID: in.ID, AuthorID: authorID, AuthorName: authorName, Body: text, ReplyExtID: in.ReplyTo, At: in.At, Addressees: addressees,
			ThreadKey: in.ThreadKey, ThreadTitle: clean(in.ThreadTitle, rooms.MaxTitle),
		}
		if in.Self && operator != "" {
			msg.Author = operator
		}
		id, err = b.rooms.PostIncomingTx(ctx, tx, msg)
		return err
	})
	return id, err
}

// clean replaces control characters (line breaks included), line and paragraph separators and
// bidirectional controls with spaces (joiners and emoji tags stay), so a value from
// outside stays on the line it is shown on, trims it and cuts it to limit characters.
func clean(s string, limit int) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Zl, unicode.Zp, unicode.Bidi_Control) {
			return ' '
		}
		return r
	}, s))
	if utf8.RuneCountInString(s) > limit {
		s = strings.TrimSpace(string([]rune(s)[:limit]))
	}
	return s
}

// Caps is what a bridge says its platform can do; "" for what it did not say.
type Caps struct {
	Personas string // how it shows who wrote a message that goes out, such as "prefix"
	Threads  string // how it keeps threads, such as "topic"
}

// IsZero reports whether the bridge said nothing it can do.
func (c Caps) IsZero() bool { return c == Caps{} }

// ReadCaps reads the caps of a bridge's hello: the known entries whose value is a JSON string,
// cleaned like an author's name and cut to MaxCap characters; others are ignored.
func ReadCaps(raw map[string]json.RawMessage) Caps {
	read := func(key string) string {
		var v string
		if json.Unmarshal(raw[key], &v) != nil {
			return ""
		}
		return clean(v, MaxCap)
	}
	return Caps{Personas: read("personas"), Threads: read("threads")}
}

// Out is a message to hand to a bridge.
type Out struct {
	ID      int64
	Author  string
	Sigil   string // the author's sigil, "" when it chose none
	Text    string
	ReplyTo string // the identifier outside of the message of the same room it replies to, if any; for a reply in a thread whose replied-to message has none, the root's
	Thread  string // the key outside of the thread the message is a reply in, if it has one
}

// Unanswered returns the identifiers of the messages of the bridge's room that the bridge has to
// send and has not answered, in posting order.
func (b *Bridges) Unanswered(ctx context.Context, name string) ([]int64, error) {
	rows, err := b.db.QueryContext(ctx, `SELECT id FROM messages WHERE room = ? AND delivery = 'sending' ORDER BY id`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Outgoing returns the messages ids of the bridge's room that the bridge has to send, in
// posting order; ids no longer to send are left out. A message waits, and is left out too, while
// the message it replies to, or for a reply that falls back on it the root of its thread, is
// still going out without an identifier outside: it goes once that message was answered, so it
// can name it.
func (b *Bridges) Outgoing(ctx context.Context, name string, ids []int64) ([]Out, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	list, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	// a reply in a thread whose replied-to message has no identifier outside (none, a note, a
	// message that did not go out) replies to the thread's root there
	rows, err := b.db.QueryContext(ctx, `SELECT m.id, m.author, COALESCE(a.icon, ''), m.body, COALESCE(r.ext_id, t.ext_id, ''), COALESCE(k.key, '') FROM messages AS m
		LEFT JOIN agents AS a ON a.name = m.author
		LEFT JOIN messages AS r ON r.id = m.reply_to AND r.room = m.room
		LEFT JOIN messages AS t ON t.id = m.thread
		LEFT JOIN thread_keys AS k ON k.room = m.room AND k.root = m.thread
		WHERE m.id IN (SELECT value FROM json_each(?)) AND m.room = ? AND m.delivery = 'sending'
			AND NOT (COALESCE(r.delivery, '') = 'sending' AND r.ext_id IS NULL
				OR r.ext_id IS NULL AND COALESCE(t.delivery, '') = 'sending' AND t.ext_id IS NULL)
		ORDER BY m.id`, string(list), name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Out
	for rows.Next() {
		var o Out
		if err := rows.Scan(&o.ID, &o.Author, &o.Sigil, &o.Text, &o.ReplyTo, &o.Thread); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// Sent marks message id of the bridge's room, handed to the bridge, as sent with the identifier
// outside extID, so later in lines with that identifier, its echo, are ignored; an echo stored
// before the answer is merged into it. It reports whether the message was waiting for the
// answer, and whether another message of the room has extID, which the message then goes
// without.
func (b *Bridges) Sent(ctx context.Context, name string, id int64, extID string) (changed, taken bool, err error) {
	if len(extID) > MaxIdentifier {
		return false, false, store.Refuse(store.ErrInvalid, "identifiers are at most %d bytes", MaxIdentifier)
	}
	err = store.InTx(ctx, b.db, func(tx *sql.Tx) error {
		var err error
		changed, taken, err = rooms.SentTx(ctx, tx, id, name, extID)
		return err
	})
	return changed, taken, err
}

// Failed marks message id of the bridge's room, handed to the bridge, as not sent for reason,
// and the board tells its author in the room. It reports whether the message was waiting for
// the answer.
func (b *Bridges) Failed(ctx context.Context, name string, id int64, reason string) (bool, error) {
	reason = clean(reason, maxError)
	if reason == "" {
		reason = "the bridge gave no reason"
	}
	changed := false
	err := store.InTx(ctx, b.db, func(tx *sql.Tx) error {
		_, author, ok, err := rooms.SettleTx(ctx, tx, id, name, rooms.Sending, rooms.Failed, reason)
		if err != nil || !ok {
			return err
		}
		changed = true
		return b.tell(ctx, tx, author, name, id, reason)
	})
	return changed, err
}

// SendDue lets the scheduled messages of the bridge's room go out whose delay has passed, unless
// the policy is read, and reports whether any did.
func (b *Bridges) SendDue(ctx context.Context, name string) (bool, error) {
	res, err := b.db.ExecContext(ctx, `UPDATE messages SET delivery = 'sending'
		WHERE room = :room AND delivery = 'scheduled' AND scheduled_at <= :due
			AND EXISTS (SELECT 1 FROM bridges WHERE name = :room AND policy != 'read')`,
		sql.Named("room", name), sql.Named("due", b.now().Add(-b.delay()).UnixMilli()))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// SendScheduled lets the scheduled message id go out at once: its room must have a bridge whose
// policy is not read.
func (b *Bridges) SendScheduled(ctx context.Context, id int64) error {
	return store.InTx(ctx, b.db, func(tx *sql.Tx) error {
		var room, policy string
		err := tx.QueryRowContext(ctx, `SELECT m.room, COALESCE(b.policy, '') FROM messages AS m LEFT JOIN bridges AS b ON b.name = m.room
			WHERE m.id = ? AND m.delivery = 'scheduled'`, id).Scan(&room, &policy)
		if errors.Is(err, sql.ErrNoRows) {
			// its delay may have let it go out a moment before: then it is going out, as asked
			var delivery string
			err := tx.QueryRowContext(ctx, `SELECT COALESCE(delivery, '') FROM messages WHERE id = ?`, id).Scan(&delivery)
			if err == nil && (rooms.Delivery(delivery) == rooms.Sending || rooms.Delivery(delivery) == rooms.Sent) {
				return nil
			}
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			return store.Refuse(ErrNotFound, "message %d is not scheduled", id)
		}
		if err != nil {
			return err
		}
		if err := mayGoOut(room, Policy(policy)); err != nil {
			return err
		}
		_, _, _, err = rooms.SettleTx(ctx, tx, id, room, rooms.Scheduled, rooms.Sending, "")
		return err
	})
}

// mayGoOut refuses sending out of room, whose bridge has policy ("" without a bridge), when
// nothing goes out of it.
func mayGoOut(room string, policy Policy) error {
	switch policy {
	case "":
		return store.Refuse(store.ErrInvalid, "#%s has no bridge", room)
	case Read:
		return store.Refuse(rooms.ErrReadOnly, "#%s is read-only; nothing goes out", room)
	}
	return nil
}

// SendNoteOut sends the note id out: a new message of the note's author with its text, replying
// to what the note replies to and in the note's thread, goes out at once, and the note stays.
// Its room must have a bridge whose policy is not read. It returns the new message's identifier.
func (b *Bridges) SendNoteOut(ctx context.Context, id int64) (int64, error) {
	var out int64
	err := store.InTx(ctx, b.db, func(tx *sql.Tx) error {
		var room, author, body, policy string
		var reply, thread int64
		var also bool
		err := tx.QueryRowContext(ctx, `SELECT m.room, m.author, m.body, COALESCE(m.reply_to, 0), COALESCE(m.thread, 0), m.also, COALESCE(b.policy, '')
			FROM messages AS m LEFT JOIN bridges AS b ON b.name = m.room WHERE m.id = ? AND m.note`, id).Scan(&room, &author, &body, &reply, &thread, &also, &policy)
		if errors.Is(err, sql.ErrNoRows) {
			return store.Refuse(ErrNotFound, "message %d is not a note", id)
		}
		if err != nil {
			return err
		}
		if err := mayGoOut(room, Policy(policy)); err != nil {
			return err
		}
		out, err = b.rooms.SendOutTx(ctx, tx, rooms.Posting{Author: author, Room: room, Body: body, ReplyTo: reply, Thread: thread, Also: also})
		return err
	})
	return out, err
}

// CancelScheduled cancels the scheduled message id, so it never goes out, and the board tells its
// author in the room.
func (b *Bridges) CancelScheduled(ctx context.Context, id int64, operator string) error {
	return store.InTx(ctx, b.db, func(tx *sql.Tx) error {
		room, author, ok, err := rooms.SettleTx(ctx, tx, id, "", rooms.Scheduled, rooms.Cancelled, "")
		if err != nil {
			return err
		}
		if !ok {
			return store.Refuse(ErrNotFound, "message %d is not scheduled", id)
		}
		who := "the operator"
		if operator != "" {
			who = operator
		}
		return b.tell(ctx, tx, author, room, id, who+" cancelled it")
	})
}

// tell has the board say in room that author's message id was not sent and why.
func (b *Bridges) tell(ctx context.Context, tx *sql.Tx, author, room string, id int64, why string) error {
	_, err := b.rooms.PostTx(ctx, tx, rooms.Board, room, fmt.Sprintf("@%s your message %d was not sent: %s.", author, id, strings.TrimSuffix(why, ".")), id)
	return err
}

// Notice has the board post text in the bridge's room, waking no one.
func (b *Bridges) Notice(ctx context.Context, name, text string) error {
	return store.InTx(ctx, b.db, func(tx *sql.Tx) error { return b.rooms.NoticeTx(ctx, tx, name, text) })
}
