// internal/api/sync_handlers_test.go
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	appdb "github.com/jphastings/game-status/internal/db"
)

func TestSetOrder_RejectsUnknownSource(t *testing.T) {
	conn, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()
	appdb.UpsertUser(context.Background(), conn, "did:plc:a")

	h := &SyncHandlers{Conn: conn}
	req := httptest.NewRequest(http.MethodPost, "/api/sync/order", strings.NewReader(`{"order":["steam","xbox"]}`))
	req = req.WithContext(context.WithValue(req.Context(), didContextKey, "did:plc:a"))
	rec := httptest.NewRecorder()

	h.SetOrder(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestSetOrder_PersistsValidOrder(t *testing.T) {
	conn, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()
	ctx := context.Background()
	appdb.UpsertUser(ctx, conn, "did:plc:a")
	appdb.SetEnabled(ctx, conn, "did:plc:a", appdb.SteamSource, true)
	appdb.SetEnabled(ctx, conn, "did:plc:a", appdb.DiscordSource, true)

	h := &SyncHandlers{Conn: conn}
	req := httptest.NewRequest(http.MethodPost, "/api/sync/order", strings.NewReader(`{"order":["discord","steam"]}`))
	req = req.WithContext(context.WithValue(req.Context(), didContextKey, "did:plc:a"))
	rec := httptest.NewRecorder()

	h.SetOrder(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	sources, err := appdb.ListEnabledSourcesByPriority(ctx, conn, "did:plc:a")
	if err != nil || len(sources) != 2 || sources[0] != "discord" || sources[1] != "steam" {
		t.Fatalf("got %v, %v, want [discord steam]", sources, err)
	}
}
