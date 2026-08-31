// internal/sync/reconcile.go
package sync

import (
	"context"
	"database/sql"
	"strings"
	"time"

	appdb "github.com/jphastings/game-status/internal/db"
)

type Reconciler struct {
	Conn     *sql.DB
	Resolver GameResolver
	Writer   RecordWriter
}

var _ appdb.Reconciler = (*Reconciler)(nil)

// Reconcile computes what should be live for did — one status record per
// distinct game currently being played across all enabled sources, keyed by
// that game's own rkey — and diffs it against what's actually live on the
// PDS, writing new/changed entries and deleting anything no longer desired.
//
// Two sources reporting the same game resolve to the same rkey: the
// priority walk below lets only the first (highest-priority) source to
// claim a given rkey populate it, so same-game conflicts resolve exactly
// like the old single-record design did, just scoped per game instead of
// per account. A game switch (rkey A -> rkey B) needs no special case: A
// simply stops appearing in `desired` (so it's deleted) while B appears (so
// it's created).
//
// Call this after any change to any source's session_starts row, after
// enable/disable, after a priority reorder, and after a claim
// invalidation — nothing else should call Writer.PutStatus/DeleteStatus.
func (r *Reconciler) Reconcile(ctx context.Context, did string, now time.Time) error {
	sources, err := appdb.ListEnabledSourcesByPriority(ctx, r.Conn, did)
	if err != nil {
		return err
	}

	desired := map[string]ActorStatus{}
	for _, source := range sources {
		row, err := appdb.GetSessionStart(ctx, r.Conn, did, source)
		if err != nil {
			return err
		}
		if row == nil {
			continue
		}
		game, err := r.Resolver.GetGameBySteamID(ctx, row.GameKey)
		if err != nil {
			return err
		}
		if game == nil {
			continue
		}
		_, _, rkey, ok := parseAtURI(game.URI)
		if !ok {
			continue // malformed game URI — shouldn't happen from cartridge, but never publish garbage
		}
		if _, claimed := desired[rkey]; claimed {
			continue // a higher-priority source already claimed this game
		}
		desired[rkey] = ActorStatus{
			Type: "games.atmosphere.status", Game: game.URI,
			Playing:   map[string]any{},
			Embed:     &Embed{Type: "app.bsky.embed.external", External: EmbedExternal{URI: game.PageURL, Title: game.Name, Description: game.Summary}},
			CreatedAt: row.StartedAt.UTC().Format(time.RFC3339),
			StaleAt:   now.Add(staleBuffer).UTC().Format(time.RFC3339),
			Via:       ViaClientName,
		}
	}

	live, err := r.Writer.ListStatuses(ctx, did)
	if err != nil {
		return err
	}
	for _, entry := range live {
		if _, ok := desired[entry.Rkey]; !ok {
			if err := r.Writer.DeleteStatus(ctx, did, entry.Rkey); err != nil {
				return err
			}
		}
	}
	for rkey, status := range desired {
		if err := r.Writer.PutStatus(ctx, did, rkey, status); err != nil {
			return err
		}
	}
	return nil
}

// UpdateSession is the source-agnostic half of what tickOne used to do
// alone: given whether a source is currently playing something (and
// what), update that source's session_starts row via the existing pure
// Decide, then let the reconciler decide what's publicly shown. Steam's
// tick and Discord's presence handler both call this — it's the only
// place either one touches session_starts.
func UpdateSession(ctx context.Context, conn *sql.DB, reconciler *Reconciler, did, source string, playing bool, gameKey string, now time.Time) error {
	var prev *SessionStart
	row, err := appdb.GetSessionStart(ctx, conn, did, source)
	if err != nil {
		return err
	}
	if row != nil {
		prev = &SessionStart{GameKey: row.GameKey, StartedAt: row.StartedAt}
	}

	decision := Decide(playing, gameKey, prev, now)
	switch decision.Action {
	case ActionDelete:
		if err := appdb.ClearSessionStart(ctx, conn, did, source); err != nil {
			return err
		}
	case ActionWrite:
		if err := appdb.SetSessionStart(ctx, conn, did, source, decision.GameKey, decision.CreatedAt); err != nil {
			return err
		}
	}
	return reconciler.Reconcile(ctx, did, now)
}

// parseAtURI splits an at:// URI into its did/collection/rkey parts. Used
// both to derive a status record's rkey from the game record it links to,
// and by ATProtoWriter.ListStatuses to read the rkey back off a listed
// record's own uri.
func parseAtURI(atURI string) (did, collection, rkey string, ok bool) {
	const prefix = "at://"
	if !strings.HasPrefix(atURI, prefix) {
		return "", "", "", false
	}
	parts := strings.SplitN(strings.TrimPrefix(atURI, prefix), "/", 3)
	if len(parts) != 3 {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}
