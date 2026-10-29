// Package store owns the SQLite database: opening it, migrations, the
// sqlc-generated queries in store/db, and encryption of secrets at rest.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/AVMG20/bitploy/internal/store/db"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store bundles the connection and the typed queries.
type Store struct {
	DB *sql.DB
	Q  *db.Queries
}

// Open opens (or creates) the database in WAL mode with foreign keys on.
func Open(path string) (*Store, error) {
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=busy_timeout(10000)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_txlock=immediate"
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One writer at a time keeps SQLite free of SQLITE_BUSY under load;
	// WAL still lets readers proceed while it writes.
	conn.SetMaxOpenConns(8)
	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("open database %s: %w", path, err)
	}
	return &Store{DB: conn, Q: db.New(conn)}, nil
}

// Migrate applies all embedded migrations.
func (s *Store) Migrate(ctx context.Context) error {
	goose.SetBaseFS(migrations)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		return err
	}
	return goose.UpContext(ctx, s.DB, "migrations")
}

// Tx runs fn in a transaction with typed queries bound to it.
func (s *Store) Tx(ctx context.Context, fn func(q *db.Queries) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(s.Q.WithTx(tx)); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *Store) Close() error { return s.DB.Close() }
