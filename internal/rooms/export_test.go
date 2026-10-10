package rooms

import (
	"context"
	"database/sql"

	"github.com/vsem-azamat/agora/internal/store"
)

// MarkRead moves agent's reading positions past msgs, for tests.
func MarkRead(ctx context.Context, r *Rooms, agent string, msgs []Message) error {
	return store.InTx(ctx, r.db, func(tx *sql.Tx) error { return markRead(ctx, tx, agent, msgs) })
}
