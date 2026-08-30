// internal/db/claims.go
package db

import (
	"context"
	"database/sql"
	"time"
)

// SteamSource is defined in session_starts.go (it's also the
// session_starts.source value for Steam-driven sessions).
const DiscordSource = "discord"

type Claim struct {
	DID            string
	Type           string
	Subject        string
	DisplayName    string
	ClaimURI       string
	RecordURI      string
	LastVerifiedAt time.Time
}

func UpsertClaim(ctx context.Context, conn *sql.DB, c Claim) error {
	_, err := conn.ExecContext(ctx, `
		INSERT INTO claims (did, claim_type, subject, display_name, claim_uri, record_uri, last_verified_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(did, claim_type) DO UPDATE SET
			subject = excluded.subject, display_name = excluded.display_name,
			claim_uri = excluded.claim_uri, record_uri = excluded.record_uri,
			last_verified_at = excluded.last_verified_at
	`, c.DID, c.Type, c.Subject, c.DisplayName, c.ClaimURI, c.RecordURI, c.LastVerifiedAt.UTC().Format(time.RFC3339))
	return err
}

func GetClaim(ctx context.Context, conn *sql.DB, did, claimType string) (*Claim, error) {
	c := Claim{DID: did, Type: claimType}
	var lastVerifiedAt string
	err := conn.QueryRowContext(ctx, `SELECT subject, display_name, claim_uri, record_uri, last_verified_at FROM claims WHERE did = ? AND claim_type = ?`, did, claimType).
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

// DeleteClaim removes a revoked/retracted/deleted claim of one type. It does
// NOT touch sync_prefs.enabled for that source — that's user intent, kept
// separate on purpose (Global Constraints) so the UI can say "enabled, but
// not valid" instead of silently flipping the toggle.
func DeleteClaim(ctx context.Context, conn *sql.DB, did, claimType string) error {
	_, err := conn.ExecContext(ctx, `DELETE FROM claims WHERE did = ? AND claim_type = ?`, did, claimType)
	return err
}
