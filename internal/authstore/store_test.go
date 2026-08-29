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
