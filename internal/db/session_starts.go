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
	// Extra is opaque per-source metadata (raw JSON, "" if none) — today
	// only ever populated by Discord (state/details/party), decoded by
	// internal/sync.Reconcile. Kept as a plain string here so internal/db
	// stays agnostic to its shape.
	Extra string
}

func GetSessionStart(ctx context.Context, conn *sql.DB, did, source string) (*SessionStart, error) {
	var s SessionStart
	var startedAt string
	err := conn.QueryRowContext(ctx, `SELECT game_key, started_at, extra FROM session_starts WHERE did = ? AND source = ?`, did, source).
		Scan(&s.GameKey, &startedAt, &s.Extra)
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

// SetSessionExtra updates a session_starts row's opaque metadata. Kept
// separate from SetSessionStart (rather than an added param) so the sources
// and tests that never have this data (Steam, and every pre-existing
// call site) don't need to thread an always-empty argument through.
func SetSessionExtra(ctx context.Context, conn *sql.DB, did, source, extra string) error {
	_, err := conn.ExecContext(ctx, `UPDATE session_starts SET extra = ? WHERE did = ? AND source = ?`, extra, did, source)
	return err
}

func ClearSessionStart(ctx context.Context, conn *sql.DB, did, source string) error {
	_, err := conn.ExecContext(ctx, `DELETE FROM session_starts WHERE did = ? AND source = ?`, did, source)
	return err
}
