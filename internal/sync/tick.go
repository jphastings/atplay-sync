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
	PutStatus(ctx context.Context, did, rkey string, status ActorStatus) error
	DeleteStatus(ctx context.Context, did, rkey string) error
	ListStatuses(ctx context.Context, did string) ([]StatusEntry, error)
}

// StatusEntry is one games.atmosphere.status record as read back off a PDS —
// just enough to diff against what should currently be live (Reconcile) or
// check for expiry (RunStatusSweep). Via lets both of those tell "ours" from
// a status written by a different self-hosted instance of this same app
// sharing the collection — empty Via (pre-via records, or the old rkey="self"
// migration path) is treated as ours too, so cleanup still self-heals.
type StatusEntry struct {
	Rkey    string
	StaleAt time.Time
	Via     string
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

	reconciler := &Reconciler{Conn: conn, Resolver: resolver, Writer: writer}
	for steamID, did := range steamIDToDID {
		summary, ok := summaries[steamID]
		if !ok {
			slog.Warn("steam omitted account from response, skipping this tick", "steam_id", steamID, "did", did)
			continue
		}
		playing := summary.GameID != ""
		if err := UpdateSession(ctx, conn, reconciler, did, appdb.SteamSource, playing, summary.GameID, summary.GameExtraInfo, SessionExtra{}, now); err != nil {
			slog.Error("sync tick failed for account", "did", did, "err", err) // one account's failure shouldn't stop the rest
		}
	}
	return nil
}
