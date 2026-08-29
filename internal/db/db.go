package db

import (
	"database/sql"
	"embed"
	"fmt"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Open applies WAL + a busy timeout on every pooled connection: four
// independent goroutines write here (HTTP handlers, the sync ticker, the
// Jetstream handler and the daily sweep) and the defaults would fail any
// overlap with an immediate SQLITE_BUSY.
func Open(path string) (*sql.DB, error) {
	conn, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	schema, err := migrationsFS.ReadFile("migrations/0001_init.sql")
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(string(schema)); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return conn, nil
}
