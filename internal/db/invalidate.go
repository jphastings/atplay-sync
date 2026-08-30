// internal/db/invalidate.go
package db

import (
	"context"
	"database/sql"
)

// StatusDeleter removes a user's live status record from their own PDS. It
// lives here, rather than being imported from the package that implements it,
// so both internal/claims and internal/jetstream can reach the invalidation
// helper below without importing each other.
type StatusDeleter interface {
	DeleteStatus(ctx context.Context, did string) error
}

// InvalidateClaim is the complete undo of a verified claim, and the only way
// any caller should do it: three separate call sites used to each do a
// different subset. Clearing session_starts matters most — it is the only
// non-idempotent local state, so a user who is revoked mid-session and later
// re-verifies would otherwise resume the same game with the original, stale
// createdAt, asserting a play session that spanned the whole revocation.
//
// Sync is turned off too — an invalidated claim means we've lost confidence
// in the identity link, so broadcasting under it should stop rather than
// silently resume the moment a new claim reappears. Re-enabling is a fresh,
// explicit choice (consent is the point), not an automatic side effect of
// re-linking.
func InvalidateClaim(ctx context.Context, conn *sql.DB, deleter StatusDeleter, did, source string) error {
	if err := DeleteClaim(ctx, conn, did, source); err != nil {
		return err
	}
	if err := SetEnabled(ctx, conn, did, source, false); err != nil {
		return err
	}
	if err := ClearSessionStart(ctx, conn, did, source); err != nil {
		return err
	}
	return deleter.DeleteStatus(ctx, did)
}
