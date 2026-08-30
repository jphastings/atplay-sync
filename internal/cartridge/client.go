package cartridge

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/bluesky-social/indigo/atproto/atclient"

	appdb "github.com/jphastings/game-status/internal/db"
)

type Client struct {
	API  *atclient.APIClient
	Conn *sql.DB
}

func New(host, clientKey string, conn *sql.DB) *Client {
	api := atclient.NewAPIClient(host)
	if clientKey != "" {
		api.Headers.Set("x-client-key", clientKey)
	}
	return &Client{API: api, Conn: conn}
}

type getGameOutput struct {
	Game struct {
		URI     string `json:"uri"`
		Name    string `json:"name"`
		Summary string `json:"summary"`
		Slug    string `json:"slug"`
	} `json:"game"`
}

func (c *Client) GetGameBySteamID(ctx context.Context, steamAppID string) (*appdb.CachedGame, error) {
	if cached, err := appdb.GetCachedGame(ctx, c.Conn, steamAppID); err != nil {
		return nil, err
	} else if cached != nil {
		return cached, nil
	}

	var out getGameOutput
	if err := c.API.Get(ctx, "games.gamesgamesgamesgames.getGame", map[string]any{"steamId": steamAppID}, &out); err != nil {
		return nil, fmt.Errorf("cartridge getGame: %w", err)
	}
	if out.Game.URI == "" {
		return nil, nil // not resolvable — caller skips the write this tick (spec)
	}

	game := appdb.CachedGame{URI: out.Game.URI, PageURL: PageURL(out.Game.Slug), Name: out.Game.Name, Summary: out.Game.Summary}
	if err := appdb.SetCachedGame(ctx, c.Conn, steamAppID, game); err != nil {
		return nil, err
	}
	return &game, nil
}

// PageURL is cartridge.dev's web page for a game. Route confirmed from
// gamesgamesgamesgamesgames/cartridge's src/app/(site)/game/[slug]/page.tsx —
// the (site) route group doesn't appear in the URL.
func PageURL(slug string) string {
	return "https://cartridge.dev/game/" + slug
}
