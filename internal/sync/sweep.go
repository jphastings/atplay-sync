// internal/sync/sweep.go
package sync

import (
	"context"
	"database/sql"
	"time"

	appdb "github.com/jphastings/game-status/internal/db"
)

// RunStatusSweep is a pure safety net — deletes any games.atmosphere.status
// record whose staleAt has passed, for every signed-in user, regardless of
// current sync state. Reconcile already deletes anything not in its desired
// set on every tick/presence event; this only catches whatever that missed
// (a crash, a process restart mid-tick, a source disabled without ever
// passing through "not playing"). Scoped to every signed-in user rather
// than just those currently syncing, since it exists to catch records that
// outlived their owner's current sync state.
func RunStatusSweep(ctx context.Context, conn *sql.DB, writer RecordWriter, now time.Time) error {
	dids, err := appdb.ListAllDIDs(ctx, conn)
	if err != nil {
		return err
	}
	for _, did := range dids {
		entries, err := writer.ListStatuses(ctx, did)
		if err != nil {
			continue // uncertain outcome (e.g. a network blip) — try again on tomorrow's sweep
		}
		for _, entry := range entries {
			if entry.StaleAt.Before(now) {
				if err := writer.DeleteStatus(ctx, did, entry.Rkey); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
