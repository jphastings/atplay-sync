package authstore

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/bluesky-social/indigo/atproto/auth/oauth"
	"github.com/bluesky-social/indigo/atproto/syntax"

	appdb "github.com/jphastings/game-status/internal/db"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestSQLiteStore_SessionRoundTrip(t *testing.T) {
	store := &SQLiteStore{Conn: openTestDB(t)}
	did := syntax.DID("did:plc:abc")

	in := oauth.ClientSessionData{AccountDID: did, SessionID: "sess-1", AccessToken: "at-1", RefreshToken: "rt-1"}
	if err := store.SaveSession(context.Background(), in); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	out, err := store.GetSession(context.Background(), did, "sess-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if out.AccessToken != "at-1" || out.RefreshToken != "rt-1" {
		t.Fatalf("got %+v, want AccessToken=at-1 RefreshToken=rt-1", out)
	}
}

// indigo re-saves the same (did, sessionID) on every inline token refresh, so the
// upsert — not the insert — is the hot path.
func TestSQLiteStore_SaveSessionOverwritesExisting(t *testing.T) {
	conn := openTestDB(t)
	store := &SQLiteStore{Conn: conn}
	did := syntax.DID("did:plc:abc")

	for _, token := range []string{"at-1", "at-2"} {
		in := oauth.ClientSessionData{AccountDID: did, SessionID: "sess-1", AccessToken: token, RefreshToken: "rt-1"}
		if err := store.SaveSession(context.Background(), in); err != nil {
			t.Fatalf("SaveSession(%s): %v", token, err)
		}
	}

	out, err := store.GetSession(context.Background(), did, "sess-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if out.AccessToken != "at-2" {
		t.Fatalf("got AccessToken=%s, want at-2", out.AccessToken)
	}

	var rows int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM oauth_sessions WHERE did = ? AND session_id = ?`, did.String(), "sess-1").Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 1 {
		t.Fatalf("got %d rows, want 1", rows)
	}
}

func TestWithStateCapture(t *testing.T) {
	store := &SQLiteStore{Conn: openTestDB(t)}
	ctx, state := WithStateCapture(context.Background())

	if err := store.SaveAuthRequestInfo(ctx, oauth.AuthRequestData{State: "state-1", PKCEVerifier: "verifier-1"}); err != nil {
		t.Fatalf("SaveAuthRequestInfo: %v", err)
	}
	if *state != "state-1" {
		t.Fatalf("got %q, want state-1", *state)
	}
}

func TestSQLiteStore_AuthRequestRoundTripAndDelete(t *testing.T) {
	store := &SQLiteStore{Conn: openTestDB(t)}

	in := oauth.AuthRequestData{State: "state-1", PKCEVerifier: "verifier-1"}
	if err := store.SaveAuthRequestInfo(context.Background(), in); err != nil {
		t.Fatalf("SaveAuthRequestInfo: %v", err)
	}

	out, err := store.GetAuthRequestInfo(context.Background(), "state-1")
	if err != nil {
		t.Fatalf("GetAuthRequestInfo: %v", err)
	}
	if out.PKCEVerifier != "verifier-1" {
		t.Fatalf("got %+v, want PKCEVerifier=verifier-1", out)
	}

	if err := store.DeleteAuthRequestInfo(context.Background(), "state-1"); err != nil {
		t.Fatalf("DeleteAuthRequestInfo: %v", err)
	}
	if _, err := store.GetAuthRequestInfo(context.Background(), "state-1"); err == nil {
		t.Fatal("expected error after delete, got nil")
	}
}

func TestSQLiteStore_DeleteStaleAuthRequestsKeepsLiveFlows(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	store := &SQLiteStore{Conn: conn}

	if err := store.SaveAuthRequestInfo(ctx, oauth.AuthRequestData{State: "live"}); err != nil {
		t.Fatalf("SaveAuthRequestInfo: %v", err)
	}
	_, err := conn.ExecContext(ctx, `INSERT INTO oauth_auth_requests (state, data, created_at) VALUES ('abandoned', '{}', datetime('now', '-1 hour'))`)
	if err != nil {
		t.Fatalf("insert abandoned request: %v", err)
	}

	if err := store.DeleteStaleAuthRequests(ctx); err != nil {
		t.Fatalf("DeleteStaleAuthRequests: %v", err)
	}

	if _, err := store.GetAuthRequestInfo(ctx, "abandoned"); err == nil {
		t.Fatal("expected the abandoned auth request to have been collected")
	}
	if _, err := store.GetAuthRequestInfo(ctx, "live"); err != nil {
		t.Fatalf("a just-started flow was collected: %v", err)
	}
}
