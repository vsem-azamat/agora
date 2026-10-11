package avatars

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/vsem-azamat/agora/internal/agents"
	"github.com/vsem-azamat/agora/internal/store"
)

// ErrNotFound means no avatar is stored under the hash.
var ErrNotFound = errors.New("avatar not found")

// Avatars keeps avatars in the hub database.
type Avatars struct {
	db  *sql.DB
	now func() time.Time
}

// New returns Avatars over db; now is the clock (time.Now when nil).
func New(db *sql.DB, now func() time.Time) *Avatars {
	if now == nil {
		now = time.Now
	}
	return &Avatars{db: db, now: now}
}

// PutTx stores p, unless a picture with its hash is stored already.
func PutTx(ctx context.Context, tx *sql.Tx, p Picture, now time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO avatars (hash, mime, data, small, w, h, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (hash) DO NOTHING`, p.Hash, p.MIME, p.Data, p.Small, p.W, p.H, now.UnixMilli())
	return err
}

// DropUnusedTx deletes the avatar hash when no agent and no person outside uses it.
func DropUnusedTx(ctx context.Context, tx *sql.Tx, hash string) error {
	if hash == "" {
		return nil
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM avatars WHERE hash = :h
		AND NOT EXISTS (SELECT 1 FROM agents WHERE avatar = :h)
		AND NOT EXISTS (SELECT 1 FROM outside_avatars WHERE avatar = :h)`, sql.Named("h", hash))
	return err
}

// SetAgent makes the image data, of the declared media type (may be empty), agent's avatar.
func (a *Avatars) SetAgent(ctx context.Context, agent string, data []byte, declared string) (Picture, error) {
	p, err := Prepare(data, declared)
	if err != nil {
		return Picture{}, err
	}
	return p, a.setAgent(ctx, agent, &p)
}

// ClearAgent leaves agent without an avatar.
func (a *Avatars) ClearAgent(ctx context.Context, agent string) error {
	return a.setAgent(ctx, agent, nil)
}

func (a *Avatars) setAgent(ctx context.Context, agent string, p *Picture) error {
	return store.InTx(ctx, a.db, func(tx *sql.Tx) error {
		hash := ""
		if p != nil {
			if err := PutTx(ctx, tx, *p, a.now()); err != nil {
				return err
			}
			hash = p.Hash
		}
		old, err := agents.SetAvatarTx(ctx, tx, agent, hash)
		if err != nil {
			return err
		}
		return DropUnusedTx(ctx, tx, old)
	})
}

// Get returns the media type and the picture stored under hash: the smaller one when small is
// set and there is one, else the larger one.
func (a *Avatars) Get(ctx context.Context, hash string, small bool) (string, []byte, error) {
	var mime string
	var data, smaller []byte
	err := a.db.QueryRowContext(ctx, `SELECT mime, data, small FROM avatars WHERE hash = ?`, hash).Scan(&mime, &data, &smaller)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, store.Refuse(ErrNotFound, "no avatar %s", hash)
	}
	if err != nil {
		return "", nil, err
	}
	if small && smaller != nil {
		return mime, smaller, nil
	}
	return mime, data, nil
}
