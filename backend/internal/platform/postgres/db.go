// Package postgres holds the connection pool, migration runner, and
// repository implementations backing every domain-layer storage interface.
// This is the only package in Algebra that imports a SQL driver.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DB wraps a pgx connection pool. Repositories embed *DB and issue SQL
// directly — there is no ORM, per the mandate's "don't introduce
// abstractions beyond what the task requires."
type DB struct {
	Pool *pgxpool.Pool
}

// Connect opens a pool against connString (a postgres:// URL) and verifies
// connectivity with a ping.
func Connect(ctx context.Context, connString string) (*DB, error) {
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		return nil, fmt.Errorf("postgres: creating pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping failed: %w", err)
	}
	return &DB{Pool: pool}, nil
}

func (db *DB) Close() {
	db.Pool.Close()
}
