// Package queue implements resource queues: resources with a number of slots that agents
// queue for, hold under a lease and give back. A lock is a resource with one slot.
package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	// DefaultLease is how long a slot is held when the agent names no lease.
	DefaultLease = 30 * time.Minute
	// ClaimWindow is how long an offered slot waits to be claimed.
	ClaimWindow = 2 * time.Minute
	// MaxMissedTurns is how many turns an agent may miss before it leaves the queue.
	MaxMissedTurns = 2
	// MaxLease is the longest lease an agent may ask for.
	MaxLease = 7 * 24 * time.Hour
	// MaxSlots is the largest number of slots a resource may have.
	MaxSlots = 1000
)

// State of an agent in a resource's queue.
type State string

const (
	Waiting State = "waiting"
	Offered State = "offered"
	Held    State = "held"
)

// Entry is one agent in one resource's queue.
type Entry struct {
	Key      string
	Agent    string
	Note     string
	State    State
	Position int // among waiting agents, from 1; 0 when offered or held
	JoinedAt time.Time
	Expires  time.Time // lease end when held, claim deadline when offered, zero when waiting
	Lease    time.Duration
}

// Resource is a resource with its queue: holders and offers first, then waiting agents in order.
type Resource struct {
	Key     string
	Slots   int
	Entries []Entry
}

// Holders returns the entries that hold a slot.
func (r Resource) Holders() []Entry {
	var out []Entry
	for _, e := range r.Entries {
		if e.State == Held {
			out = append(out, e)
		}
	}
	return out
}

var (
	// ErrInvalid marks a request with an invalid key, agent name or value.
	ErrInvalid = errors.New("invalid request")
	// ErrNotQueued means the agent neither holds nor waits for the resource.
	ErrNotQueued = errors.New("not queued")
	// ErrNotYourTurn means the agent waits and has no slot to renew.
	ErrNotYourTurn = errors.New("not your turn yet")
)

// ForbiddenError refuses to remove another agent's entry without force.
type ForbiddenError struct{ Agent string }

func (e *ForbiddenError) Error() string {
	return fmt.Sprintf("this place in the queue belongs to %s; force the release only if they are gone", e.Agent)
}

var keyRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]{0,63}$`)

// ValidKey reports whether key may name a resource.
func ValidKey(key string) bool {
	return keyRE.MatchString(key) && !strings.Contains(key, "..")
}

// Queue keeps resource queues in a SQLite database.
type Queue struct {
	db  *sql.DB
	now func() time.Time
}

// New returns a Queue over db; now is the clock (time.Now when nil).
func New(db *sql.DB, now func() time.Time) *Queue {
	if now == nil {
		now = time.Now
	}
	return &Queue{db: db, now: now}
}

func checkKey(key string) error {
	if !ValidKey(key) {
		return fmt.Errorf("%w: resource key %q: up to 64 lowercase letters, digits, '.', '_', '-', '/', no '..'", ErrInvalid, key)
	}
	return nil
}

func checkAgent(agent string) error {
	if strings.TrimSpace(agent) == "" {
		return fmt.Errorf("%w: agent name is required", ErrInvalid)
	}
	return nil
}

// Join puts agent in the queue of key, granting a slot at once when one is free and nobody
// waits. Joining again keeps the agent's place and updates its note. With noWait (a lock), the
// agent ends up holding a slot or the request is refused and the returned entry is nil: a
// holder's lease is renewed for lease, and an agent that only waits or is offered is refused.
// A zero lease means DefaultLease.
func (q *Queue) Join(ctx context.Context, key, agent, note string, lease time.Duration, noWait bool) (*Entry, Resource, error) {
	if err := checkKey(key); err != nil {
		return nil, Resource{}, err
	}
	if err := checkAgent(agent); err != nil {
		return nil, Resource{}, err
	}
	if lease == 0 {
		lease = DefaultLease
	}
	if lease < 0 || lease > MaxLease {
		return nil, Resource{}, fmt.Errorf("%w: lease must be between 1s and %s", ErrInvalid, MaxLease)
	}
	var res Resource
	var joined bool
	err := q.tx(ctx, func(tx *sql.Tx, now time.Time) error {
		if err := ensureResource(ctx, tx, key); err != nil {
			return err
		}
		if err := settle(ctx, tx, key, now); err != nil {
			return err
		}
		r, err := load(ctx, tx, key)
		if err != nil {
			return err
		}
		if existing := find(r, agent); existing != nil {
			if noWait && existing.State != Held {
				res = r
				return nil
			}
			var err error
			if noWait {
				_, err = tx.ExecContext(ctx, `UPDATE entries SET note = ?, lease_ms = ?, expires_at = ? WHERE key = ? AND agent = ?`,
					note, lease.Milliseconds(), ms(now.Add(lease)), key, agent)
			} else {
				_, err = tx.ExecContext(ctx, `UPDATE entries SET note = ? WHERE key = ? AND agent = ?`, note, key, agent)
			}
			if err != nil {
				return err
			}
			joined = true
			res, err = load(ctx, tx, key)
			return err
		}
		active, waiting := counts(r)
		free := active < r.Slots && waiting == 0
		switch {
		case free:
			_, err = tx.ExecContext(ctx, `INSERT INTO entries (key, agent, note, state, seq, lease_ms, joined_at, expires_at)
				VALUES (?, ?, ?, 'held', `+nextSeq+`, ?, ?, ?)`, key, agent, note, key, lease.Milliseconds(), ms(now), ms(now.Add(lease)))
		case noWait:
			res = r
			return nil
		default:
			_, err = tx.ExecContext(ctx, `INSERT INTO entries (key, agent, note, state, seq, lease_ms, joined_at)
				VALUES (?, ?, ?, 'waiting', `+nextSeq+`, ?, ?)`, key, agent, note, key, lease.Milliseconds(), ms(now))
		}
		if err != nil {
			return err
		}
		joined = true
		res, err = load(ctx, tx, key)
		return err
	})
	if err != nil || !joined {
		return nil, res, err
	}
	return find(res, agent), res, nil
}

// Claim turns an offered slot into a held one, or returns the entry unchanged. Waiting agents
// call it while they wait, so a slot offered to a waiting agent is claimed at once.
func (q *Queue) Claim(ctx context.Context, key, agent string) (*Entry, error) {
	return q.touch(ctx, key, agent, false)
}

// Renew claims an offered slot or extends a held lease by the entry's lease duration.
func (q *Queue) Renew(ctx context.Context, key, agent string) (*Entry, error) {
	return q.touch(ctx, key, agent, true)
}

func (q *Queue) touch(ctx context.Context, key, agent string, renew bool) (*Entry, error) {
	if err := checkKey(key); err != nil {
		return nil, err
	}
	if err := checkAgent(agent); err != nil {
		return nil, err
	}
	var out *Entry
	err := q.tx(ctx, func(tx *sql.Tx, now time.Time) error {
		if err := settle(ctx, tx, key, now); err != nil {
			return err
		}
		r, err := load(ctx, tx, key)
		if err != nil {
			return err
		}
		e := find(r, agent)
		if e == nil {
			return fmt.Errorf("%w: %s is not in the queue of %s", ErrNotQueued, agent, key)
		}
		if e.State == Waiting {
			if renew {
				return fmt.Errorf("%w: %s waits at position %d for %s", ErrNotYourTurn, agent, e.Position, key)
			}
			out = e
			return nil
		}
		if e.State == Held && !renew {
			out = e
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE entries SET state = 'held', expires_at = ?, skips = 0 WHERE key = ? AND agent = ?`,
			ms(now.Add(e.Lease)), key, agent); err != nil {
			return err
		}
		r, err = load(ctx, tx, key)
		if err != nil {
			return err
		}
		out = find(r, agent)
		return nil
	})
	return out, err
}

