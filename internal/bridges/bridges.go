// Package bridges keeps the bridges that connect rooms to chats outside Agora: which room each
// one serves, its command and outbound policy, the messages that come in through it and what
// goes out.
package bridges

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vsem-azamat/agora/internal/agents"
	"github.com/vsem-azamat/agora/internal/rooms"
	"github.com/vsem-azamat/agora/internal/store"
)

// Policy says what goes out of a bridged room.
type Policy string

// Outbound policies.
const (
	Approve Policy = "approve" // agents' messages wait for the operator
	Open    Policy = "open"    // every message goes out at once
	Read    Policy = "read"    // nothing goes out; agents may not post
)

const (
	// Truncated ends a message from outside that was shortened to rooms.MaxBody characters.
	Truncated = " … [truncated]"
	// maxError bounds the reason a failed message keeps.
	maxError = 500
)

var (
	// ErrNotFound means there is no such bridge, or no such pending message.
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
		return false, fmt.Errorf("%w: bridge name %q: %s", store.ErrInvalid, name, rooms.NameRule)
	}
	if name == rooms.General {
		return false, fmt.Errorf("%w: #%s is everyone's room and cannot be bridged", store.ErrInvalid, rooms.General)
	}
	if command == "" {
		return false, fmt.Errorf("%w: a bridge needs a command", store.ErrInvalid)
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
			return fmt.Errorf("%w: #%s already has a bridge; remove it first with `agora bridge remove %s`", ErrExists, name, name)
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM rooms WHERE name = ?`, name).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if err := b.rooms.CreateTx(ctx, tx, name, purpose, agent); err != nil {
				return err
			}
			created = true
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO bridges (name, command, created_by, created_at) VALUES (?, ?, ?, ?)`,
			name, command, agent, b.now().UnixMilli()); err != nil {
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
// in the room that it is no longer bridged.
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
			return fmt.Errorf("%w: no bridge %s; see `agora bridge list`", ErrNotFound, name)
		}
		_, err = b.rooms.PostTx(ctx, tx, rooms.Board, name, agent+" removed the bridge of this room; the room stays, no longer bridged.", 0)
		return err
	})
}

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
	return Bridge{}, fmt.Errorf("%w: no bridge %s", ErrNotFound, name)
}

// SetPolicy changes the outbound policy of the bridge name.
func (b *Bridges) SetPolicy(ctx context.Context, name string, p Policy) error {
	switch p {
	case Approve, Open, Read:
	default:
		return fmt.Errorf("%w: policy %q: approve, open or read", store.ErrInvalid, p)
	}
	res, err := b.db.ExecContext(ctx, `UPDATE bridges SET policy = ? WHERE name = ?`, p, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: no bridge %s", ErrNotFound, name)
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
}

// Receive stores a message that came in through the bridge name, once per identifier outside,
// and keeps its cursor. A message marked Self is attributed to operator, when there is one. It
// returns the stored message's identifier, or 0 when the message was already stored.
func (b *Bridges) Receive(ctx context.Context, name string, in In, operator string) (int64, error) {
	if in.ID == "" || strings.TrimSpace(in.Text) == "" {
		return 0, fmt.Errorf("%w: an in line needs an id and text", store.ErrInvalid)
	}
	text := strings.TrimSpace(in.Text)
	if utf8.RuneCountInString(text) > rooms.MaxBody {
		keep := rooms.MaxBody - utf8.RuneCountInString(Truncated)
		text = string([]rune(text)[:keep]) + Truncated
	}
	authorName := strings.TrimSpace(in.AuthorName)
	if authorName == "" {
		authorName = in.AuthorID
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
			return fmt.Errorf("%w: no bridge %s", ErrNotFound, name)
		}
		var addressees []string
		if in.Addressed {
			if addressees, err = store.Strings(ctx, tx, `SELECT agent FROM bridge_agents WHERE bridge = ? ORDER BY agent`, name); err != nil {
				return err
			}
		}
		msg := rooms.Incoming{
			Room: name, ExtID: in.ID, AuthorID: in.AuthorID, AuthorName: authorName, Body: text, ReplyExtID: in.ReplyTo, At: in.At, Addressees: addressees,
		}
		if in.Self && operator != "" {
			msg.Author = operator
		}
		id, err = b.rooms.PostIncomingTx(ctx, tx, msg)
		return err
	})
	return id, err
}

// Out is a message to hand to a bridge.
type Out struct {
	ID      int64
	Author  string
	Text    string
	ReplyTo string // the identifier outside of the message it replies to, if any
}

