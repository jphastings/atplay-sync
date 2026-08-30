// internal/api/discord_handlers_test.go
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

func TestDiscordSetEnabled_RejectsEnableWithoutValidClaim(t *testing.T) {
	conn, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()
	appdb.UpsertUser(context.Background(), conn, "did:plc:a")

	h := &DiscordHandlers{Conn: conn}
	req := httptest.NewRequest(http.MethodPost, "/api/discord/enabled", strings.NewReader(`{"enabled":true}`))
	req = req.WithContext(context.WithValue(req.Context(), didContextKey, "did:plc:a"))
	rec := httptest.NewRecorder()

	h.SetEnabled(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestDiscordSetEnabled_AllowsEnableWithValidClaim(t *testing.T) {
	conn, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()
	ctx := context.Background()
	appdb.UpsertUser(ctx, conn, "did:plc:a")
	appdb.UpsertClaim(ctx, conn, appdb.Claim{DID: "did:plc:a", Type: appdb.DiscordSource, Subject: "690973862245957683", ClaimURI: "x", RecordURI: "y", LastVerifiedAt: time.Now()})

	h := &DiscordHandlers{Conn: conn, Reconciler: &fakeReconciler{}}
	req := httptest.NewRequest(http.MethodPost, "/api/discord/enabled", strings.NewReader(`{"enabled":true}`))
	req = req.WithContext(context.WithValue(req.Context(), didContextKey, "did:plc:a"))
	rec := httptest.NewRecorder()

	h.SetEnabled(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	enabled, err := appdb.IsEnabled(ctx, conn, "did:plc:a", appdb.DiscordSource)
	if err != nil || !enabled {
		t.Fatalf("IsEnabled = %v, %v, want true, nil", enabled, err)
	}
}

func TestDiscordSetEnabled_DisableReconcilesAndClearsSession(t *testing.T) {
	conn, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()
	ctx := context.Background()
	appdb.UpsertUser(ctx, conn, "did:plc:a")
	appdb.UpsertClaim(ctx, conn, appdb.Claim{DID: "did:plc:a", Type: appdb.DiscordSource, Subject: "690973862245957683", ClaimURI: "x", RecordURI: "y", LastVerifiedAt: time.Now()})
	appdb.SetEnabled(ctx, conn, "did:plc:a", appdb.DiscordSource, true)
	appdb.SetSessionStart(ctx, conn, "did:plc:a", appdb.DiscordSource, "271590", time.Now())

	reconciler := &fakeReconciler{}
	h := &DiscordHandlers{Conn: conn, Reconciler: reconciler}
	req := httptest.NewRequest(http.MethodPost, "/api/discord/enabled", strings.NewReader(`{"enabled":false}`))
	req = req.WithContext(context.WithValue(req.Context(), didContextKey, "did:plc:a"))
	rec := httptest.NewRecorder()

	h.SetEnabled(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if len(reconciler.calls) != 1 || reconciler.calls[0] != "did:plc:a" {
		t.Fatalf("Reconcile calls = %v, want one call for did:plc:a", reconciler.calls)
	}
	if row, err := appdb.GetSessionStart(ctx, conn, "did:plc:a", appdb.DiscordSource); err != nil || row != nil {
		t.Fatalf("GetSessionStart = %+v, %v, want nil, nil (cleared)", row, err)
	}
	if enabled, err := appdb.IsEnabled(ctx, conn, "did:plc:a", appdb.DiscordSource); err != nil || enabled {
		t.Fatalf("IsEnabled = %v, %v, want false, nil", enabled, err)
	}
}

func TestDiscordSetEnabled_DisableFailsClosedWhenReconcileFails(t *testing.T) {
	conn, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()
	ctx := context.Background()
	appdb.UpsertUser(ctx, conn, "did:plc:a")
	appdb.UpsertClaim(ctx, conn, appdb.Claim{DID: "did:plc:a", Type: appdb.DiscordSource, Subject: "690973862245957683", ClaimURI: "x", RecordURI: "y", LastVerifiedAt: time.Now()})
	appdb.SetEnabled(ctx, conn, "did:plc:a", appdb.DiscordSource, true)

	h := &DiscordHandlers{Conn: conn, Reconciler: &fakeReconciler{err: errors.New("pds unreachable")}}
	req := httptest.NewRequest(http.MethodPost, "/api/discord/enabled", strings.NewReader(`{"enabled":false}`))
	req = req.WithContext(context.WithValue(req.Context(), didContextKey, "did:plc:a"))
	rec := httptest.NewRecorder()

	h.SetEnabled(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	// A failed reconcile must not leave the pref flipped off with the record
	// still live — the toggle should read as still-enabled so the UI/user
	// can retry rather than silently drifting out of sync with the PDS.
	if enabled, err := appdb.IsEnabled(ctx, conn, "did:plc:a", appdb.DiscordSource); err != nil || !enabled {
		t.Fatalf("IsEnabled = %v, %v, want true, nil (unchanged)", enabled, err)
	}
}
