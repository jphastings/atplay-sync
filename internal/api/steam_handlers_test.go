// internal/api/steam_handlers_test.go
package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appdb "github.com/jphastings/game-status/internal/db"
)

type fakeDeleter struct {
	err   error
	calls []string
}

func (f *fakeDeleter) DeleteStatus(ctx context.Context, did string) error {
	f.calls = append(f.calls, did)
	return f.err
}

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
	appdb.UpsertClaim(ctx, conn, appdb.Claim{DID: "did:plc:a", Type: appdb.SteamSource, Subject: "765", ClaimURI: "x", RecordURI: "y", LastVerifiedAt: time.Now()})

	h := &SteamHandlers{Conn: conn}
	req := httptest.NewRequest(http.MethodPost, "/api/steam/enabled", strings.NewReader(`{"enabled":true}`))
	req = req.WithContext(context.WithValue(req.Context(), didContextKey, "did:plc:a"))
	rec := httptest.NewRecorder()

	h.SetEnabled(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	enabled, err := appdb.IsEnabled(ctx, conn, "did:plc:a", appdb.SteamSource)
	if err != nil || !enabled {
		t.Fatalf("IsEnabled = %v, %v, want true, nil", enabled, err)
	}
}

func TestSetEnabled_DisableDeletesStatusAndClearsSession(t *testing.T) {
	conn, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()
	ctx := context.Background()
	appdb.UpsertUser(ctx, conn, "did:plc:a")
	appdb.UpsertClaim(ctx, conn, appdb.Claim{DID: "did:plc:a", Type: appdb.SteamSource, Subject: "765", ClaimURI: "x", RecordURI: "y", LastVerifiedAt: time.Now()})
	appdb.SetEnabled(ctx, conn, "did:plc:a", appdb.SteamSource, true)
	appdb.SetSessionStart(ctx, conn, "did:plc:a", appdb.SteamSource, "271590", time.Now())

	deleter := &fakeDeleter{}
	h := &SteamHandlers{Conn: conn, Deleter: deleter}
	req := httptest.NewRequest(http.MethodPost, "/api/steam/enabled", strings.NewReader(`{"enabled":false}`))
	req = req.WithContext(context.WithValue(req.Context(), didContextKey, "did:plc:a"))
	rec := httptest.NewRecorder()

	h.SetEnabled(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if len(deleter.calls) != 1 || deleter.calls[0] != "did:plc:a" {
		t.Fatalf("DeleteStatus calls = %v, want one call for did:plc:a", deleter.calls)
	}
	if row, err := appdb.GetSessionStart(ctx, conn, "did:plc:a", appdb.SteamSource); err != nil || row != nil {
		t.Fatalf("GetSessionStart = %+v, %v, want nil, nil (cleared)", row, err)
	}
	if enabled, err := appdb.IsEnabled(ctx, conn, "did:plc:a", appdb.SteamSource); err != nil || enabled {
		t.Fatalf("IsEnabled = %v, %v, want false, nil", enabled, err)
	}
}

func TestSetEnabled_DisableFailsClosedWhenDeleteFails(t *testing.T) {
	conn, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()
	ctx := context.Background()
	appdb.UpsertUser(ctx, conn, "did:plc:a")
	appdb.UpsertClaim(ctx, conn, appdb.Claim{DID: "did:plc:a", Type: appdb.SteamSource, Subject: "765", ClaimURI: "x", RecordURI: "y", LastVerifiedAt: time.Now()})
	appdb.SetEnabled(ctx, conn, "did:plc:a", appdb.SteamSource, true)

	h := &SteamHandlers{Conn: conn, Deleter: &fakeDeleter{err: errors.New("pds unreachable")}}
	req := httptest.NewRequest(http.MethodPost, "/api/steam/enabled", strings.NewReader(`{"enabled":false}`))
	req = req.WithContext(context.WithValue(req.Context(), didContextKey, "did:plc:a"))
	rec := httptest.NewRecorder()

	h.SetEnabled(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	// A failed delete must not leave the pref flipped off with the record
	// still live — the toggle should read as still-enabled so the UI/user
	// can retry rather than silently drifting out of sync with the PDS.
	if enabled, err := appdb.IsEnabled(ctx, conn, "did:plc:a", appdb.SteamSource); err != nil || !enabled {
		t.Fatalf("IsEnabled = %v, %v, want true, nil (unchanged)", enabled, err)
	}
}
