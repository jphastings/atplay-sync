package db

import (
	"context"
	"database/sql"
	"time"
)

// CachedGame's URI is the games.gamesgamesgamesgames.game record's own
// AT-URI (goes in the status record's `game` field); PageURL is cartridge.dev's
// web page for the game (goes in the embed's external.uri) — deliberately
// separate fields, not interchangeable.
type CachedGame struct {
	URI     string
	PageURL string
	Name    string
	Summary string
}

const gameCacheTTL = 24 * time.Hour

func GetCachedGame(ctx context.Context, conn *sql.DB, steamID string) (*CachedGame, error) {
	var g CachedGame
	var cachedAt string
	err := conn.QueryRowContext(ctx, `SELECT game_uri, page_url, name, summary, cached_at FROM game_cache WHERE steam_id = ?`, steamID).
		Scan(&g.URI, &g.PageURL, &g.Name, &g.Summary, &cachedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	at, err := time.Parse(time.RFC3339, cachedAt)
	if err != nil {
		return nil, err
	}
	if time.Since(at) > gameCacheTTL {
		return nil, nil
	}
	return &g, nil
}

func SetCachedGame(ctx context.Context, conn *sql.DB, steamID string, g CachedGame) error {
	_, err := conn.ExecContext(ctx, `
		INSERT INTO game_cache (steam_id, game_uri, page_url, name, summary, cached_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(steam_id) DO UPDATE SET game_uri = excluded.game_uri, page_url = excluded.page_url, name = excluded.name, summary = excluded.summary, cached_at = excluded.cached_at
	`, steamID, g.URI, g.PageURL, g.Name, g.Summary, time.Now().UTC().Format(time.RFC3339))
	return err
}
