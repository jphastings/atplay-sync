package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

const defaultDetectableURL = "https://discord.com/api/v10/applications/detectable"

type detectableEntry struct {
	ID             string `json:"id"`
	ThirdPartySKUs []struct {
		Distributor string `json:"distributor"`
		ID          string `json:"id"`
	} `json:"third_party_skus"`
}

// GameIndex maps a presence activity's application_id to a Steam App ID.
// Confirmed live (design session, 2026-08-30): ~77% of Discord's ~24k
// detectable games carry one. Safe for concurrent Refresh/SteamAppID calls.
type GameIndex struct {
	mu             sync.RWMutex
	steamAppID     map[string]string
	HTTPClient     *http.Client
	detectableURL  string // overridable for tests
}

func NewGameIndex() *GameIndex {
	return &GameIndex{steamAppID: map[string]string{}, HTTPClient: http.DefaultClient, detectableURL: defaultDetectableURL}
}

func (g *GameIndex) Refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.detectableURL, nil)
	if err != nil {
		return err
	}
	resp, err := g.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch detectable applications: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch detectable applications: unexpected status %d", resp.StatusCode)
	}

	var entries []detectableEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return fmt.Errorf("decode detectable applications: %w", err)
	}

	next := make(map[string]string, len(entries))
	for _, e := range entries {
		for _, sku := range e.ThirdPartySKUs {
			if sku.Distributor == "steam" && sku.ID != "" {
				next[e.ID] = sku.ID
				break // first steam SKU wins; a small minority of entries list more than one
			}
		}
	}

	g.mu.Lock()
	g.steamAppID = next
	g.mu.Unlock()
	return nil
}

// SteamAppID looks up the Steam App ID for a Discord application_id, as of
// the most recent Refresh. ok=false covers both "unknown application_id"
// and "known, but no Steam release" — both mean "skip" to callers.
func (g *GameIndex) SteamAppID(applicationID string) (string, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	id, ok := g.steamAppID[applicationID]
	return id, ok
}
