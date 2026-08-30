package db

import (
	"context"
	"database/sql"
)

func SetSteamEnabled(ctx context.Context, conn *sql.DB, did string, enabled bool) error {
	e := 0
	if enabled {
		e = 1
	}
	_, err := conn.ExecContext(ctx, `
		INSERT INTO sync_prefs (did, source, enabled, priority) VALUES (?, 'steam', ?, 0)
		ON CONFLICT(did, source) DO UPDATE SET enabled = excluded.enabled
	`, did, e)
	return err
}

func IsSteamEnabled(ctx context.Context, conn *sql.DB, did string) (bool, error) {
	var enabled int
	err := conn.QueryRowContext(ctx, `SELECT enabled FROM sync_prefs WHERE did = ? AND source = 'steam'`, did).Scan(&enabled)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return enabled == 1, nil
}
