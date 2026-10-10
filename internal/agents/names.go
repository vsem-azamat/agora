package agents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/vsem-azamat/agora/internal/store"
)

// ErrTaken means another agent has the name, or had it.
var ErrTaken = errors.New("name taken")

// Icons are the sigils an agent may choose; the owl is the board's.
var Icons = []string{
	"helmet", "lyre", "trireme", "column", "hoplon", "trident", "torch", "olive", "scales", "lamp", "mask", "key", "divider",
	"arrow", "anchor", "wheat", "anvil", "rod", "eye", "kantharos", "labrys", "bolt", "sun", "moon", "dolphin", "amphora",
}

// Pigments are the colours an agent may choose.
var Pigments = []string{"terracotta", "ochre", "olive", "lapis", "tyrian", "umber", "verdigris", "soot"}

// FormerName is a name an agent gave up by renaming itself.
type FormerName struct {
	Name string
	At   time.Time
}

// checkChoice refuses a value that is set, not empty and not one of allowed.
func checkChoice(what string, v *string, allowed []string) error {
	if v == nil {
		return nil
	}
	if s := strings.TrimSpace(*v); s != "" && !slices.Contains(allowed, s) {
		return fmt.Errorf("%w: %s %q: one of %s, or empty to unset", store.ErrInvalid, what, s, strings.Join(allowed, ", "))
	}
	return nil
}

// renames move every column that holds an agent's name from :old to :new.
var renames = []string{
	"UPDATE agents SET name = :new WHERE name = :old",
	"UPDATE former_names SET agent = :new WHERE agent = :old",
	"UPDATE sessions SET agent = :new WHERE agent = :old",
	"UPDATE entries SET agent = :new WHERE agent = :old",
	"UPDATE removals SET agent = :new WHERE agent = :old",
	"UPDATE removals SET actor = :new WHERE actor = :old",
	"UPDATE rooms SET created_by = :new WHERE created_by = :old",
	"UPDATE messages SET author = :new WHERE author = :old",
	"UPDATE mentions SET agent = :new WHERE agent = :old",
	"UPDATE subscriptions SET agent = :new WHERE agent = :old",
	"UPDATE read_positions SET agent = :new WHERE agent = :old",
	"UPDATE read_marks SET agent = :new WHERE agent = :old",
	"UPDATE proposals SET author = :new WHERE author = :old",
	"UPDATE proposals SET closed_by = :new WHERE closed_by = :old",
	"UPDATE votes SET agent = :new WHERE agent = :old",
	"UPDATE charter_changes SET changed_by = :new WHERE changed_by = :old",
	"UPDATE charter SET changed_by = :new WHERE changed_by = :old",
	"UPDATE pull_requests SET agent = :new WHERE agent = :old",
}

// RenameTx gives agent the name name inside the caller's transaction: every row that names
// the agent moves to the new name, and the old name is recorded as a former name. The caller
// checks that the agent may take the name (FreeForTx).
func RenameTx(ctx context.Context, tx *sql.Tx, agent, name string, now time.Time) error {
	// references are checked when the transaction commits, once all of them have moved
	if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
		return err
	}
	// mentions of the new name posted before the rename addressed nobody
	if _, err := tx.ExecContext(ctx, `DELETE FROM mentions WHERE agent = ?`, name); err != nil {
		return err
	}
	for _, stmt := range renames {
		if _, err := tx.ExecContext(ctx, stmt, sql.Named("new", name), sql.Named("old", agent)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM former_names WHERE name = ?`, name); err != nil { // a former name taken back
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO former_names (name, agent, renamed_at) VALUES (?, ?, ?)`, agent, name, now.UnixMilli())
	return err
}

// FreeForTx returns ErrTaken when agent may not rename itself to name: another agent has the
// name or gave it up, or the name was used in a resource queue without having joined (it holds
// or waits for a resource, or appears in the record of forced removals), whose records would
// otherwise pass to the agent.
func FreeForTx(ctx context.Context, tx *sql.Tx, agent, name string) error {
	var used int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM agents WHERE name = :n) OR EXISTS (SELECT 1 FROM entries WHERE agent = :n)
		OR EXISTS (SELECT 1 FROM removals WHERE agent = :n OR actor = :n)`, sql.Named("n", name)).Scan(&used); err != nil {
		return err
	}
	if used != 0 {
		return fmt.Errorf("%w: %q is in use; pick another name", ErrTaken, name)
	}
	owner, err := formerOwner(ctx, tx, name)
	if err != nil || owner == "" || owner == agent {
		return err
	}
	return nowCalled(ErrTaken, name, owner)
}

// NotFormerTx returns ErrTaken when name is a name some agent gave up, saying what that agent is
// called now.
func NotFormerTx(ctx context.Context, tx *sql.Tx, name string) error {
	owner, err := formerOwner(ctx, tx, name)
	if err != nil || owner == "" {
		return err
	}
	return nowCalled(ErrTaken, name, owner)
}

// formerOwner returns the agent that gave up name, or "".
func formerOwner(ctx context.Context, tx *sql.Tx, name string) (string, error) {
	var owner string
	err := tx.QueryRowContext(ctx, `SELECT agent FROM former_names WHERE name = ?`, name).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return owner, err
}

// FormerNameError refuses a name an agent gave up, saying what the agent is called now. It
// wraps ErrTaken or ErrUnknown.
type FormerNameError struct {
	Name, Current string
	sentinel      error
}

func (e *FormerNameError) Error() string {
	return fmt.Sprintf("%v: %q is now called %q; use %s", e.sentinel, e.Name, e.Current, e.Current)
}

func (e *FormerNameError) Unwrap() error { return e.sentinel }

func nowCalled(sentinel error, name, owner string) error {
	return &FormerNameError{Name: name, Current: owner, sentinel: sentinel}
}

// formerlyColumn lists agent a's former names, newest first, as "fixer:1760000000000 ...".
const formerlyColumn = `COALESCE((SELECT group_concat(f.name || ':' || f.renamed_at, ' ' ORDER BY f.renamed_at DESC, f.name)
	FROM former_names AS f WHERE f.agent = a.name), '')`

func parseFormerly(s string) []FormerName {
	var out []FormerName
	for _, f := range strings.Fields(s) {
		name, at, _ := strings.Cut(f, ":")
		ms, err := strconv.ParseInt(at, 10, 64)
		if err != nil {
			continue
		}
		out = append(out, FormerName{Name: name, At: time.UnixMilli(ms)})
	}
	return out
}
