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
		INSERT INTO sync_prefs (did, steam_enabled) VALUES (?, ?)
		ON CONFLICT(did) DO UPDATE SET steam_enabled = excluded.steam_enabled
	`, did, e)
	return err
}

func IsSteamEnabled(ctx context.Context, conn *sql.DB, did string) (bool, error) {
	var enabled int
	err := conn.QueryRowContext(ctx, `SELECT steam_enabled FROM sync_prefs WHERE did = ?`, did).Scan(&enabled)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return enabled == 1, nil
}
