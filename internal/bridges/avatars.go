package bridges

import (
	"context"
	"database/sql"
	"errors"

	"github.com/vsem-azamat/agora/internal/avatars"
	"github.com/vsem-azamat/agora/internal/store"
)

// MaxOutsideAvatars bounds how many people outside a bridge keeps avatars for.
const MaxOutsideAvatars = 1000

// ErrAvatarsFull means the bridge keeps avatars for MaxOutsideAvatars people already.
var ErrAvatarsFull = errors.New("too many avatars")

// AvatarRev returns the rev of the avatar stored for the person outside author in the bridge's
// room, and whether one is stored.
func (b *Bridges) AvatarRev(ctx context.Context, name, author string) (string, bool, error) {
	var rev string
	err := b.db.QueryRowContext(ctx, `SELECT rev FROM outside_avatars WHERE room = ? AND author = ?`, name, clean(author, maxName)).Scan(&rev)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return rev, err == nil, err
}

// SetAvatar stores p as the avatar, named rev by the bridge, of the person outside author in the
// bridge's room; a nil p records that the person has none. A person new to the bridge is refused
// with ErrAvatarsFull once it keeps avatars for MaxOutsideAvatars people.
func (b *Bridges) SetAvatar(ctx context.Context, name, author, rev string, p *avatars.Picture) error {
	author = clean(author, maxName)
	if author == "" {
		return store.Refuse(store.ErrInvalid, "an avatar line needs an author")
	}
	if len(rev) > MaxIdentifier {
		return store.Refuse(store.ErrInvalid, "identifiers are at most %d bytes", MaxIdentifier)
	}
	return store.InTx(ctx, b.db, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM bridges WHERE name = ?`, name).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return store.Refuse(ErrNotFound, "no bridge %s", name)
		}
		var old sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT avatar FROM outside_avatars WHERE room = ? AND author = ?`, name, author).Scan(&old)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM outside_avatars WHERE room = ?`, name).Scan(&n); err != nil {
				return err
			}
			if n >= MaxOutsideAvatars {
				return store.Refuse(ErrAvatarsFull, "#%s keeps avatars for %d people already", name, MaxOutsideAvatars)
			}
		case err != nil:
			return err
		}
		var hash sql.NullString
		if p != nil {
			if err := avatars.PutTx(ctx, tx, *p, b.now()); err != nil {
				return err
			}
			hash = sql.NullString{String: p.Hash, Valid: true}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO outside_avatars (room, author, rev, avatar, updated_at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (room, author) DO UPDATE SET rev = excluded.rev, avatar = excluded.avatar, updated_at = excluded.updated_at`,
			name, author, rev, hash, b.now().UnixMilli()); err != nil {
			return err
		}
		return avatars.DropUnusedTx(ctx, tx, old.String)
	})
}

// AvatarRevSeen takes the rev an in line names for its author's avatar: when it differs from the
// stored one, the stored avatar is out of date and dropped, keeping the person's row with an
// empty rev, so the avatar line with the new rev is taken and the person still counts as one the
// bridge keeps an avatar for. It reports whether it dropped one.
func (b *Bridges) AvatarRevSeen(ctx context.Context, name, author, rev string) (bool, error) {
	author = clean(author, maxName)
	dropped := false
	err := store.InTx(ctx, b.db, func(tx *sql.Tx) error {
		var stored string
		var old sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT rev, avatar FROM outside_avatars WHERE room = ? AND author = ?`, name, author).Scan(&stored, &old)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && (stored == rev || (stored == "" && !old.Valid))) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE outside_avatars SET rev = '', avatar = NULL, updated_at = ? WHERE room = ? AND author = ?`,
			b.now().UnixMilli(), name, author); err != nil {
			return err
		}
		dropped = true
		return avatars.DropUnusedTx(ctx, tx, old.String)
	})
	return dropped, err
}
