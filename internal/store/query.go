package store

import (
	"context"
	"database/sql"
	"errors"
)

// ErrInvalid marks a request with an invalid value; every domain package wraps it with what was
// wrong.
var ErrInvalid = errors.New("invalid request")

// InTx runs fn in a transaction on db and commits it, or rolls it back and returns fn's error.
// A panic in fn rolls the transaction back too, so the single connection is not left in it.
func InTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // after Commit a no-op; report the error that failed the transaction
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// Querier runs queries: a database or a transaction.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Strings runs a query that selects one text column and returns its values. The rows are
// closed before it returns, so the single database connection is free for the next statement.
func Strings(ctx context.Context, q Querier, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
