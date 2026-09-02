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

type noopWriter struct{}

func (noopWriter) PutStatus(ctx context.Context, did, rkey string, s sync.ActorStatus) error { return nil }
func (noopWriter) DeleteStatus(ctx context.Context, did, rkey string) error                  { return nil }
func (noopWriter) ListStatuses(ctx context.Context, did string) ([]sync.StatusEntry, error) {
	return nil, nil
}

// The whole live-push path end to end: a browser holding an open socket
// must see a later Reconcile (a Steam tick, say) without reconnecting.
func TestLiveHandlers_PushesUpdatesToAnAlreadyOpenConnection(t *testing.T) {
	conn, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()
	ctx := context.Background()
	appdb.UpsertUser(ctx, conn, "did:plc:a")
	appdb.SetEnabled(ctx, conn, "did:plc:a", appdb.SteamSource, true)
	appdb.SetSourceOnline(ctx, conn, "did:plc:a", appdb.SteamSource, true)

	resolver := fakeGameResolver{games: map[string]*appdb.CachedGame{
		"570": {URI: "at://cartridge/games.gamesgamesgamesgames.game/dota2", Name: "Dota 2"},
	}}
	hub := livestate.NewHub()
	reconciler := &sync.Reconciler{Conn: conn, Resolver: resolver, Writer: noopWriter{}, Broadcaster: hub}
	h := &LiveHandlers{Conn: conn, Resolver: resolver, Hub: hub}

	mux := http.NewServeMux()
	mux.HandleFunc("/live", func(w http.ResponseWriter, r *http.Request) {
		h.Serve(w, r.WithContext(context.WithValue(r.Context(), didContextKey, "did:plc:a")))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsConn, _, err := gorilla.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/live", nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer wsConn.Close()

	var first []sync.SourceOutcome
	if err := wsConn.ReadJSON(&first); err != nil {
		t.Fatalf("ReadJSON (initial snapshot): %v", err)
	}
	if len(first) != 1 || first[0].Status != sync.OutcomeIdle {
		t.Fatalf("got initial %+v, want steam idle", first)
	}

	// A game starts, exactly as a Steam tick would record it.
	if err := sync.UpdateSession(ctx, conn, reconciler, "did:plc:a", appdb.SteamSource, true, "570", "Dota 2", sync.SessionExtra{}, time.Now()); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}

	wsConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var second []sync.SourceOutcome
	if err := wsConn.ReadJSON(&second); err != nil {
		t.Fatalf("ReadJSON (pushed update): %v — the open connection never received the update", err)
	}
	if len(second) != 1 || second[0].Status != sync.OutcomeSynced || second[0].GameName != "Dota 2" {
		t.Fatalf("got pushed %+v, want steam synced/Dota 2", second)
	}
}

// The heartbeat is what keeps an idle connection from being culled and what
// lets the page notice a socket that died without telling it, so it has to
// actually reach a real client — not just fire internally.
func TestLiveHandlers_HeartbeatReachesAnIdleConnection(t *testing.T) {
	conn, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()
	ctx := context.Background()
	appdb.UpsertUser(ctx, conn, "did:plc:a")
	appdb.SetEnabled(ctx, conn, "did:plc:a", appdb.SteamSource, true)

	hub := livestate.NewHub()
	heartbeatCtx, stop := context.WithCancel(ctx)
	defer stop()
	go hub.Heartbeat(heartbeatCtx, 50*time.Millisecond)

	h := &LiveHandlers{Conn: conn, Resolver: fakeGameResolver{}, Hub: hub}
	mux := http.NewServeMux()
	mux.HandleFunc("/live", func(w http.ResponseWriter, r *http.Request) {
		h.Serve(w, r.WithContext(context.WithValue(r.Context(), didContextKey, "did:plc:a")))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsConn, _, err := gorilla.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/live", nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer wsConn.Close()

	// Initial snapshot, then nothing happens at all — only heartbeats.
	var snapshot []sync.SourceOutcome
	if err := wsConn.ReadJSON(&snapshot); err != nil {
		t.Fatalf("ReadJSON (snapshot): %v", err)
	}
	wsConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var beat []sync.SourceOutcome
	if err := wsConn.ReadJSON(&beat); err != nil {
		t.Fatalf("ReadJSON (heartbeat): %v — an idle connection received no keepalive traffic", err)
	}
	if len(beat) != 1 || beat[0].Source != appdb.SteamSource {
		t.Fatalf("heartbeat sent %+v, want the did's current state", beat)
	}
}
