package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/bluesky-social/indigo/atproto/auth/oauth"
	"github.com/bluesky-social/indigo/atproto/syntax"

	appdb "github.com/jphastings/game-status/internal/db"
)

// failingSessionStore is an oauth.ClientAuthStore whose GetSession always
// errors, simulating a transient OAuth/session-resume failure (expired
// refresh token, unreachable PDS, etc).
type failingSessionStore struct{}

func (failingSessionStore) GetSession(ctx context.Context, did syntax.DID, sessionID string) (*oauth.ClientSessionData, error) {
	return nil, errors.New("session store unavailable")
}
func (failingSessionStore) SaveSession(ctx context.Context, sess oauth.ClientSessionData) error {
	return nil
}
func (failingSessionStore) DeleteSession(ctx context.Context, did syntax.DID, sessionID string) error {
	return nil
}
func (failingSessionStore) GetAuthRequestInfo(ctx context.Context, state string) (*oauth.AuthRequestData, error) {
	return nil, nil
}
func (failingSessionStore) SaveAuthRequestInfo(ctx context.Context, info oauth.AuthRequestData) error {
	return nil
}
func (failingSessionStore) DeleteAuthRequestInfo(ctx context.Context, state string) error {
	return nil
}

func TestMeHandler_ReturnsClaimAndPrefs(t *testing.T) {
	conn, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()

	ctx := context.Background()
	appdb.UpsertUser(ctx, conn, "did:plc:abc")
	appdb.UpsertSteamClaim(ctx, conn, appdb.SteamClaim{DID: "did:plc:abc", Subject: "765", DisplayName: "JP", ClaimURI: "x", RecordURI: "y", LastVerifiedAt: time.Now()})
	appdb.SetSteamEnabled(ctx, conn, "did:plc:abc", true)

	h := &MeHandler{Conn: conn}
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req = req.WithContext(context.WithValue(req.Context(), didContextKey, "did:plc:abc"))
	rec := httptest.NewRecorder()

	h.Get(rec, req)

	var got meResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.DID != "did:plc:abc" || got.SteamSubject == nil || *got.SteamSubject != "765" || !got.SteamEnabled {
		t.Fatalf("got %+v", got)
	}
}

func TestMeHandler_ResumeSessionFailureDegradesGracefully(t *testing.T) {
	conn, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()

	ctx := context.Background()
	appdb.UpsertUser(ctx, conn, "did:plc:abc")
	appdb.SetActiveSession(ctx, conn, "did:plc:abc", "sess-1")
	appdb.UpsertSteamClaim(ctx, conn, appdb.SteamClaim{DID: "did:plc:abc", Subject: "765", DisplayName: "JP", ClaimURI: "x", RecordURI: "y", LastVerifiedAt: time.Now()})
	appdb.SetSteamEnabled(ctx, conn, "did:plc:abc", true)

	h := &MeHandler{Conn: conn, App: &oauth.ClientApp{Store: failingSessionStore{}}}
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req = req.WithContext(context.WithValue(req.Context(), didContextKey, "did:plc:abc"))
	rec := httptest.NewRecorder()

	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (a session-resume failure must not fail the whole response)", rec.Code)
	}
	var got meResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Live != nil {
		t.Fatalf("got Live = %+v, want nil", got.Live)
	}
	if got.DID != "did:plc:abc" || got.SteamSubject == nil || !got.SteamEnabled {
		t.Fatalf("unrelated fields lost on session-resume failure: %+v", got)
	}
}
