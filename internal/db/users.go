package db

import (
	"context"
	"database/sql"
	"time"
)

type User struct {
	DID             string
	ActiveSessionID string
	CreatedAt       time.Time
}

func UpsertUser(ctx context.Context, conn *sql.DB, did string) error {
	_, err := conn.ExecContext(ctx, `
		INSERT INTO users (did, created_at) VALUES (?, ?)
		ON CONFLICT(did) DO NOTHING
	`, did, time.Now().UTC().Format(time.RFC3339))
	return err
}

func SetActiveSession(ctx context.Context, conn *sql.DB, did, sessionID string) error {
	_, err := conn.ExecContext(ctx, `UPDATE users SET active_session_id = ? WHERE did = ?`, sessionID, did)
	return err
}

func GetUser(ctx context.Context, conn *sql.DB, did string) (*User, error) {
	var u User
	u.DID = did
	var createdAt string
	var activeSessionID sql.NullString
	err := conn.QueryRowContext(ctx, `SELECT active_session_id, created_at FROM users WHERE did = ?`, did).
		Scan(&activeSessionID, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u.ActiveSessionID = activeSessionID.String
	if u.CreatedAt, err = time.Parse(time.RFC3339, createdAt); err != nil {
		return nil, err
	}
	return &u, nil
}

// ListAllDIDs returns every signed-in user, regardless of claim or sync
// state — the Jetstream watch list needs to cover anyone who might link or
// unlink a claim, not just accounts already syncing.
func ListAllDIDs(ctx context.Context, conn *sql.DB) ([]string, error) {
	rows, err := conn.QueryContext(ctx, `SELECT did FROM users`)
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