// Outgoing returns up to limit messages of the bridge's room after the message after that the
// bridge has to send, in posting order.
func (b *Bridges) Outgoing(ctx context.Context, name string, after int64, limit int) ([]Out, error) {
	rows, err := b.db.QueryContext(ctx, `SELECT m.id, m.author, m.body, COALESCE(r.ext_id, '') FROM messages AS m
		LEFT JOIN messages AS r ON r.id = m.reply_to
		WHERE m.room = ? AND m.delivery = 'sending' AND m.id > ? ORDER BY m.id LIMIT ?`, name, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Out
	for rows.Next() {
		var o Out
		if err := rows.Scan(&o.ID, &o.Author, &o.Text, &o.ReplyTo); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// Sent marks message id of the bridge's room, handed to the bridge, as sent with the identifier
// outside extID, which later in lines with the same identifier repeat. It reports whether the
// message was waiting for the answer.
func (b *Bridges) Sent(ctx context.Context, name string, id int64, extID string) (bool, error) {
	changed := false
	err := store.InTx(ctx, b.db, func(tx *sql.Tx) error {
		var err error
		_, _, changed, err = rooms.SettleTx(ctx, tx, id, name, rooms.Sending, rooms.Sent, extID, "")
		return err
	})
	return changed, err
}

// Failed marks message id of the bridge's room, handed to the bridge, as not sent for reason,
// and the board tells its author in the room. It reports whether the message was waiting for
// the answer.
func (b *Bridges) Failed(ctx context.Context, name string, id int64, reason string) (bool, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "the bridge gave no reason"
	}
	if utf8.RuneCountInString(reason) > maxError {
		reason = string([]rune(reason)[:maxError]) + " …"
	}
	changed := false
	err := store.InTx(ctx, b.db, func(tx *sql.Tx) error {
		_, author, ok, err := rooms.SettleTx(ctx, tx, id, name, rooms.Sending, rooms.Failed, "", reason)
		if err != nil || !ok {
			return err
		}
		changed = true
		return b.tell(ctx, tx, author, name, id, reason)
	})
	return changed, err
}

// SendPending lets the pending message id go out: its room must have a bridge whose policy is
// not read.
func (b *Bridges) SendPending(ctx context.Context, id int64) error {
	return store.InTx(ctx, b.db, func(tx *sql.Tx) error {
		var room, policy string
		err := tx.QueryRowContext(ctx, `SELECT m.room, COALESCE(b.policy, '') FROM messages AS m LEFT JOIN bridges AS b ON b.name = m.room
			WHERE m.id = ? AND m.delivery = 'pending'`, id).Scan(&room, &policy)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: message %d is not pending", ErrNotFound, id)
		}
		if err != nil {
			return err
		}
		switch Policy(policy) {
		case "":
			return fmt.Errorf("%w: #%s has no bridge", store.ErrInvalid, room)
		case Read:
			return fmt.Errorf("%w: #%s is read-only; nothing goes out", rooms.ErrReadOnly, room)
		}
		_, _, _, err = rooms.SettleTx(ctx, tx, id, room, rooms.Pending, rooms.Sending, "", "")
		return err
	})
}

// DeclinePending keeps the pending message id from ever going out, and the board tells its
// author in the room.
func (b *Bridges) DeclinePending(ctx context.Context, id int64, operator string) error {
	return store.InTx(ctx, b.db, func(tx *sql.Tx) error {
		room, author, ok, err := rooms.SettleTx(ctx, tx, id, "", rooms.Pending, rooms.Declined, "", "")
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: message %d is not pending", ErrNotFound, id)
		}
		who := "the operator"
		if operator != "" {
			who = operator
		}
		return b.tell(ctx, tx, author, room, id, who+" declined it")
	})
}

// tell has the board say in room that author's message id was not sent and why.
func (b *Bridges) tell(ctx context.Context, tx *sql.Tx, author, room string, id int64, why string) error {
	_, err := b.rooms.PostTx(ctx, tx, rooms.Board, room, fmt.Sprintf("@%s your message %d was not sent: %s.", author, id, strings.TrimSuffix(why, ".")), id)
	return err
}

// Notice has the board post text in the bridge's room.
func (b *Bridges) Notice(ctx context.Context, name, text string) error {
	return store.InTx(ctx, b.db, func(tx *sql.Tx) error {
		_, err := b.rooms.PostTx(ctx, tx, rooms.Board, name, text, 0)
		return err
	})
}
