// Package store opens the hub's SQLite database and applies its schema migrations.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

//go:embed migrations/*.sql
var migrations embed.FS

// Open opens (creating if needed) the database at path and migrates it to the latest schema.
// The database file is readable and writable by its owner only; SQLite gives its WAL and
// shared-memory files the same mode. A database that records a migration this binary does not carry,
// or lacks one before a recorded one, or has a newer schema version, is refused. Use ":memory:" for a private in-memory database.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
		if err := ownerOnly(path); err != nil {
			return nil, err
		}
	}
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// One connection: SQLite has a single writer, and an in-memory database lives per connection.
	db.SetMaxOpenConns(1)
	if err := migrate(ctx, db, migrations); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// ownerOnly creates the database file with mode 0600 if it is missing, and restricts an
// existing one and its WAL and shared-memory files to that mode.
func ownerOnly(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("create database: %w", err)
	}
	f.Close()
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(p, 0o600); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("restrict database file: %w", err)
		}
	}
	return nil
}

// migrate brings the database to the migrations in fsys. The schema_migrations table records
// which ran, by name: the database is refused when it records one fsys does not carry, or
// lacks one that fsys carries before one it records (a fork); else the missing tail runs.
func migrate(ctx context.Context, db *sql.DB, fsys fs.FS) error {
	files, err := fs.Glob(fsys, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version > len(files) {
		return fmt.Errorf("the database has schema version %d, newer than this agora knows (%d); run a newer agora", version, len(files))
	}
	if err := ensureRecord(ctx, db, files, version); err != nil {
		return err
	}
	recorded, err := Strings(ctx, db, `SELECT name FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("read applied migrations: %w", err)
	}
	applied := make(map[string]bool, len(recorded))
	for _, n := range recorded {
		applied[n] = true
	}
	carried := make(map[string]bool, len(files))
	for _, f := range files {
		carried[path.Base(f)] = true
	}
	for _, n := range slices.Sorted(maps.Keys(applied)) {
		if !carried[n] {
			return fmt.Errorf("the database has applied migration %s, which this agora does not carry (written by a newer agora or another branch); run the agora that wrote it", n)
		}
	}
	missing := -1
	for i, f := range files {
		switch {
		case !applied[path.Base(f)] && missing < 0:
			missing = i
		case applied[path.Base(f)] && missing >= 0:
			return fmt.Errorf("the database lacks migration %s but has the later %s (written by another branch); it is not run out of order", path.Base(files[missing]), path.Base(f))
		}
	}
	if missing < 0 {
		return nil
	}
	for i := missing; i < len(files); i++ {
		body, err := fs.ReadFile(fsys, files[i])
		if err != nil {
			return err
		}
		if err := apply(ctx, db, files[i], string(body), i+1); err != nil {
			return err
		}
	}
	return nil
}

// ensureRecord creates schema_migrations. A database that had none was migrated by the rule
// that counted them, so the first version migrations of files are what ran.
func ensureRecord(ctx context.Context, db *sql.DB, files []string, version int) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`).Scan(&existing); err != nil {
		_ = tx.Rollback() // report the error that failed the read
		return fmt.Errorf("read applied migrations: %w", err)
	}
	if existing == 0 {
		if _, err := tx.ExecContext(ctx, `CREATE TABLE schema_migrations (name TEXT PRIMARY KEY)`); err != nil {
			_ = tx.Rollback() // report the error that failed the create
			return fmt.Errorf("create schema_migrations: %w", err)
		}
		for _, f := range files[:version] {
			if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (name) VALUES (?)`, path.Base(f)); err != nil {
				_ = tx.Rollback() // report the error that failed the insert
				return fmt.Errorf("record %s: %w", path.Base(f), err)
			}
		}
	}
	return tx.Commit()
}

// foreignKeysOff marks a migration that rebuilds a table other tables point at: it runs with
// foreign keys off, as SQLite prescribes for that, and they are checked before it commits.
const foreignKeysOff = "-- foreign keys: off\n"

// apply runs migration name and records it and version, in one transaction.
func apply(ctx context.Context, db *sql.DB, name, body string, version int) (err error) {
	keysOff := strings.HasPrefix(body, foreignKeysOff)
	if keysOff {
		// outside a transaction: inside one SQLite ignores it; the pool has one connection
		if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
			return err
		}
		defer func() {
			if _, on := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); on != nil && err == nil {
				err = fmt.Errorf("after %s: turn foreign keys on: %w", name, on)
			}
		}()
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, body); err != nil {
		_ = tx.Rollback() // report the error that failed the migration
		return fmt.Errorf("apply %s: %w", name, err)
	}
	if keysOff {
		tables, err := Strings(ctx, tx, `SELECT DISTINCT "table" || ' -> ' || parent FROM pragma_foreign_key_check ORDER BY 1`)
		if err != nil {
			_ = tx.Rollback() // report the error that failed the check
			return fmt.Errorf("apply %s: check foreign keys: %w", name, err)
		}
		if len(tables) > 0 {
			_ = tx.Rollback() // report the violations
			return fmt.Errorf("apply %s: it leaves rows pointing at missing rows (%s); the database is unchanged", name, strings.Join(tables, ", "))
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (name) VALUES (?)`, path.Base(name)); err != nil {
		_ = tx.Rollback() // report the error that failed the record
		return fmt.Errorf("record %s: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		_ = tx.Rollback() // report the error that failed the migration
		return err
	}
	return tx.Commit()
}
