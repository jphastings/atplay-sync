package db

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestOpen_RecordsAppliedMigrationsAndIsIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	if _, err := Open(dbPath); err != nil {
		t.Fatalf("first Open: %v", err)
	}
	conn, err := Open(dbPath) // second Open must not error re-applying anything
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer conn.Close()

	rows, err := conn.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, v)
	}
	want := []string{"0001_init.sql", "0002_generalize_sources.sql"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("schema_migrations = %v, want %v", got, want)
	}
}

func TestOpen_MigratesExistingSteamClaimsAndSyncPrefs(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	// Simulate a database that only ever had 0001_init.sql applied
	// (pre-migration-runner), with real user data in the old Steam-only shape.
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	schema, err := os.ReadFile("migrations/0001_init.sql")
	if err != nil {
		t.Fatalf("read 0001: %v", err)
	}
	if _, err := raw.Exec(string(schema)); err != nil {
		t.Fatalf("apply 0001: %v", err)
	}
	seed := []string{
		`INSERT INTO users (did, created_at) VALUES ('did:plc:old', '2026-01-01T00:00:00Z')`,
		`INSERT INTO steam_claims (did, subject, display_name, claim_uri, record_uri, last_verified_at)
		 VALUES ('did:plc:old', '765', 'Old Player', 'https://x', 'at://x', '2026-01-01T00:00:00Z')`,
		`INSERT INTO sync_prefs (did, steam_enabled) VALUES ('did:plc:old', 1)`,
	}
	for _, stmt := range seed {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}
	raw.Close()

	conn, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()

	var subject, displayName string
	err = conn.QueryRow(`SELECT subject, display_name FROM claims WHERE did = ? AND claim_type = 'steam'`, "did:plc:old").
		Scan(&subject, &displayName)
	if err != nil {
		t.Fatalf("query migrated claim: %v", err)
	}
	if subject != "765" || displayName != "Old Player" {
		t.Fatalf("got subject=%q displayName=%q, want 765/Old Player", subject, displayName)
	}

	var enabled, priority int
	err = conn.QueryRow(`SELECT enabled, priority FROM sync_prefs WHERE did = ? AND source = 'steam'`, "did:plc:old").
		Scan(&enabled, &priority)
	if err != nil {
		t.Fatalf("query migrated sync_prefs: %v", err)
	}
	if enabled != 1 {
		t.Fatalf("got enabled=%d, want 1", enabled)
	}

	for _, table := range []string{"steam_claims_old", "sync_prefs_old", "steam_claims"} {
		var name string
		err := conn.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if err != sql.ErrNoRows {
			t.Fatalf("table %s: got err=%v, want sql.ErrNoRows (should be gone)", table, err)
		}
	}
	_ = context.Background
}
