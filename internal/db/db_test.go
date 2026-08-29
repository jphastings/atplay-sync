package db

import (
	"path/filepath"
	"testing"
)

func TestOpen_AppliesSchema(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	conn, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()

	tables := []string{"users", "oauth_sessions", "oauth_auth_requests", "steam_claims", "sync_prefs", "session_starts", "game_cache", "keytrace_key_cache"}
	for _, table := range tables {
		var name string
		if err := conn.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name); err != nil {
			t.Errorf("table %s not found: %v", table, err)
		}
	}
}

func TestOpen_IdempotentAcrossRestarts(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	if _, err := Open(dbPath); err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if _, err := Open(dbPath); err != nil {
		t.Fatalf("second Open: %v", err)
	}
}
