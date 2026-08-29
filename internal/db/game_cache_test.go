package db

import (
	"context"
	"testing"
)

func TestSetCachedGame_RoundTrip(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)

	game := CachedGame{
		URI:     "at://did:plc:repo/games.gamesgamesgames/game/123",
		PageURL: "https://cartridge.dev/games/123",
		Name:    "Test Game",
		Summary: "A test game",
	}

	if err := SetCachedGame(ctx, conn, "76561198000000000", game); err != nil {
		t.Fatalf("SetCachedGame: %v", err)
	}

	g, err := GetCachedGame(ctx, conn, "76561198000000000")
	if err != nil {
		t.Fatalf("GetCachedGame: %v", err)
	}
	if g == nil {
		t.Fatalf("got nil, want CachedGame")
	}
	if g.URI != game.URI || g.PageURL != game.PageURL || g.Name != game.Name || g.Summary != game.Summary {
		t.Fatalf("got %+v, want %+v", g, game)
	}
}

func TestGetCachedGame_MissingReturnsNil(t *testing.T) {
	conn := openTestDB(t)
	g, err := GetCachedGame(context.Background(), conn, "76561198000000000")
	if err != nil {
		t.Fatalf("GetCachedGame: %v", err)
	}
	if g != nil {
		t.Fatalf("got %+v, want nil", g)
	}
}
