package db

import (
	"context"
	"database/sql"
)

// defaultPriority only applies the first time a (did, source) row is
// created — an existing row's priority is only ever changed by an explicit
// reorder (SetSourceOrder).
var defaultPriority = map[string]int{SteamSource: 0, DiscordSource: 1}

func SetEnabled(ctx context.Context, conn *sql.DB, did, source string, enabled bool) error {
	e := 0
	if enabled {
		e = 1
	}
	_, err := conn.ExecContext(ctx, `
		INSERT INTO sync_prefs (did, source, enabled, priority) VALUES (?, ?, ?, ?)
		ON CONFLICT(did, source) DO UPDATE SET enabled = excluded.enabled
	`, did, source, e, defaultPriority[source])
	return err
}

func IsEnabled(ctx context.Context, conn *sql.DB, did, source string) (bool, error) {
	var enabled int
	err := conn.QueryRowContext(ctx, `SELECT enabled FROM sync_prefs WHERE did = ? AND source = ?`, did, source).Scan(&enabled)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return enabled == 1, nil
}

// ListEnabledDIDs returns DIDs eligible to sync a given source right now:
// user intent (sync_prefs) AND claim validity (claims) both hold.
func ListEnabledDIDs(ctx context.Context, conn *sql.DB, source string) ([]string, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT sp.did FROM sync_prefs sp
		JOIN claims c ON c.did = sp.did AND c.claim_type = sp.source
		WHERE sp.source = ? AND sp.enabled = 1
	`, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var dids []string
	for rows.Next() {
		var did string
		if err := rows.Scan(&did); err != nil {
			return nil, err
		}
		dids = append(dids, did)
	}
	return dids, rows.Err()
}

// SetSourceOrder persists a drag-to-reorder priority: order[0] is
// highest-priority. Only touches rows already present in sync_prefs (a
// source that's never been enabled has nothing to reorder).
func SetSourceOrder(ctx context.Context, conn *sql.DB, did string, order []string) error {
	for i, source := range order {
		if _, err := conn.ExecContext(ctx, `UPDATE sync_prefs SET priority = ? WHERE did = ? AND source = ?`, i, did, source); err != nil {
			return err
		}
	}
	return nil
}

// ListEnabledSourcesByPriority returns this user's enabled sync sources,
// highest-priority first — the order Reconcile (internal/sync) walks.
func ListEnabledSourcesByPriority(ctx context.Context, conn *sql.DB, did string) ([]string, error) {
	rows, err := conn.QueryContext(ctx, `SELECT source FROM sync_prefs WHERE did = ? AND enabled = 1 ORDER BY priority ASC`, did)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sources []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		sources = append(sources, s)
	}
	return sources, rows.Err()
}

// ListAllSourcesOrdered returns every sync_prefs source for this user,
// enabled ones first (each ordered by priority), disabled ones after —
// unlike ListEnabledSourcesByPriority, this includes disabled sources so
// the frontend can render the full drag list.
func ListAllSourcesOrdered(ctx context.Context, conn *sql.DB, did string) ([]string, error) {
	rows, err := conn.QueryContext(ctx, `SELECT source FROM sync_prefs WHERE did = ? ORDER BY enabled DESC, priority ASC`, did)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sources []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		sources = append(sources, s)
	}
	return sources, rows.Err()
}
