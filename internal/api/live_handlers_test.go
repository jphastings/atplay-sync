// internal/api/live_handlers_test.go
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gorilla "github.com/gorilla/websocket"

	appdb "github.com/jphastings/game-status/internal/db"
	"github.com/jphastings/game-status/internal/livestate"
	"github.com/jphastings/game-status/internal/sync"
)

type fakeGameResolver struct{ games map[string]*appdb.CachedGame }

func (f fakeGameResolver) GetGameBySteamID(ctx context.Context, appID string) (*appdb.CachedGame, error) {
	return f.games[appID], nil
}

func TestLiveHandlers_Serve_SendsInitialSnapshot(t *testing.T) {
	conn, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()
	ctx := context.Background()
	appdb.UpsertUser(ctx, conn, "did:plc:a")
	appdb.SetEnabled(ctx, conn, "did:plc:a", appdb.SteamSource, true)
	appdb.SetSessionStart(ctx, conn, "did:plc:a", appdb.SteamSource, "570", time.Now())

	resolver := fakeGameResolver{games: map[string]*appdb.CachedGame{
		"570": {URI: "at://cartridge/games.gamesgamesgamesgames.game/dota2", Name: "Dota 2"},
	}}
	h := &LiveHandlers{Conn: conn, Resolver: resolver, Hub: livestate.NewHub()}

	mux := http.NewServeMux()
	mux.HandleFunc("/live", func(w http.ResponseWriter, r *http.Request) {
		h.Serve(w, r.WithContext(context.WithValue(r.Context(), didContextKey, "did:plc:a")))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/live"
	wsConn, _, err := gorilla.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer wsConn.Close()

	var outcomes []sync.SourceOutcome
	if err := wsConn.ReadJSON(&outcomes); err != nil {
		t.Fatalf("ReadJSON: %v", err)
	}
	if len(outcomes) != 1 || outcomes[0] != (sync.SourceOutcome{Source: appdb.SteamSource, Status: sync.OutcomeSynced, GameName: "Dota 2"}) {
		t.Fatalf("got %+v, want a single synced Dota 2 outcome", outcomes)
	}
}

func TestLiveHandlers_Serve_Unauthenticated_Returns401WithoutUpgrading(t *testing.T) {
	conn, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()

	h := &LiveHandlers{Conn: conn, Resolver: fakeGameResolver{}, Hub: livestate.NewHub()}
	srv := httptest.NewServer(http.HandlerFunc(h.Serve)) // no did injected into context — same as a request that never passed RequireAuth
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}
