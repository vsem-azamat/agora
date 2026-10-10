package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"testing"
)

func TestInTxCommitsOrRollsBack(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `CREATE TABLE t (s TEXT, n INTEGER)`); err != nil {
		t.Fatal(err)
	}
	insert := func(s string, n int) func(*sql.Tx) error {
		return func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO t (s, n) VALUES (?, ?)`, s, n)
			return err
		}
	}
	if err := InTx(ctx, db, insert("kept", 1)); err != nil {
		t.Fatal(err)
	}
	failed := errors.New("failed")
	err = InTx(ctx, db, func(tx *sql.Tx) error {
		if err := insert("dropped", 2)(tx); err != nil {
			return err
		}
		return failed
	})
	if !errors.Is(err, failed) {
		t.Fatalf("err = %v", err)
	}
	if got, err := Strings(ctx, db, `SELECT s FROM t ORDER BY s`); err != nil || !slices.Equal(got, []string{"kept"}) {
		t.Fatalf("strings %v %v", got, err)
	}
}
