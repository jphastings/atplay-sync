package steam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const defaultBaseURL = "https://api.steampowered.com/ISteamUser/GetPlayerSummaries/v0002/"

// BatchSize is how many Steam IDs GetPlayerSummaries packs into a single
// HTTP call — exported so callers can compute calls-needed for budgeting
// without duplicating Steam's own limit.
const BatchSize = 100

// ErrRateLimited means Steam itself returned 429 — its own enforcement, a
// more reliable signal than any self-imposed daily budget.
var ErrRateLimited = errors.New("steam: rate limited (429)")

type PlayerSummary struct {
	SteamID       string
	GameID        string
	GameExtraInfo string
}

type Client struct {
	APIKey     string
	HTTPClient *http.Client
	BaseURL    string // overridable for tests
}

func New(apiKey string) *Client {
	return &Client{APIKey: apiKey, HTTPClient: http.DefaultClient, BaseURL: defaultBaseURL}
}

type summariesResponse struct {
	Response struct {
		Players []struct {
			SteamID       string `json:"steamid"`
			GameID        string `json:"gameid"`
			GameExtraInfo string `json:"gameextrainfo"`
		} `json:"players"`
	} `json:"response"`
}

func (c *Client) GetPlayerSummaries(ctx context.Context, steamIDs []string) (map[string]PlayerSummary, error) {
	result := make(map[string]PlayerSummary, len(steamIDs))

	for start := 0; start < len(steamIDs); start += BatchSize {
		end := min(start+BatchSize, len(steamIDs))
		batch := steamIDs[start:end]

		reqURL := c.BaseURL + "?" + url.Values{"key": {c.APIKey}, "steamids": {strings.Join(batch, ",")}}.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, err
		}
		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("steam GetPlayerSummaries: %w", err)
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			return nil, ErrRateLimited
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			return nil, fmt.Errorf("steam GetPlayerSummaries: unexpected status %d: %s", resp.StatusCode, body)
		}
		var parsed summariesResponse
		err = json.NewDecoder(resp.Body).Decode(&parsed)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("decode steam response: %w", err)
		}

		for _, p := range parsed.Response.Players {
			result[p.SteamID] = PlayerSummary{SteamID: p.SteamID, GameID: p.GameID, GameExtraInfo: p.GameExtraInfo}
		}
	}

	return result, nil
}
