// Package webtoken keeps the web app's access token in the hub database.
package webtoken

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"time"

	"github.com/vsem-azamat/agora/internal/store"
)

// tokenBytes is how many random bytes a token has.
const tokenBytes = 32

// Tokens keeps the web token.
type Tokens struct {
	db  *sql.DB
	now func() time.Time
}

// New returns Tokens over db; now is the clock (time.Now when nil).
func New(db *sql.DB, now func() time.Time) *Tokens {
	if now == nil {
		now = time.Now
	}
	return &Tokens{db: db, now: now}
}

// Get returns the stored token, or "" when there is none.
func (t *Tokens) Get(ctx context.Context) (string, error) {
	var token string
	err := t.db.QueryRowContext(ctx, `SELECT token FROM web_token WHERE id = 1`).Scan(&token)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return token, err
}

// Ensure returns the token, creating one when there is none; with rotate it replaces it.
func (t *Tokens) Ensure(ctx context.Context, rotate bool) (string, error) {
	var token string
	err := store.InTx(ctx, t.db, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT token FROM web_token WHERE id = 1`).Scan(&token)
		if err == nil && !rotate {
			return nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		b := make([]byte, tokenBytes)
		rand.Read(b) // never fails; see crypto/rand
		token = base64.RawURLEncoding.EncodeToString(b)
		_, err = tx.ExecContext(ctx, `INSERT INTO web_token (id, token, created_at) VALUES (1, ?, ?)
			ON CONFLICT (id) DO UPDATE SET token = excluded.token, created_at = excluded.created_at`, token, t.now().UnixMilli())
		return err
	})
	return token, err
}
