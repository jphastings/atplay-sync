package db

import (
	"context"
	"database/sql"
	"time"
)

type SteamClaim struct {
	DID            string
	Subject        string
	DisplayName    string
	ClaimURI       string
	RecordURI      string
	LastVerifiedAt time.Time
}

func UpsertSteamClaim(ctx context.Context, conn *sql.DB, c SteamClaim) error {
	_, err := conn.ExecContext(ctx, `
		INSERT INTO claims (did, claim_type, subject, display_name, claim_uri, record_uri, last_verified_at)
		VALUES (?, 'steam', ?, ?, ?, ?, ?)
		ON CONFLICT(did, claim_type) DO UPDATE SET
			subject = excluded.subject, display_name = excluded.display_name,
			claim_uri = excluded.claim_uri, record_uri = excluded.record_uri,
			last_verified_at = excluded.last_verified_at
	`, c.DID, c.Subject, c.DisplayName, c.ClaimURI, c.RecordURI, c.LastVerifiedAt.UTC().Format(time.RFC3339))
	return err
}

func GetSteamClaim(ctx context.Context, conn *sql.DB, did string) (*SteamClaim, error) {
	c := SteamClaim{DID: did}
	var lastVerifiedAt string
	err := conn.QueryRowContext(ctx, `SELECT subject, display_name, claim_uri, record_uri, last_verified_at FROM claims WHERE did = ? AND claim_type = 'steam'`, did).
		Scan(&c.Subject, &c.DisplayName, &c.ClaimURI, &c.RecordURI, &lastVerifiedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if c.LastVerifiedAt, err = time.Parse(time.RFC3339, lastVerifiedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

// InvalidateSteamClaim removes a revoked/retracted/deleted claim. It does NOT
// touch sync_prefs.enabled — that's user intent, kept separate on
// purpose (Global Constraints) so the UI can say "enabled, but not valid"
// instead of silently flipping the toggle.
func InvalidateSteamClaim(ctx context.Context, conn *sql.DB, did string) error {
	_, err := conn.ExecContext(ctx, `DELETE FROM claims WHERE did = ? AND claim_type = 'steam'`, did)
	return err
}
