package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestUpsertUser_CreatesThenLeavesExisting(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)

	if err := UpsertUser(ctx, conn, "did:plc:abc"); err != nil {
		t.Fatalf("first UpsertUser: %v", err)
	}
	if err := SetActiveSession(ctx, conn, "did:plc:abc", "sess-1"); err != nil {
		t.Fatalf("SetActiveSession: %v", err)
	}
	if err := UpsertUser(ctx, conn, "did:plc:abc"); err != nil { // re-login shouldn't clobber the session
		t.Fatalf("second UpsertUser: %v", err)
	}

	u, err := GetUser(ctx, conn, "did:plc:abc")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if u == nil || u.ActiveSessionID != "sess-1" {
		t.Fatalf("got %+v, want ActiveSessionID=sess-1", u)
	}
}

func TestGetUser_MissingReturnsNil(t *testing.T) {
	conn := openTestDB(t)
	u, err := GetUser(context.Background(), conn, "did:plc:nobody")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if u != nil {
		t.Fatalf("got %+v, want nil", u)
	}
}

// mustUpsertUser is used across this package's test files.
func mustUpsertUser(t *testing.T, conn *sql.DB, did string) {
	t.Helper()
	if err := UpsertUser(context.Background(), conn, did); err != nil {
		t.Fatalf("UpsertUser(%s): %v", did, err)
	}
}
