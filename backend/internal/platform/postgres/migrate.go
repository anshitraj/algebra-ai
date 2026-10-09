package postgres

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

const schemaMigrationsDDL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version     TEXT PRIMARY KEY,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);`

// Migrate applies every *.sql file in dir, in filename order, that isn't
// already recorded in schema_migrations. Each file runs inside its own
// transaction — a failing migration rolls back cleanly rather than leaving
// the schema half-applied. This is a deliberately small hand-rolled runner
// (the mandate favors avoiding dependencies that aren't earning their
// keep); it does not support down-migrations, which Algebra's schema
// changes are not expected to need in normal operation.
func (db *DB) Migrate(ctx context.Context, dir string) error {
	if _, err := db.Pool.Exec(ctx, schemaMigrationsDDL); err != nil {
		return fmt.Errorf("postgres: creating schema_migrations table: %w", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("postgres: reading migrations dir %q: %w", dir, err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".sql" {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	applied := map[string]bool{}
	rows, err := db.Pool.Query(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return fmt.Errorf("postgres: listing applied migrations: %w", err)
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return fmt.Errorf("postgres: scanning applied migration: %w", err)
		}
		applied[v] = true
	}
	rows.Close()

	for _, name := range files {
		if applied[name] {
			continue
		}
		sqlBytes, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("postgres: reading migration %q: %w", name, err)
		}

		tx, err := db.Pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("postgres: beginning transaction for %q: %w", name, err)
		}
		if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("postgres: applying migration %q: %w", name, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", name); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("postgres: recording migration %q: %w", name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("postgres: committing migration %q: %w", name, err)
		}
	}
	return nil
}
