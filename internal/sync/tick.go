// internal/sync/tick.go
package sync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	appdb "github.com/jphastings/game-status/internal/db"
	"github.com/jphastings/game-status/internal/steam"
)

// staleBuffer must comfortably exceed the tick interval so one missed tick
// doesn't make a live status look abandoned (spec: "staleAt: now + buffer").
const staleBuffer = 15 * time.Minute

type SteamAPI interface {
	GetPlayerSummaries(ctx context.Context, steamIDs []string) (map[string]steam.PlayerSummary, error)
}

type GameResolver interface {
	GetGameBySteamID(ctx context.Context, steamAppID string) (*appdb.CachedGame, error)
}

type RecordWriter interface {
	PutStatus(ctx context.Context, did string, status ActorStatus) error
	DeleteStatus(ctx context.Context, did string) error
}

// CallBudget guards RunTick's Steam calls against a self-imposed daily
// ceiling. See steam.Budget.
type CallBudget interface {
	Reserve(n int) bool
	Exhaust()
}

func RunTick(ctx context.Context, conn *sql.DB, steamAPI SteamAPI, resolver GameResolver, writer RecordWriter, budget CallBudget, now time.Time) error {
	dids, err := appdb.ListEnabledDIDs(ctx, conn, appdb.SteamSource)
	if err != nil {
		return err
	}
	if len(dids) == 0 {
		return nil
	}

	steamIDs := make([]string, 0, len(dids))
	steamIDToDID := make(map[string]string, len(dids))
	for _, did := range dids {
		claim, err := appdb.GetClaim(ctx, conn, did, appdb.SteamSource)
		if err != nil {
			return err
		}
		if claim == nil {
			continue // claim vanished between the list and here — Jetstream/the daily sweep will settle this
		}
		steamIDs = append(steamIDs, claim.Subject)
		steamIDToDID[claim.Subject] = did
	}

	calls := (len(steamIDs) + steam.BatchSize - 1) / steam.BatchSize
	if calls > 0 && !budget.Reserve(calls) {
		slog.Warn("steam daily call budget exhausted, skipping this tick", "accounts", len(steamIDs), "calls_needed", calls)
		return nil
	}

	summaries, err := steamAPI.GetPlayerSummaries(ctx, steamIDs)
	if errors.Is(err, steam.ErrRateLimited) {
		budget.Exhaust()
		return fmt.Errorf("steam GetPlayerSummaries: %w", err)
	}
	if err != nil {
		return fmt.Errorf("steam GetPlayerSummaries: %w", err)
	}

	for steamID, did := range steamIDToDID {
		summary, ok := summaries[steamID]
		if !ok {
			slog.Warn("steam omitted account from response, skipping this tick", "steam_id", steamID, "did", did)
			continue
		}
		if err := tickOne(ctx, conn, resolver, writer, did, summary, now); err != nil {
			slog.Error("sync tick failed for account", "did", did, "err", err) // one account's failure shouldn't stop the rest
		}
	}
	return nil
}

func tickOne(ctx context.Context, conn *sql.DB, resolver GameResolver, writer RecordWriter, did string, summary steam.PlayerSummary, now time.Time) error {
	playing := summary.GameID != ""

	var prev *SessionStart
	row, err := appdb.GetSessionStart(ctx, conn, did, appdb.SteamSource)
	if err != nil {
		return err
	}
	if row != nil {
		prev = &SessionStart{GameKey: row.GameKey, StartedAt: row.StartedAt}
	}

	decision := Decide(playing, summary.GameID, prev, now)

	switch decision.Action {
	case ActionDelete:
		if err := writer.DeleteStatus(ctx, did); err != nil {
			return err
		}
		return appdb.ClearSessionStart(ctx, conn, did, appdb.SteamSource)

	case ActionWrite:
		if err := appdb.SetSessionStart(ctx, conn, did, appdb.SteamSource, decision.GameKey, decision.CreatedAt); err != nil {
			return err
		}
		game, err := resolver.GetGameBySteamID(ctx, decision.GameKey)
		if err != nil {
			return err
		}
		if game == nil {
			return nil // not resolvable — skip the write this tick; session_starts is already correct (spec)
		}
		status := ActorStatus{
			Type: "games.gamesgamesgamesgames.actor.status", Game: game.URI,
			Playing:   map[string]any{},
			Embed:     &Embed{Type: "app.bsky.embed.external", External: EmbedExternal{URI: game.PageURL, Title: game.Name, Description: game.Summary}},
			CreatedAt: decision.CreatedAt.UTC().Format(time.RFC3339),
			StaleAt:   now.Add(staleBuffer).UTC().Format(time.RFC3339),
			Via:       ViaClientName,
		}
		return writer.PutStatus(ctx, did, status)
	}
	return nil
}