// Release removes agent from the queue of key on behalf of actor. Another agent's entry is
// removed only with force, and the forced removal is recorded. It reports whether the agent
// was queued.
func (q *Queue) Release(ctx context.Context, key, agent, actor string, force bool) (bool, error) {
	if err := checkKey(key); err != nil {
		return false, err
	}
	if err := checkAgent(agent); err != nil {
		return false, err
	}
	if actor == "" {
		actor = agent
	}
	released := false
	err := q.tx(ctx, func(tx *sql.Tx, now time.Time) error {
		if err := settle(ctx, tx, key, now); err != nil {
			return err
		}
		r, err := load(ctx, tx, key)
		if err != nil {
			return err
		}
		if find(r, agent) == nil {
			return nil
		}
		if actor != agent {
			if !force {
				return &ForbiddenError{Agent: agent}
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO removals (at, key, agent, actor) VALUES (?, ?, ?, ?)`,
				ms(now), key, agent, actor); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM entries WHERE key = ? AND agent = ?`, key, agent); err != nil {
			return err
		}
		released = true
		return settle(ctx, tx, key, now)
	})
	return released, err
}

// SetSlots sets how many agents may hold key at once. Current holders keep their slots.
func (q *Queue) SetSlots(ctx context.Context, key string, slots int) (Resource, error) {
	if err := checkKey(key); err != nil {
		return Resource{}, err
	}
	if slots < 1 || slots > MaxSlots {
		return Resource{}, fmt.Errorf("%w: slots must be between 1 and %d", ErrInvalid, MaxSlots)
	}
	var res Resource
	err := q.tx(ctx, func(tx *sql.Tx, now time.Time) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO resources (key, slots, slots_set) VALUES (?, ?, 1)
			ON CONFLICT (key) DO UPDATE SET slots = excluded.slots, slots_set = 1`, key, slots); err != nil {
			return err
		}
		if err := settle(ctx, tx, key, now); err != nil {
			return err
		}
		var err error
		res, err = load(ctx, tx, key)
		return err
	})
	return res, err
}

// List returns resources that someone holds or waits for, or whose slots were set; only key
// when key is not empty.
func (q *Queue) List(ctx context.Context, key string) ([]Resource, error) {
	if key != "" {
		if err := checkKey(key); err != nil {
			return nil, err
		}
	}
	var out []Resource
	err := q.tx(ctx, func(tx *sql.Tx, now time.Time) error {
		keys, err := listKeys(ctx, tx, key)
		if err != nil {
			return err
		}
		for _, k := range keys {
			if err := settle(ctx, tx, k, now); err != nil {
				return err
			}
			r, err := load(ctx, tx, k)
			if err != nil {
				return err
			}
			var set bool
			if err := tx.QueryRowContext(ctx, `SELECT slots_set = 1 FROM resources WHERE key = ?`, k).Scan(&set); err != nil {
				return err
			}
			if len(r.Entries) > 0 || set {
				out = append(out, r)
			}
		}
		return nil
	})
	return out, err
}

// EntriesOf returns every place agent has in any resource queue, ordered by resource key.
func (q *Queue) EntriesOf(ctx context.Context, agent string) ([]Entry, error) {
	var out []Entry
	err := q.tx(ctx, func(tx *sql.Tx, now time.Time) error {
		keys, err := keysOf(ctx, tx, agent)
		if err != nil {
			return err
		}
		for _, k := range keys {
			if err := settle(ctx, tx, k, now); err != nil {
				return err
			}
			r, err := load(ctx, tx, k)
			if err != nil {
				return err
			}
			if e := find(r, agent); e != nil {
				out = append(out, *e)
			}
		}
		return nil
	})
	return out, err
}

// ReleaseAgent removes agent from every resource queue and returns the keys it left.
func (q *Queue) ReleaseAgent(ctx context.Context, agent string) ([]string, error) {
	var left []string
	err := q.tx(ctx, func(tx *sql.Tx, now time.Time) error {
		keys, err := keysOf(ctx, tx, agent)
		if err != nil {
			return err
		}
		for _, k := range keys {
			if _, err := tx.ExecContext(ctx, `DELETE FROM entries WHERE key = ? AND agent = ?`, k, agent); err != nil {
				return err
			}
			if err := settle(ctx, tx, k, now); err != nil {
				return err
			}
		}
		left = keys
		return nil
	})
	return left, err
}

func keysOf(ctx context.Context, tx *sql.Tx, agent string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT key FROM entries WHERE agent = ? ORDER BY key`, agent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// Sweep settles every resource with an expired lease or claim deadline and returns the keys
// that changed. The hub calls it periodically so waiting agents advance without other traffic.
func (q *Queue) Sweep(ctx context.Context) ([]string, error) {
	var changed []string
	err := q.tx(ctx, func(tx *sql.Tx, now time.Time) error {
		rows, err := tx.QueryContext(ctx, `SELECT DISTINCT key FROM entries WHERE expires_at IS NOT NULL AND expires_at <= ? ORDER BY key`, ms(now))
		if err != nil {
			return err
		}
		var keys []string
		for rows.Next() {
			var k string
			if err := rows.Scan(&k); err != nil {
				rows.Close()
				return err
			}
			keys = append(keys, k)
		}
		rows.Close()
		for _, k := range keys {
			if err := settle(ctx, tx, k, now); err != nil {
				return err
			}
		}
		changed = keys
		return rows.Err()
	})
	return changed, err
}

// --- internals ---------------------------------------------------------------

func (q *Queue) tx(ctx context.Context, fn func(*sql.Tx, time.Time) error) error {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx, q.now()); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func ensureResource(ctx context.Context, tx *sql.Tx, key string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO resources (key) VALUES (?) ON CONFLICT (key) DO NOTHING`, key)
	return err
}

// settle applies everything time has decided for key: ends expired leases, moves agents that
// missed their turn to the end of the queue (or out of it), and offers free slots in order.
func settle(ctx context.Context, tx *sql.Tx, key string, now time.Time) error {
	t := ms(now)
	if _, err := tx.ExecContext(ctx, `DELETE FROM entries WHERE key = ? AND state = 'held' AND expires_at <= ?`, key, t); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM entries WHERE key = ? AND state = 'offered' AND expires_at <= ? AND skips + 1 >= ?`,
		key, t, MaxMissedTurns); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT agent FROM entries WHERE key = ? AND state = 'offered' AND expires_at <= ? ORDER BY seq`, key, t)
	if err != nil {
		return err
	}
	var missed []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			rows.Close()
			return err
		}
		missed = append(missed, a)
	}
	rows.Close()
	for _, a := range missed {
		if _, err := tx.ExecContext(ctx, `UPDATE entries SET state = 'waiting', expires_at = NULL, skips = skips + 1,
			seq = `+nextSeq+` WHERE key = ? AND agent = ?`, key, key, a); err != nil {
			return err
		}
	}
	var slots, active int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT slots FROM resources WHERE key = ?), 1)`, key).Scan(&slots); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries WHERE key = ? AND state IN ('held', 'offered')`, key).Scan(&active); err != nil {
		return err
	}
	if free := slots - active; free > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE entries SET state = 'offered', expires_at = ?
			WHERE key = ? AND agent IN (SELECT agent FROM entries WHERE key = ? AND state = 'waiting' ORDER BY seq LIMIT ?)`,
			ms(now.Add(ClaimWindow)), key, key, free); err != nil {
			return err
		}
	}
	return rows.Err()
}

