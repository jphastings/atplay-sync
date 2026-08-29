package db

import (
	"context"
	"database/sql"
	"time"
)

// SteamSource is the session_starts.source value for Steam-driven sessions.
const SteamSource = "steam"

type SessionStart struct {
	GameKey   string
	StartedAt time.Time
}

func GetSessionStart(ctx context.Context, conn *sql.DB, did, source string) (*SessionStart, error) {
	var s SessionStart
	var startedAt string
	err := conn.QueryRowContext(ctx, `SELECT game_key, started_at FROM session_starts WHERE did = ? AND source = ?`, did, source).
		Scan(&s.GameKey, &startedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if s.StartedAt, err = time.Parse(time.RFC3339, startedAt); err != nil {
		return nil, err
	}
	return &s, nil
}

func SetSessionStart(ctx context.Context, conn *sql.DB, did, source, gameKey string, startedAt time.Time) error {
	_, err := conn.ExecContext(ctx, `
		INSERT INTO session_starts (did, source, game_key, started_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(did, source) DO UPDATE SET game_key = excluded.game_key, started_at = excluded.started_at
	`, did, source, gameKey, startedAt.UTC().Format(time.RFC3339))
	return err
}

func ClearSessionStart(ctx context.Context, conn *sql.DB, did, source string) error {
	_, err := conn.ExecContext(ctx, `DELETE FROM session_starts WHERE did = ? AND source = ?`, did, source)
	return err
}
