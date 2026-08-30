package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	appdb "github.com/jphastings/game-status/internal/db"
)

func TestMeHandler_ReturnsClaimAndPrefs(t *testing.T) {
	conn, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()

	ctx := context.Background()
	appdb.UpsertUser(ctx, conn, "did:plc:abc")
	appdb.UpsertClaim(ctx, conn, appdb.Claim{DID: "did:plc:abc", Type: appdb.SteamSource, Subject: "765", DisplayName: "JP", ClaimURI: "x", RecordURI: "y"})
	appdb.SetEnabled(ctx, conn, "did:plc:abc", appdb.SteamSource, true)

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
