// internal/api/jetstream.go
package api

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/jphastings/game-status/internal/db"
	"github.com/jphastings/game-status/internal/jetstream"
)

// restartJetstreamWatch re-applies the full watch list off the request
// path: Manager.Restart reads the DID list itself, so concurrent callers
// can't apply a stale one. Every signed-in user is watched, not just those
// currently syncing, so a first-time claim link or an unlink is caught
// live instead of only on the next explicit recheck.
func restartJetstreamWatch(mgr *jetstream.Manager, conn *sql.DB) {
	if mgr == nil {
		return
	}
	go func() {
		if err := mgr.Restart(context.Background(), func(ctx context.Context) ([]string, error) {
			return db.ListAllDIDs(ctx, conn)
		}); err != nil {
			slog.Error("jetstream restart", "err", err)
		}
	}()
}
