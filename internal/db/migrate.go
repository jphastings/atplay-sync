package db

import (
	"database/sql"
	"fmt"
	"io/fs"
	"sort"
)

// applyMigrations runs every migrations/*.sql file not yet recorded in
// schema_migrations, in filename order, each in its own transaction.
// 0001_init.sql's CREATE TABLE IF NOT EXISTS statements are safe to
// re-apply against a database that predates this runner — recording it
// afterward just bootstraps schema_migrations for such a database.
func applyMigrations(conn *sql.DB, migrations fs.FS) error {
	if _, err := conn.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		var applied string
		err := conn.QueryRow(`SELECT version FROM schema_migrations WHERE version = ?`, name).Scan(&applied)
		if err == nil {
			continue // already applied
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("check migration %s: %w", name, err)
		}

		sqlBytes, err := fs.ReadFile(migrations, "migrations/"+name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		tx, err := conn.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", name, err)
		}
		if _, err := tx.Exec(string(sqlBytes)); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, name); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}
	return nil
}