func load(ctx context.Context, tx *sql.Tx, key string) (Resource, error) {
	r := Resource{Key: key, Slots: 1}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT slots FROM resources WHERE key = ?), 1)`, key).Scan(&r.Slots); err != nil {
		return r, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT agent, note, state, lease_ms, joined_at, expires_at FROM entries WHERE key = ?
		ORDER BY CASE state WHEN 'held' THEN 0 WHEN 'offered' THEN 1 ELSE 2 END, seq`, key)
	if err != nil {
		return r, err
	}
	defer rows.Close()
	pos := 0
	for rows.Next() {
		var e Entry
		var leaseMS, joined int64
		var expires sql.NullInt64
		if err := rows.Scan(&e.Agent, &e.Note, &e.State, &leaseMS, &joined, &expires); err != nil {
			return r, err
		}
		e.Key = key
		e.Lease = time.Duration(leaseMS) * time.Millisecond
		e.JoinedAt = time.UnixMilli(joined)
		if expires.Valid {
			e.Expires = time.UnixMilli(expires.Int64)
		}
		if e.State == Waiting {
			pos++
			e.Position = pos
		}
		r.Entries = append(r.Entries, e)
	}
	return r, rows.Err()
}

func listKeys(ctx context.Context, tx *sql.Tx, key string) ([]string, error) {
	query, args := `SELECT key FROM resources ORDER BY key`, []any{}
	if key != "" {
		query, args = `SELECT key FROM resources WHERE key = ?`, []any{key}
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func find(r Resource, agent string) *Entry {
	for i := range r.Entries {
		if r.Entries[i].Agent == agent {
			e := r.Entries[i]
			return &e
		}
	}
	return nil
}

func counts(r Resource) (active, waiting int) {
	for _, e := range r.Entries {
		if e.State == Waiting {
			waiting++
		} else {
			active++
		}
	}
	return active, waiting
}

// nextSeq is the SQL for the position after the last entry of a resource; it takes the key.
const nextSeq = `(SELECT COALESCE(MAX(seq), 0) + 1 FROM entries WHERE key = ?)`

func ms(t time.Time) int64 { return t.UnixMilli() }
