package cartridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	appdb "github.com/jphastings/game-status/internal/db"
)

func TestGetGameBySteamID_SendsClientKeyAndSteamIDCachesResult(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if got := r.Header.Get("x-client-key"); got != "test-key" {
			t.Errorf("x-client-key = %q, want test-key", got)
		}
		if got := r.URL.Query().Get("steamId"); got != "271590" {
			t.Errorf("steamId = %q, want 271590", got)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"game": map[string]any{
				"uri":  "at://did:plc:cartridge/games.gamesgamesgamesgames.game/gta5",
				"name": "Grand Theft Auto V", "summary": "An open world game.", "slug": "grand-theft-auto-v",
			},
		})
	}))
	defer server.Close()

	conn, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()

	c := New(server.URL, "test-key", conn)

	got, err := c.GetGameBySteamID(context.Background(), "271590")
	if err != nil {
		t.Fatalf("GetGameBySteamID: %v", err)
	}
	if got == nil || got.Name != "Grand Theft Auto V" || got.PageURL != "https://cartridge.dev/game/grand-theft-auto-v" {
		t.Fatalf("got %+v", got)
	}

	if _, err := c.GetGameBySteamID(context.Background(), "271590"); err != nil {
		t.Fatalf("second GetGameBySteamID: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (second call should hit the cache)", calls)
	}
}

func TestGetGameBySteamID_UnresolvedReturnsNilNil(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"game": map[string]any{}})
	}))
	defer server.Close()

	conn, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()

	got, err := New(server.URL, "test-key", conn).GetGameBySteamID(context.Background(), "999999")
	if err != nil {
		t.Fatalf("GetGameBySteamID: %v", err)
	}
	if got != nil {
		t.Fatalf("got %+v, want nil for an unresolvable game", got)
	}
}
