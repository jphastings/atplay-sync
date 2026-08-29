// internal/api/steam_handlers_test.go
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appdb "github.com/jphastings/game-status/internal/db"
)

func TestSetEnabled_RejectsEnableWithoutValidClaim(t *testing.T) {
	conn, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()
	appdb.UpsertUser(context.Background(), conn, "did:plc:a")

	h := &SteamHandlers{Conn: conn}
	req := httptest.NewRequest(http.MethodPost, "/api/steam/enabled", strings.NewReader(`{"enabled":true}`))
	req = req.WithContext(context.WithValue(req.Context(), didContextKey, "did:plc:a"))
	rec := httptest.NewRecorder()

	h.SetEnabled(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestSetEnabled_AllowsEnableWithValidClaim(t *testing.T) {
	conn, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()
	ctx := context.Background()
	appdb.UpsertUser(ctx, conn, "did:plc:a")
	appdb.UpsertSteamClaim(ctx, conn, appdb.SteamClaim{DID: "did:plc:a", Subject: "765", ClaimURI: "x", RecordURI: "y", LastVerifiedAt: time.Now()})

	h := &SteamHandlers{Conn: conn}
	req := httptest.NewRequest(http.MethodPost, "/api/steam/enabled", strings.NewReader(`{"enabled":true}`))
	req = req.WithContext(context.WithValue(req.Context(), didContextKey, "did:plc:a"))
	rec := httptest.NewRecorder()

	h.SetEnabled(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	enabled, err := appdb.IsSteamEnabled(ctx, conn, "did:plc:a")
	if err != nil || !enabled {
		t.Fatalf("IsSteamEnabled = %v, %v, want true, nil", enabled, err)
	}
}
