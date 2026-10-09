package sessions

import (
	"context"
	"database/sql"
	"testing"
)

// RaceNoteCheck makes the next note check lose its compare-and-set, as when a concurrent hook
// of the same session records its check between the read and the write.
func RaceNoteCheck(t *testing.T) {
	t.Helper()
	beforeNoteCheck = func(ctx context.Context, tx *sql.Tx, id string) error {
		beforeNoteCheck = nil
		_, err := tx.ExecContext(ctx, `UPDATE sessions SET checked_at = checked_at + 1 WHERE id = ?`, id)
		return err
	}
	t.Cleanup(func() { beforeNoteCheck = nil })
}
