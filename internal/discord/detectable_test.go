package discord

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const fixtureJSON = `[
	{"id":"356875988589740042","name":"Dota 2","third_party_skus":[{"distributor":"steam","id":"570"}]},
	{"id":"1","name":"No Steam Release","third_party_skus":[{"distributor":"battlenet","id":null}]},
	{"id":"2","name":"No SKUs At All"}
]`

func TestRefresh_IndexesSteamSKUsOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(fixtureJSON))
	}))
	defer server.Close()

	idx := NewGameIndex()
	idx.detectableURL = server.URL
	if err := idx.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	appID, ok := idx.SteamAppID("356875988589740042")
	if !ok || appID != "570" {
		t.Fatalf("SteamAppID(dota2) = %q, %v, want 570, true", appID, ok)
	}
	if _, ok := idx.SteamAppID("1"); ok {
		t.Fatal("SteamAppID(no steam release) = true, want false")
	}
	if _, ok := idx.SteamAppID("2"); ok {
		t.Fatal("SteamAppID(no skus) = true, want false")
	}
	if _, ok := idx.SteamAppID("unknown"); ok {
		t.Fatal("SteamAppID(unknown application_id) = true, want false")
	}
}

func TestRefresh_UnexpectedStatus_ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	idx := NewGameIndex()
	idx.detectableURL = server.URL
	if err := idx.Refresh(context.Background()); err == nil {
		t.Fatal("Refresh: got nil error, want one for a 500 response")
	}
}
