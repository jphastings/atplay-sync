package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
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

func TestListSteamEnabledDIDs_RequiresBothEnabledAndClaimed(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)

	for _, did := range []string{"did:plc:a", "did:plc:b", "did:plc:c"} {
		mustUpsertUser(t, conn, did)
	}
	mustSetSteamEnabled(t, conn, "did:plc:a", true) // enabled, no claim
	mustSetSteamEnabled(t, conn, "did:plc:b", true)
	mustUpsertSteamClaim(t, conn, "did:plc:b") // enabled AND claimed
	mustUpsertSteamClaim(t, conn, "did:plc:c") // claimed, not enabled

	dids, err := ListSteamEnabledDIDs(ctx, conn)
	if err != nil {
		t.Fatalf("ListSteamEnabledDIDs: %v", err)
	}
	if len(dids) != 1 || dids[0] != "did:plc:b" {
		t.Fatalf("got %v, want [did:plc:b]", dids)
	}
}

// Test helpers below are used across this task's test files.

func mustUpsertUser(t *testing.T, conn *sql.DB, did string) {
	t.Helper()
	if err := UpsertUser(context.Background(), conn, did); err != nil {
		t.Fatalf("UpsertUser(%s): %v", did, err)
	}
}

func mustSetSteamEnabled(t *testing.T, conn *sql.DB, did string, enabled bool) {
	t.Helper()
	if err := SetSteamEnabled(context.Background(), conn, did, enabled); err != nil {
		t.Fatalf("SetSteamEnabled(%s): %v", did, err)
	}
}

func mustUpsertSteamClaim(t *testing.T, conn *sql.DB, did string) {
	t.Helper()
	err := UpsertSteamClaim(context.Background(), conn, SteamClaim{
		DID: did, Subject: "76500000000000000", DisplayName: "Test",
		ClaimURI: "https://steamcommunity.com/profiles/76500000000000000",
		RecordURI: "at://" + did + "/dev.keytrace.claim/abc", LastVerifiedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("UpsertSteamClaim(%s): %v", did, err)
	}
}
