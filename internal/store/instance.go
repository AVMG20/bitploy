package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

// InstanceID returns the identity of this installation, creating it on first
// use. It is stored in the database, so a restored backup keeps its identity
// (and with it its Docker resources), while a fresh database gets a new one.
func (s *Store) InstanceID(ctx context.Context) (string, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM instance LIMIT 1`).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id = hex.EncodeToString(b)
	// Two starters racing: the table has one row at most in practice (single process); re-read to be safe.
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO instance (id, created_at) SELECT ?, ? WHERE NOT EXISTS (SELECT 1 FROM instance)`, id, time.Now().Unix()); err != nil {
		return "", err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM instance LIMIT 1`).Scan(&id); err != nil {
		return "", err
	}
	return id, nil
}
