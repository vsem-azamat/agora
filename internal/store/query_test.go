package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"
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

func TestInTxRollsBackOnPanic(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	func() {
		defer func() { _ = recover() }()
		_ = InTx(ctx, db, func(*sql.Tx) error { panic("boom") })
	}()
	done := make(chan error, 1)
	go func() { done <- InTx(ctx, db, func(*sql.Tx) error { return nil }) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the panicking transaction still holds the connection")
	}
}

func TestStringsFreesTheConnectionForTheNextStatement(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	err = InTx(ctx, db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `CREATE TABLE t (s TEXT)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO t (s) VALUES ('a'), ('b')`); err != nil {
			return err
		}
		got, err := Strings(ctx, tx, `SELECT s FROM t ORDER BY s`)
		if err != nil || !slices.Equal(got, []string{"a", "b"}) {
			return fmt.Errorf("strings %v %w", got, err)
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM t WHERE s = ?`, got[0]) // the rows are closed, so this runs
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
