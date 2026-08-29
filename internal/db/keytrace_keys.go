// internal/db/keytrace_keys.go
package db

import (
	"context"
	"database/sql"
	"time"
)

type KeytraceKey struct {
	AtURI      string
	PublicJWK  string
	ValidFrom  string
	ValidUntil string
}

// GetKeytraceKey / SetKeytraceKey cache forever (no TTL): the record at a
// given AT-URI is immutable once published, and keytrace rotates to a *new*
// URI daily rather than mutating an old one.
func GetKeytraceKey(ctx context.Context, conn *sql.DB, atURI string) (*KeytraceKey, error) {
	k := KeytraceKey{AtURI: atURI}
	err := conn.QueryRowContext(ctx, `SELECT public_jwk, valid_from, valid_until FROM keytrace_key_cache WHERE at_uri = ?`, atURI).
		Scan(&k.PublicJWK, &k.ValidFrom, &k.ValidUntil)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &k, nil
}

func SetKeytraceKey(ctx context.Context, conn *sql.DB, k KeytraceKey) error {
	_, err := conn.ExecContext(ctx, `
		INSERT INTO keytrace_key_cache (at_uri, public_jwk, valid_from, valid_until, cached_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(at_uri) DO NOTHING
	`, k.AtURI, k.PublicJWK, k.ValidFrom, k.ValidUntil, time.Now().UTC().Format(time.RFC3339))
	return err
}
