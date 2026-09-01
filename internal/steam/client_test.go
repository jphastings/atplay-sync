package steam

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestGetPlayerSummaries_BatchesRequestsOfAtMost100(t *testing.T) {
	var batchSizes []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ids := strings.Split(r.URL.Query().Get("steamids"), ",")
		batchSizes = append(batchSizes, len(ids))
		players := make([]map[string]any, len(ids))
		for i, id := range ids {
			players[i] = map[string]any{"steamid": id}
		}
		json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"players": players}})
	}))
	defer server.Close()

	steamIDs := make([]string, 201)
	for i := range steamIDs {
		steamIDs[i] = strconv.Itoa(i)
	}

	c := &Client{APIKey: "key", HTTPClient: http.DefaultClient, BaseURL: server.URL}
	got, err := c.GetPlayerSummaries(context.Background(), steamIDs)
	if err != nil {
		t.Fatalf("GetPlayerSummaries: %v", err)
	}
	if len(got) != 201 {
		t.Fatalf("got %d players, want 201", len(got))
	}
	if want := []int{100, 100, 1}; !equalInts(batchSizes, want) {
		t.Fatalf("batch sizes = %v, want %v", batchSizes, want)
	}
}

func TestGetPlayerSummaries_ParsesCurrentGame(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"players": []map[string]any{
			{"steamid": "76500000000000000", "gameid": "271590", "gameextrainfo": "Grand Theft Auto V"},
			{"steamid": "76500000000000001"}, // not currently playing
		}}})
	}))
	defer server.Close()

	c := &Client{APIKey: "key", HTTPClient: http.DefaultClient, BaseURL: server.URL}
	got, err := c.GetPlayerSummaries(context.Background(), []string{"76500000000000000", "76500000000000001"})
	if err != nil {
		t.Fatalf("GetPlayerSummaries: %v", err)
	}
	if got["76500000000000000"].GameID != "271590" {
		t.Fatalf("got %+v, want gameid 271590", got["76500000000000000"])
	}
	if got["76500000000000001"].GameID != "" {
		t.Fatalf("got %+v, want empty gameid", got["76500000000000001"])
	}
}

func TestGetPlayerSummaries_ReturnsErrorOnNonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{"error": "invalid API key"})
	}))
	defer server.Close()

	c := &Client{APIKey: "key", HTTPClient: http.DefaultClient, BaseURL: server.URL}
	got, err := c.GetPlayerSummaries(context.Background(), []string{"76500000000000000"})
	if err == nil {
		t.Fatalf("GetPlayerSummaries: expected error, got nil (got=%+v)", got)
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("error = %q, want it to mention status code 401", err.Error())
	}
}

func TestGetPlayerSummaries_429ReturnsErrRateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	c := &Client{APIKey: "key", HTTPClient: http.DefaultClient, BaseURL: server.URL}
	_, err := c.GetPlayerSummaries(context.Background(), []string{"76500000000000000"})
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestGetPlayerSummaries_ParsesPersonaState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"players": []map[string]any{
			{"steamid": "76500000000000000", "personastate": 3}, // away, no game
			{"steamid": "76500000000000001", "personastate": 0}, // offline
		}}})
	}))
	defer server.Close()

	c := &Client{APIKey: "key", HTTPClient: http.DefaultClient, BaseURL: server.URL}
	got, err := c.GetPlayerSummaries(context.Background(), []string{"76500000000000000", "76500000000000001"})
	if err != nil {
		t.Fatalf("GetPlayerSummaries: %v", err)
	}
	// Anything but 0 counts as online — away/busy/snooze all mean the
	// account is connected to Steam, just not at the keyboard.
	if !got["76500000000000000"].Online {
		t.Fatalf("got %+v, want away (personastate 3) to count as online", got["76500000000000000"])
	}
	if got["76500000000000001"].Online {
		t.Fatalf("got %+v, want personastate 0 to be offline", got["76500000000000001"])
	}
}
